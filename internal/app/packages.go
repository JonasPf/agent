package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/andybalholm/brotli"
)

// Programs a conversation asks for come from Nix (ADR-072). The agent installs
// them into one store that every conversation's tools read and none writes: a
// path there is named by a hash of what it is made from and never changes once
// it exists, so what one conversation installs another can run but not alter.
// A conversation's PATH carries what it installed and nothing another did.

// nixRelease is where packages come from. Moving to the next stable release is
// a change to the agent, made twice a year.
const nixRelease = "nixos-26.05"

const (
	// keepFree is the disk an install must leave free: the transcripts live on
	// the same disk, and a full one stops every conversation, not one.
	keepFree = 2 << 30
	// refreshEvery is how often the release is asked for a newer revision.
	refreshEvery = 24 * time.Hour
)

// nixSource is where the release and its binary cache are served from. Tests
// serve their own; Unsigned is for theirs, whose paths nobody signed.
type nixSource struct {
	Channel  string
	Cache    string
	Unsigned bool
}

var defaultNixSource = nixSource{
	Channel: "https://channels.nixos.org/" + nixRelease,
	Cache:   "https://cache.nixos.org",
}

// Package is one thing a conversation installed: its name in the release, the
// version it got, and the exact store paths, which a fork, an import, or a
// restart puts back without asking the release again.
type Package struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Paths   []string `json:"paths"`
}

// nixMu is held by everything that writes the store or collects it, across the
// whole agent: one install at a time, because finding what a package needs
// takes hundreds of megabytes and the server has few to spare, and a
// collection must never run between an install and the root that keeps it.
var nixMu sync.Mutex

// Packages is the agent's side of Nix: the release's package list, kept as a
// small index for search, and the roots that keep each conversation's
// packages in the store.
type Packages struct {
	src nixSource
	bin string
	dir string

	imu sync.Mutex
	db  *sql.DB
	// collecting is the collections still running after a delete.
	collecting sync.WaitGroup
}

func newPackages(dataDir string, src nixSource) *Packages {
	bin, err := exec.LookPath("nix")
	if err != nil {
		bin = "nix"
	}
	return &Packages{src: src, bin: bin, dir: dataDir}
}

// roots is where a conversation's roots live: one link per path it
// installed, which Nix keeps alive for as long as the link is there.
func (p *Packages) roots(sessionID string) string {
	return filepath.Join(p.dir, "packages", sessionID)
}

// nix runs one Nix command as the agent, with a configuration of its own: the
// store at /nix, only the release's cache, nothing ever built here, and two
// downloads at a time, which is what keeps an install inside the server's
// memory.
func (p *Packages) nix(ctx context.Context, args ...string) (string, string, error) {
	conf := []string{
		"experimental-features = nix-command",
		"store = local",
		"substituters = " + p.src.Cache,
		"max-jobs = 0",
		"max-substitution-jobs = 2",
		"http-connections = 4",
		"build-users-group =",
		"sandbox = false",
	}
	if p.src.Unsigned {
		conf = append(conf, "require-sigs = false")
	}
	home := filepath.Join(p.dir, "nix-home")
	env := []string{
		"NIX_CONFIG=" + strings.Join(conf, "\n"),
		"HOME=" + home,
		"XDG_CACHE_HOME=" + filepath.Join(home, "cache"),
		"XDG_CONFIG_HOME=" + filepath.Join(home, "config"),
		"XDG_STATE_HOME=" + filepath.Join(home, "state"),
		"XDG_DATA_HOME=" + filepath.Join(home, "data"),
	}
	for _, k := range []string{"PATH", "SSL_CERT_FILE", "SSL_CERT_DIR", "NIX_SSL_CERT_FILE", "TZ"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	cmd := exec.CommandContext(ctx, p.bin, args...)
	cmd.Env = env
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	return out.String(), errOut.String(), err
}

// nixError is the part of what Nix wrote that says what went wrong, or, where
// it was stopped before it could say, that it was.
func nixError(stderr string, err error) string {
	// Nix writes what it was evaluating first and the cause last.
	lines := strings.Split(stderr, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if _, msg, ok := strings.Cut(lines[i], "error:"); ok && strings.TrimSpace(msg) != "" {
			return truncate(strings.Join(strings.Fields(msg), " "), 300)
		}
	}
	if err != nil {
		return err.Error()
	}
	return truncate(strings.TrimSpace(stderr), 300)
}

// source is the release's own source at this revision, unpacked once into the
// agent's data. Installs evaluate it from there, read-only: Nix unpacking it
// took nearly 500 MB, tar takes a few, and a read-only evaluation of ffmpeg
// some 140 (ADR-072). The revision before it is deleted.
func (p *Packages) source(ctx context.Context, base, rev string) (string, error) {
	root := filepath.Join(p.dir, "nixpkgs")
	dir := filepath.Join(root, rev)
	if _, err := os.Stat(filepath.Join(dir, "default.nix")); err == nil {
		return dir, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/nixexprs.tar.xz", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("%s: %s", req.URL, resp.Status)
	}
	tarball, err := os.CreateTemp(root, "nixexprs-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tarball.Name())
	_, err = io.Copy(tarball, resp.Body)
	if cerr := tarball.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(root, "unpacking-")
	if err != nil {
		return "", err
	}
	if out, err := exec.CommandContext(ctx, "tar", "-xf", tarball.Name(), "--strip-components=1", "-C", tmp).CombinedOutput(); err != nil {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("unpacking %s: %v %s", req.URL, err, truncate(string(out), 200))
	}
	if err := os.Rename(tmp, dir); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	old, _ := os.ReadDir(root)
	for _, e := range old {
		if e.Name() != rev {
			os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
	return dir, nil
}

// index opens the package list's database: a file of its own beside the
// agent's data, where no tool can reach it.
func (p *Packages) index() (*sql.DB, error) {
	if p.db != nil {
		return p.db, nil
	}
	db, err := sql.Open("sqlite", filepath.Join(p.dir, "packages.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS release (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			base TEXT NOT NULL, revision TEXT NOT NULL, checked_at INTEGER NOT NULL);
		CREATE TABLE IF NOT EXISTS package (
			attr TEXT PRIMARY KEY, version TEXT NOT NULL, description TEXT NOT NULL)`); err != nil {
		db.Close()
		return nil, err
	}
	p.db = db
	return db, nil
}

// release is the revision searches read and installs take, with the address
// its files are served from. It asks the release for a newer one at most once
// a day; a list that cannot be refreshed is still used, since an older list is
// better than none.
func (p *Packages) release(ctx context.Context) (base, rev string, err error) {
	p.imu.Lock()
	defer p.imu.Unlock()
	db, err := p.index()
	if err != nil {
		return "", "", err
	}
	var checked int64
	have := db.QueryRow(`SELECT base, revision, checked_at FROM release WHERE id = 1`).Scan(&base, &rev, &checked) == nil
	if have && time.Since(time.Unix(checked, 0)) < refreshEvery {
		return base, rev, nil
	}
	newBase, newRev, ferr := p.newest(ctx)
	if ferr == nil && (!have || newRev != rev) {
		ferr = p.load(ctx, db, newBase)
	}
	if ferr != nil {
		if have {
			log.Printf("packages: the %s list could not be refreshed, using revision %s: %v", nixRelease, short(rev), ferr)
			return base, rev, nil
		}
		return "", "", fmt.Errorf("the %s package list could not be read: %v", nixRelease, ferr)
	}
	if _, err := db.Exec(`INSERT INTO release (id, base, revision, checked_at) VALUES (1, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET base = excluded.base, revision = excluded.revision, checked_at = excluded.checked_at`,
		newBase, newRev, time.Now().Unix()); err != nil {
		return "", "", err
	}
	return newBase, newRev, nil
}

// newest asks the channel for its current revision. The channel redirects to
// that revision's directory, which is where its list and its source are.
func (p *Packages) newest(ctx context.Context) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", p.src.Channel+"/git-revision", nil)
	if err != nil {
		return "", "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("%s: %s", req.URL, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", "", err
	}
	base := strings.TrimSuffix(resp.Request.URL.String(), "/git-revision")
	return base, strings.TrimSpace(string(body)), nil
}

// load replaces the index with a revision's package list. The list is some
// four hundred megabytes of JSON, so it is read as it arrives, one package at
// a time, and only name, version, and description are kept.
func (p *Packages) load(ctx context.Context, db *sql.DB, base string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/packages.json.br", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s: %s", req.URL, resp.Status)
	}
	dec := json.NewDecoder(brotli.NewReader(resp.Body))
	if err := expectDelim(dec, '{'); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM package`); err != nil {
		return err
	}
	put, err := tx.Prepare(`INSERT OR REPLACE INTO package (attr, version, description) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer put.Close()
	n := 0
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return err
		}
		if key != "packages" {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return err
			}
			continue
		}
		if err := expectDelim(dec, '{'); err != nil {
			return err
		}
		for dec.More() {
			attr, err := dec.Token()
			if err != nil {
				return err
			}
			var v struct {
				Version string `json:"version"`
				Meta    struct {
					Description string `json:"description"`
				} `json:"meta"`
			}
			if err := dec.Decode(&v); err != nil {
				return err
			}
			if _, err := put.Exec(attr, v.Version, v.Meta.Description); err != nil {
				return err
			}
			n++
		}
		if err := expectDelim(dec, '}'); err != nil {
			return err
		}
	}
	if n == 0 {
		return fmt.Errorf("%s/packages.json.br lists no packages", base)
	}
	return tx.Commit()
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok != want {
		return fmt.Errorf("package list: expected %q, found %v", want, tok)
	}
	return nil
}

// Search lists the packages whose name or description holds every word.
func (p *Packages) Search(ctx context.Context, query string) (string, error) {
	words := strings.Fields(query)
	if len(words) == 0 {
		return "", fmt.Errorf("search for at least one word")
	}
	_, rev, err := p.release(ctx)
	if err != nil {
		return "", err
	}
	db, err := p.index()
	if err != nil {
		return "", err
	}
	var where []string
	var args []any
	for _, w := range words {
		like := "%" + likeEscape.Replace(w) + "%"
		where = append(where, `(attr LIKE ? ESCAPE '\' OR description LIKE ? ESCAPE '\')`)
		args = append(args, like, like)
	}
	q := strings.Join(words, " ")
	args = append(args, q, likeEscape.Replace(q)+"%")
	rows, err := db.QueryContext(ctx, `SELECT attr, version, description FROM package WHERE `+
		strings.Join(where, " AND ")+
		` ORDER BY CASE WHEN attr = ? THEN 0 WHEN attr LIKE ? ESCAPE '\' THEN 1 ELSE 2 END, length(attr), attr LIMIT 25`, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var attr, version, desc string
		if err := rows.Scan(&attr, &version, &desc); err != nil {
			return "", err
		}
		lines = append(lines, strings.TrimSuffix(fmt.Sprintf("%s %s — %s", attr, version, desc), " — "))
	}
	if len(lines) == 0 {
		return fmt.Sprintf("No package in %s (revision %s) matches %q.", nixRelease, short(rev), q), nil
	}
	return fmt.Sprintf("Packages in %s (revision %s) matching %q, at most 25:\n%s\nInstall one with install and its name, the first word of the line.",
		nixRelease, short(rev), q, strings.Join(lines, "\n")), nil
}

var likeEscape = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func short(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

// lookup says whether the release has a package of this name, and its version.
func (p *Packages) lookup(ctx context.Context, name string) (string, bool, error) {
	db, err := p.index()
	if err != nil {
		return "", false, err
	}
	var version string
	err = db.QueryRowContext(ctx, `SELECT version FROM package WHERE attr = ?`, name).Scan(&version)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return version, err == nil, err
}

// closest are the release's names nearest to one it does not have: those
// containing it first, then those a few letters away.
func (p *Packages) closest(ctx context.Context, name string) []string {
	db, err := p.index()
	if err != nil {
		return nil
	}
	rows, err := db.QueryContext(ctx, `SELECT attr FROM package`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	type near struct {
		attr string
		d    int
	}
	var all []near
	lower := strings.ToLower(name)
	for rows.Next() {
		var attr string
		if rows.Scan(&attr) != nil {
			continue
		}
		a := strings.ToLower(attr)
		d := editDistance(lower, a)
		if strings.Contains(a, lower) {
			d = 0
		}
		if d <= 2+len(name)/4 {
			all = append(all, near{attr, d})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].d != all[j].d {
			return all[i].d < all[j].d
		}
		return len(all[i].attr) < len(all[j].attr)
	})
	var out []string
	for i := 0; i < len(all) && i < 5; i++ {
		out = append(out, all[i].attr)
	}
	return out
}

func editDistance(a, b string) int {
	if len(a) > len(b) {
		a, b = b, a
	}
	if len(b)-len(a) > 8 {
		return len(b) - len(a)
	}
	prev := make([]int, len(a)+1)
	cur := make([]int, len(a)+1)
	for i := range prev {
		prev[i] = i
	}
	for j := 1; j <= len(b); j++ {
		cur[0] = j
		for i := 1; i <= len(a); i++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[i] = min(prev[i]+1, cur[i-1]+1, prev[i-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(a)]
}

// resolved is what the release's source says a package is: its version, and
// the store paths of the outputs a person installing it would want.
type resolved struct {
	Version string   `json:"version"`
	Paths   []string `json:"paths"`
}

const resolveExpr = `p: {
  version = p.version or "";
  paths = map (o: p.${o}.outPath) (p.meta.outputsToInstall or [ (p.outputName or "out") ]);
}`

// Install installs each name into a conversation, one after another, and
// records each with record as it lands. It stops at the first that cannot be
// installed, saying what it did install before that.
func (p *Packages) Install(ctx context.Context, sess *Session, names []string, record func(Package) error) (string, error) {
	if len(names) == 0 {
		return "", fmt.Errorf("name at least one package to install")
	}
	nixMu.Lock()
	defer nixMu.Unlock()
	base, rev, err := p.release(ctx)
	if err != nil {
		return "", err
	}
	have := map[string]Package{}
	for _, pk := range sess.Packages {
		have[pk.Name] = pk
	}
	var done []string
	refuse := func(format string, a ...any) (string, error) {
		msg := fmt.Sprintf(format, a...)
		if len(done) > 0 {
			msg = strings.Join(done, "\n") + "\n" + msg
		}
		return "", fmt.Errorf("%s", msg)
	}
	for _, name := range names {
		if pk, ok := have[name]; ok && p.present(pk.Paths) {
			done = append(done, fmt.Sprintf("%s %s was already installed in this conversation: %s.", name, pk.Version, strings.Join(pk.Paths, ", ")))
			continue
		}
		if _, ok, err := p.lookup(ctx, name); err != nil {
			return "", err
		} else if !ok {
			msg := fmt.Sprintf("%s has no package named %q.", nixRelease, name)
			if near := p.closest(ctx, name); len(near) > 0 {
				msg += " Closest: " + strings.Join(near, ", ") + "."
			} else {
				msg += " Search for it first."
			}
			return refuse("%s", msg)
		}
		src, err := p.source(ctx, base, rev)
		if err != nil {
			return refuse("the %s source could not be fetched: %v", nixRelease, err)
		}
		out, stderr, err := p.nix(ctx, "eval", "--read-only", "--json", "--file", src, name, "--apply", resolveExpr)
		if err != nil {
			return refuse("%s cannot be installed from %s: %s", name, nixRelease, nixError(stderr, err))
		}
		var r resolved
		if err := json.Unmarshal([]byte(out), &r); err != nil || len(r.Paths) == 0 {
			return refuse("%s: the release's source did not say what to install: %s", name, truncate(out, 200))
		}
		fetch, disk, why := p.cost(ctx, r.Paths)
		if why != "" {
			return refuse("%s %s: %s", name, r.Version, why)
		}
		if free := freeBytes(); disk > 0 && free < disk+keepFree {
			return refuse("%s %s needs %s on disk and %s is free; installs keep %s free.",
				name, r.Version, fmtBytes(disk), fmtBytes(free), fmtBytes(keepFree))
		}
		if _, stderr, err := p.nix(ctx, append([]string{"build", "--out-link", filepath.Join(p.roots(sess.ID), name)}, r.Paths...)...); err != nil {
			return refuse("%s %s could not be fetched: %s", name, r.Version, nixError(stderr, err))
		}
		pk := Package{Name: name, Version: r.Version, Paths: r.Paths}
		if err := record(pk); err != nil {
			return "", err
		}
		line := fmt.Sprintf("Installed %s %s from %s (revision %s): %s (%s fetched, %s on disk).",
			name, r.Version, nixRelease, short(rev), strings.Join(r.Paths, ", "), fmtBytes(fetch), fmtBytes(disk))
		if progs := programs(r.Paths); len(progs) > 0 {
			line += " On PATH from your next call: " + strings.Join(progs, ", ") + "."
		} else {
			line += " It has no programs of its own (nothing in bin/)."
		}
		done = append(done, line)
	}
	return strings.Join(done, "\n"), nil
}

// present says whether every path is in the store.
func (p *Packages) present(paths []string) bool {
	for _, path := range paths {
		if _, err := os.Lstat(path); err != nil {
			return false
		}
	}
	return len(paths) > 0
}

var fetchedRe = regexp.MustCompile(`will be fetched \(([\d.]+) (\w+) download, ([\d.]+) (\w+) unpacked\)`)

// cost is what fetching these paths would download and put on disk, asked of
// the cache before anything is downloaded. A path the cache cannot give would
// have to be built, which nothing here does; why says so.
func (p *Packages) cost(ctx context.Context, paths []string) (fetch, disk uint64, why string) {
	_, stderr, err := p.nix(ctx, append([]string{"build", "--dry-run"}, paths...)...)
	if strings.Contains(stderr, "will be built") || strings.Contains(stderr, "don't know how to build") {
		return 0, 0, "it is not in the binary cache for " + nixRelease + ", and nothing is built here."
	}
	if err != nil {
		return 0, 0, "the cache could not be asked about it: " + nixError(stderr, err)
	}
	if m := fetchedRe.FindStringSubmatch(stderr); m != nil {
		return sizeOf(m[1], m[2]), sizeOf(m[3], m[4]), ""
	}
	return 0, 0, ""
}

func sizeOf(n, unit string) uint64 {
	f, _ := strconv.ParseFloat(n, 64)
	mult := map[string]float64{"B": 1, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40, "PiB": 1 << 50, "EiB": 1 << 60}[unit]
	if mult == 0 {
		mult = 1 << 20
	}
	return uint64(f * mult)
}

// freeBytes is what the disk holding the store has free.
func freeBytes() uint64 {
	for _, d := range []string{"/nix/store", "/nix", "/"} {
		var st syscall.Statfs_t
		if syscall.Statfs(d, &st) == nil {
			return uint64(st.Bavail) * uint64(st.Bsize)
		}
	}
	return 0
}

func fmtBytes(n uint64) string {
	f := float64(n)
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	i := 0
	for f >= 1000 && i < len(units)-1 {
		f /= 1000
		i++
	}
	if i > 0 && f < 10 {
		return fmt.Sprintf("%.1f %s", f, units[i])
	}
	return fmt.Sprintf("%.0f %s", f, units[i])
}

// programs are the names in each path's bin/.
func programs(paths []string) []string {
	var out []string
	for _, p := range paths {
		entries, _ := os.ReadDir(filepath.Join(p, "bin"))
		for _, e := range entries {
			out = append(out, e.Name())
		}
	}
	if len(out) > 12 {
		out = append(out[:12], fmt.Sprintf("and %d more", len(out)-12))
	}
	return out
}

// List says what a conversation has installed.
func listPackages(pkgs []Package) string {
	if len(pkgs) == 0 {
		return "This conversation has installed no packages. Search for one with search."
	}
	lines := []string{"Installed in this conversation:"}
	for _, pk := range pkgs {
		line := fmt.Sprintf("%s %s: %s", pk.Name, pk.Version, strings.Join(pk.Paths, ", "))
		if progs := programs(pk.Paths); len(progs) > 0 {
			line += " — " + strings.Join(progs, ", ")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// keep roots a conversation's packages, fetching any path the store does not
// have, as a fork, an import, or a restart needs. It answers the paths it could
// not put back.
func (p *Packages) keep(ctx context.Context, sessionID string, pkgs []Package) []string {
	nixMu.Lock()
	defer nixMu.Unlock()
	var missing []string
	for _, pk := range pkgs {
		if !p.present(pk.Paths) {
			_, disk, why := p.cost(ctx, pk.Paths)
			if why == "" && disk > 0 && freeBytes() < disk+keepFree {
				why = fmt.Sprintf("it needs %s on disk and installs keep %s free", fmtBytes(disk), fmtBytes(keepFree))
			}
			if why != "" {
				log.Printf("packages: %s for %s: %s", pk.Name, sessionID, why)
				missing = append(missing, pk.Paths...)
				continue
			}
		}
		if _, stderr, err := p.nix(ctx, append([]string{"build", "--out-link", filepath.Join(p.roots(sessionID), pk.Name)}, pk.Paths...)...); err != nil {
			log.Printf("packages: %s for %s: %s", pk.Name, sessionID, nixError(stderr, err))
			missing = append(missing, pk.Paths...)
		}
	}
	return missing
}

// forget drops a conversation's roots and collects, in the background, what
// no other conversation keeps.
func (p *Packages) forget(sessionID string) {
	if !validSessionID(sessionID) {
		return
	}
	nixMu.Lock()
	err := os.RemoveAll(p.roots(sessionID))
	nixMu.Unlock()
	if err != nil {
		log.Printf("packages: dropping the roots of %s: %v", sessionID, err)
		return
	}
	p.collectLater()
}

func (p *Packages) collectLater() {
	p.collecting.Add(1)
	go func() {
		defer p.collecting.Done()
		nixMu.Lock()
		defer nixMu.Unlock()
		p.gc()
	}()
}

// collect removes from the store everything no root keeps.
func (p *Packages) collect() {
	nixMu.Lock()
	defer nixMu.Unlock()
	p.gc()
}

func (p *Packages) gc() {
	if _, stderr, err := p.nix(context.Background(), "store", "gc"); err != nil {
		log.Printf("packages: collecting the store: %s", nixError(stderr, err))
	}
}

// idle waits for the collections a delete started.
func (p *Packages) idle() { p.collecting.Wait() }

// restore puts every conversation's packages back after a restart: a store
// that outlived the container keeps them and this only renews their roots; one
// that did not has them fetched again.
func (p *Packages) restore(a *App) {
	for _, s := range a.store.Sessions() {
		if len(s.Packages) == 0 {
			continue
		}
		if missing := p.keep(context.Background(), s.ID, s.Packages); len(missing) > 0 {
			log.Printf("packages: %s could not get back %s", s.ID, strings.Join(missing, ", "))
		}
	}
}

// packagePath is what a conversation's installed programs add to PATH: each
// installed path's bin/, in the order they were installed.
func (a *App) packagePath(sessionID string) string {
	s := a.store.Session(sessionID)
	if s == nil {
		return ""
	}
	var dirs []string
	for _, pk := range s.Packages {
		for _, path := range pk.Paths {
			if st, err := os.Stat(filepath.Join(path, "bin")); err == nil && st.IsDir() {
				dirs = append(dirs, filepath.Join(path, "bin"))
			}
		}
	}
	return strings.Join(dirs, ":")
}

// withPackages puts a conversation's installed programs at the front of PATH.
func (a *App) withPackages(env []string, sessionID string) []string {
	dirs := a.packagePath(sessionID)
	if dirs == "" {
		return env
	}
	path := os.Getenv("PATH")
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	return append(env, "PATH="+dirs+":"+path)
}

func (a *App) tSearchPackages(w http.ResponseWriter, r *http.Request) {
	if a.pkgs == nil {
		fail(w, 503, "packages cannot be installed in this agent")
		return
	}
	q := r.URL.Query().Get("search")
	if q == "" {
		s := a.store.Session(caller(r).SessionID)
		if s == nil {
			fail(w, 404, "no such session")
			return
		}
		writeJSON(w, 200, map[string]string{"content": listPackages(s.Packages)})
		return
	}
	content, err := a.pkgs.Search(r.Context(), q)
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, map[string]string{"content": content})
}

func (a *App) tInstallPackages(w http.ResponseWriter, r *http.Request) {
	if a.pkgs == nil {
		fail(w, 503, "packages cannot be installed in this agent")
		return
	}
	var in struct {
		Install []string `json:"install"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "%v", err)
		return
	}
	id := caller(r).SessionID
	s := a.store.Session(id)
	if s == nil {
		fail(w, 404, "no such session")
		return
	}
	content, err := a.pkgs.Install(r.Context(), s, in.Install, func(pk Package) error {
		_, err := a.store.UpdateSession(id, func(x *Session) {
			for i, have := range x.Packages {
				if have.Name == pk.Name {
					x.Packages[i] = pk
					return
				}
			}
			x.Packages = append(x.Packages, pk)
		})
		return err
	})
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	a.hub.Broadcast(wsEvent{Kind: "sessions"})
	writeJSON(w, 200, map[string]string{"content": content})
}
