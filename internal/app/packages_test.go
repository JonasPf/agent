package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
)

// A conversation installs a program the image does not ship with the packages
// tool, and the agent fetches it into a Nix store every conversation reads and
// none writes (ADR-072). These tests run Nix as shipped; what stands in for
// channels.nixos.org and cache.nixos.org is a release of this test's own, which
// the public web may be (R5).

// fakePkg is one package of a fake release: a program named after it that
// prints its name and version.
type fakePkg struct {
	attr, version, description string
	// uncached leaves it out of the binary cache; huge has the cache claim it
	// is a petabyte.
	uncached, huge bool
	// refuses makes evaluating it fail with this message, as the release
	// refuses an unfree package.
	refuses string
}

// fakeRelease serves a release the way channels.nixos.org does — a channel URL
// redirecting to the newest revision's directory, holding its package list and
// its source — and a binary cache the way cache.nixos.org does.
type fakeRelease struct {
	srv   *httptest.Server
	paths map[string]string // attr → the store path its source evaluates to

	mu       sync.Mutex
	narinfos map[string][]byte // hash part → narinfo
	nars     map[string][]byte // hash part → NAR
	asked    []string          // hash parts whose narinfo was asked for, in order
	fetched  []string          // hash parts whose NAR was downloaded
	// hold, when set, keeps every NAR download waiting until it is closed, and
	// each one that starts waiting is announced on holding.
	hold    chan struct{}
	holding chan string
}

func nixOrNotRun(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("nix"); err != nil {
		notRun(t, "nix is not installed here. The test container and CI install it (testenv/install-nix.sh).")
	}
}

// newFakeRelease builds a release of these packages. Each test's programs get
// names of their own, so a store path another test or another run left behind
// never answers for this one.
func newFakeRelease(t *testing.T, pkgs ...fakePkg) *fakeRelease {
	t.Helper()
	nixOrNotRun(t)
	token := make([]byte, 4)
	_, _ = rand.Read(token)
	tag := hex.EncodeToString(token)

	var src strings.Builder
	src.WriteString(`{ ... }:
let
  mk = name: version: description: derivation {
    inherit name;
    system = builtins.currentSystem;
    builder = "/bin/sh";
    args = [ "-c" "exit 1" ];
  } // { inherit version; meta = { inherit description; outputsToInstall = [ "out" ]; }; };
in {
`)
	list := map[string]any{}
	for _, p := range pkgs {
		if p.refuses != "" {
			fmt.Fprintf(&src, "  %q = (mk %q %q %q) // { meta.outputsToInstall = throw %q; };\n",
				p.attr, p.attr+"-"+tag+"-"+p.version, p.version, p.description, p.refuses)
		} else {
			fmt.Fprintf(&src, "  %q = mk %q %q %q;\n", p.attr, p.attr+"-"+tag+"-"+p.version, p.version, p.description)
		}
		list[p.attr] = map[string]any{"name": p.attr + "-" + p.version, "pname": p.attr, "version": p.version,
			"outputName": "out", "outputs": map[string]any{"out": nil}, "meta": map[string]any{"description": p.description}}
	}
	src.WriteString("}\n")

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "default.nix"), []byte(src.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &fakeRelease{paths: map[string]string{}, narinfos: map[string][]byte{}, nars: map[string][]byte{}}
	for _, p := range pkgs {
		out, err := exec.Command("nix", "--extra-experimental-features", "nix-command",
			"eval", "--raw", "--file", dir, p.attr+".outPath").Output()
		if err != nil {
			t.Fatalf("evaluating %s: %v", p.attr, err)
		}
		path := string(out)
		r.paths[p.attr] = path
		if p.uncached || p.refuses != "" {
			continue
		}
		nar := narOfProgram(p.attr, fmt.Sprintf("#!/bin/sh\necho %s %s\n", p.attr, p.version))
		hash := hashPart(path)
		size := len(nar)
		if p.huge {
			size = 1 << 50
		}
		sum := sha256.Sum256(nar)
		r.narinfos[hash] = []byte(fmt.Sprintf("StorePath: %s\nURL: nar/%s.nar\nCompression: none\n"+
			"FileHash: sha256:%s\nFileSize: %d\nNarHash: sha256:%s\nNarSize: %d\nReferences: \n",
			path, hash, nix32(sum[:]), size, nix32(sum[:]), size))
		r.nars[hash] = nar
	}

	var listing bytes.Buffer
	bw := brotli.NewWriter(&listing)
	if err := json.NewEncoder(bw).Encode(map[string]any{"version": 2, "packages": list}); err != nil {
		t.Fatal(err)
	}
	bw.Close()
	source := tarGz(t, "nixexprs/default.nix", src.String())

	mux := http.NewServeMux()
	mux.HandleFunc("GET /nixos-26.05/{file}", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/releases/nixos-26.05.1."+tag+"/"+req.PathValue("file"), http.StatusFound)
	})
	mux.HandleFunc("GET /releases/{release}/git-revision", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, tag+"0000000000000000000000000000000000")
	})
	mux.HandleFunc("GET /releases/{release}/packages.json.br", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(listing.Bytes())
	})
	// The real source is xz; Nix reads an archive by what it is, not by its name.
	mux.HandleFunc("GET /releases/{release}/nixexprs.tar.xz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(source)
	})
	mux.HandleFunc("GET /cache/nix-cache-info", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "StoreDir: /nix/store\nWantMassQuery: 1\nPriority: 30\n")
	})
	mux.HandleFunc("GET /cache/{file}", func(w http.ResponseWriter, req *http.Request) {
		hash, ok := strings.CutSuffix(req.PathValue("file"), ".narinfo")
		if !ok {
			http.NotFound(w, req)
			return
		}
		r.mu.Lock()
		r.asked = append(r.asked, hash)
		info, found := r.narinfos[hash]
		r.mu.Unlock()
		if !found {
			http.NotFound(w, req)
			return
		}
		w.Write(info)
	})
	mux.HandleFunc("GET /cache/nar/{file}", func(w http.ResponseWriter, req *http.Request) {
		hash := strings.TrimSuffix(req.PathValue("file"), ".nar")
		r.mu.Lock()
		r.fetched = append(r.fetched, hash)
		nar, hold := r.nars[hash], r.hold
		r.mu.Unlock()
		if hold != nil {
			r.holding <- hash
			<-hold
		}
		w.Write(nar)
	})
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	return r
}

// use points an agent at this release.
func (r *fakeRelease) use(a *App) {
	a.pkgs = newPackages(a.cfg.DataDir, nixSource{
		Channel: r.srv.URL + "/nixos-26.05", Cache: r.srv.URL + "/cache", Unsigned: true})
}

func (r *fakeRelease) wasAsked(attr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, h := range r.asked {
		if h == hashPart(r.paths[attr]) {
			return true
		}
	}
	return false
}

func (r *fakeRelease) wasFetched(attr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, h := range r.fetched {
		if h == hashPart(r.paths[attr]) {
			return true
		}
	}
	return false
}

func hashPart(storePath string) string {
	base := filepath.Base(storePath)
	if len(base) < 32 {
		return base
	}
	return base[:32]
}

// narOfProgram is the Nix archive of a directory holding bin/<name>, an
// executable script.
func narOfProgram(name, script string) []byte {
	var b bytes.Buffer
	str := func(s string) {
		var n [8]byte
		binary.LittleEndian.PutUint64(n[:], uint64(len(s)))
		b.Write(n[:])
		b.WriteString(s)
		b.Write(make([]byte, (8-len(s)%8)%8))
	}
	for _, s := range []string{"nix-archive-1", "(", "type", "directory",
		"entry", "(", "name", "bin", "node", "(", "type", "directory",
		"entry", "(", "name", name, "node", "(", "type", "regular", "executable", "", "contents", script,
		")", ")", ")", ")", ")"} {
		str(s)
	}
	return b.Bytes()
}

// nix32 is Nix's base-32, in which a narinfo states a hash.
func nix32(h []byte) string {
	const chars = "0123456789abcdfghijklmnpqrsvwxyz"
	n := (len(h)*8-1)/5 + 1
	out := make([]byte, 0, n)
	for i := n - 1; i >= 0; i-- {
		b := uint(i * 5)
		j, k := b/8, b%8
		c := uint(h[j]) >> k
		if int(j)+1 < len(h) {
			c |= uint(h[j+1]) << (8 - k)
		}
		out = append(out, chars[c&0x1f])
	}
	return string(out)
}

func tarGz(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: filepath.Dir(name) + "/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte(body))
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// packagesApp is an agent whose tools run confined, as shipped, with the tool
// API up, using this release.
func packagesApp(t *testing.T, r *fakeRelease) *App {
	t.Helper()
	root := t.TempDir()
	toolsDir := filepath.Join(root, "tools")
	copyToolTree(t, "../../tools", toolsDir)
	a, _ := confinedApp(t, toolsDir, filepath.Join(root, "skills"), "")
	confinementOrSkip(t, a.sandbox)
	toolAPI(t, a)
	r.use(a)
	return a
}

func useTool(t *testing.T, a *App, sessionID, name string, args any) toolResult {
	t.Helper()
	b, _ := json.Marshal(args)
	return a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: sessionID}, name, b)
}

func shell(t *testing.T, a *App, sessionID, command string) toolResult {
	t.Helper()
	return useTool(t, a, sessionID, "bash", map[string]string{"command": command})
}

func installPkg(t *testing.T, a *App, sessionID string, names ...string) toolResult {
	t.Helper()
	return useTool(t, a, sessionID, "packages", map[string]any{"install": names})
}

func within(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("not within %s: %s", d, what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

var hello = fakePkg{attr: "hello-nix", version: "1.0", description: "Says hello from the store"}

// "A search returns the release's packages matching every word, with name,
// version, and description, and runs no Nix evaluation."
func TestASearchReadsTheReleasesPackageList(t *testing.T) {
	r := newFakeRelease(t, hello, fakePkg{attr: "goodbye-nix", version: "2.1", description: "Says goodbye"})
	a := packagesApp(t, r)
	a.pkgs.bin = filepath.Join(t.TempDir(), "no-nix-here")
	s := newSession(t, a)

	res := useTool(t, a, s.ID, "packages", map[string]string{"search": "says hello"})
	if !res.OK || !strings.Contains(res.Content, "hello-nix 1.0") || !strings.Contains(res.Content, "Says hello from the store") {
		t.Errorf("search = %+v, want hello-nix with its version and description", res)
	}
	if strings.Contains(res.Content, "goodbye-nix") {
		t.Errorf("search for two words found a package matching one: %q", res.Content)
	}
	if res := useTool(t, a, s.ID, "packages", map[string]string{"search": "says"}); !strings.Contains(res.Content, "goodbye-nix 2.1") {
		t.Errorf("search for one word = %q, want both packages", res.Content)
	}
}

// "An install fetches the named packages from the revision the search list
// belongs to; the result names each one's version, store path, and disk taken."
// "An installed package's programs run in the conversation's bash and checks,
// under the sandbox, from the next call on." "Another conversation's PATH does
// not include what this one installed." And with no parameters the tool lists
// what the conversation has.
func TestAnInstalledProgramRunsInItsConversationOnly(t *testing.T) {
	r := newFakeRelease(t, hello)
	a := packagesApp(t, r)
	mine, other := newSession(t, a), newSession(t, a)

	res := installPkg(t, a, mine.ID, "hello-nix")
	if !res.OK || !strings.Contains(res.Content, "hello-nix 1.0") || !strings.Contains(res.Content, r.paths["hello-nix"]) ||
		!strings.Contains(res.Content, "B") {
		t.Fatalf("install = %+v, want the version, the store path, and the disk it took", res)
	}
	if !r.wasFetched("hello-nix") {
		t.Error("the program was not fetched from the release's cache")
	}
	if out := shell(t, a, mine.ID, "hello-nix"); !out.OK || !strings.Contains(out.Content, "hello-nix 1.0") {
		t.Errorf("running it in its conversation = %+v", out)
	}
	if !checkMet(t, a, a.store.Session(mine.ID), "hello-nix | grep -q 'hello-nix 1.0'") {
		t.Error("a check in its conversation cannot run it")
	}
	if out := shell(t, a, other.ID, "command -v hello-nix"); out.OK {
		t.Errorf("another conversation finds it on PATH: %q", out.Content)
	}

	got := getSessionJSON(t, a, mine.ID)
	if len(got.Packages) != 1 || got.Packages[0].Name != "hello-nix" || got.Packages[0].Version != "1.0" ||
		len(got.Packages[0].Paths) != 1 || got.Packages[0].Paths[0] != r.paths["hello-nix"] {
		t.Errorf("the session's packages = %+v", got.Packages)
	}
	if list := useTool(t, a, mine.ID, "packages", map[string]any{}); !list.OK || !strings.Contains(list.Content, "hello-nix 1.0") {
		t.Errorf("listing = %+v, want hello-nix", list)
	}
	if list := useTool(t, a, other.ID, "packages", map[string]any{}); strings.Contains(list.Content, "hello-nix") {
		t.Errorf("another conversation's listing names it: %q", list.Content)
	}
	if again := installPkg(t, a, mine.ID, "hello-nix"); !again.OK || !strings.Contains(again.Content, "already") {
		t.Errorf("installing it twice = %+v, want it to say it was already there", again)
	}
}

// "A program installed with packages runs in the shell under the sandbox, and
// the shell cannot change it." "A tool cannot write ... the Nix store."
func TestAToolCannotChangeTheStore(t *testing.T) {
	r := newFakeRelease(t, hello)
	a := packagesApp(t, r)
	s := newSession(t, a)
	if res := installPkg(t, a, s.ID, "hello-nix"); !res.OK {
		t.Fatalf("install = %+v", res)
	}
	program := filepath.Join(r.paths["hello-nix"], "bin", "hello-nix")
	before, _ := os.ReadFile(program)
	for _, cmd := range []string{
		"echo 'echo changed' > " + program,
		"rm -f " + program,
		"touch /nix/store/planted",
		"mkdir /nix/var/nix/gcroots/planted",
	} {
		if res := shell(t, a, s.ID, cmd); res.OK {
			t.Errorf("%s succeeded: %q", cmd, res.Content)
		}
	}
	if after, _ := os.ReadFile(program); !bytes.Equal(before, after) {
		t.Errorf("the program changed: %q", after)
	}
	if exists("/nix/store/planted") {
		t.Error("a tool wrote into the store")
	}
}

// "A name the release does not have is refused, and the refusal names the
// closest names." "A package the binary cache does not hold is refused, and
// nothing is built." "An install that would leave less than 2 GB of disk free is
// refused before anything is downloaded, naming its size."
func TestAnInstallThatCannotBeDoneIsRefused(t *testing.T) {
	r := newFakeRelease(t, hello,
		fakePkg{attr: "never-built", version: "1.0", description: "Not in the cache", uncached: true},
		fakePkg{attr: "enormous", version: "1.0", description: "A petabyte", huge: true},
		fakePkg{attr: "not-free", version: "1.0", description: "Unfree",
			refuses: "Package ‘not-free-1.0’ has an unfree license (‘unfree’), refusing to evaluate."})
	a := packagesApp(t, r)
	s := newSession(t, a)

	if res := installPkg(t, a, s.ID, "helo-nix"); res.OK || !strings.Contains(res.Error, "hello-nix") {
		t.Errorf("a misspelt name = %+v, want a refusal naming hello-nix", res)
	}
	if res := installPkg(t, a, s.ID, "never-built"); res.OK || !strings.Contains(res.Error, "cache") {
		t.Errorf("a package the cache lacks = %+v, want a refusal saying so", res)
	}
	if exists(r.paths["never-built"]) {
		t.Error("a package the cache lacks was built")
	}
	if res := installPkg(t, a, s.ID, "enormous"); res.OK || !strings.Contains(res.Error, "PB") || !strings.Contains(res.Error, "free") {
		t.Errorf("a package too large = %+v, want a refusal naming its size and what is free", res)
	}
	if r.wasFetched("enormous") {
		t.Error("the package too large was downloaded before it was refused")
	}
	// What the release says, not what Nix was evaluating when it said it.
	if res := installPkg(t, a, s.ID, "not-free"); res.OK || !strings.Contains(res.Error, "has an unfree license") ||
		strings.Contains(res.Error, "while evaluating") {
		t.Errorf("a package the release refuses = %+v, want its reason and nothing else", res)
	}
	if got := getSessionJSON(t, a, s.ID); len(got.Packages) != 0 {
		t.Errorf("refused installs were recorded: %+v", got.Packages)
	}
}

// "Two installs at once run one after the other."
func TestInstallsRunOneAtATime(t *testing.T) {
	second := fakePkg{attr: "second-nix", version: "1.0", description: "Waits its turn"}
	r := newFakeRelease(t, hello, second)
	a := packagesApp(t, r)
	s1, s2 := newSession(t, a), newSession(t, a)
	r.mu.Lock()
	r.hold, r.holding = make(chan struct{}), make(chan string, 4)
	r.mu.Unlock()

	results := make(chan toolResult, 2)
	go func() { results <- installPkg(t, a, s1.ID, "hello-nix") }()
	select {
	case <-r.holding:
	case <-time.After(2 * time.Minute):
		t.Fatal("the first install never started downloading")
	}
	go func() { results <- installPkg(t, a, s2.ID, "second-nix") }()
	time.Sleep(2 * time.Second)
	if r.wasAsked("second-nix") {
		t.Error("the second install reached the cache while the first was downloading")
	}
	r.mu.Lock()
	close(r.hold)
	r.hold = nil
	r.mu.Unlock()
	for range 2 {
		if res := <-results; !res.OK {
			t.Errorf("install = %+v", res)
		}
	}
}

// "A fork has its origin's installed packages on its PATH, without copying them."
func TestAForkHasItsOriginsPackages(t *testing.T) {
	r := newFakeRelease(t, hello)
	a := packagesApp(t, r)
	origin := newSession(t, a)
	if res := installPkg(t, a, origin.ID, "hello-nix"); !res.OK {
		t.Fatalf("install = %+v", res)
	}
	w := callAPI(t, a, "POST", "/sessions/"+origin.ID+"/fork", map[string]any{})
	if w.Code != 201 {
		t.Fatalf("fork = %d %s", w.Code, w.Body.String())
	}
	var fork Session
	_ = json.Unmarshal(w.Body.Bytes(), &fork)
	if out := shell(t, a, fork.ID, "hello-nix"); !out.OK || !strings.Contains(out.Content, "hello-nix 1.0") {
		t.Errorf("running it in the fork = %+v", out)
	}
	if out := shell(t, a, fork.ID, "du -sk . | cut -f1"); strings.TrimSpace(out.Content) == "" {
		t.Errorf("du = %+v", out)
	}
	if found := shell(t, a, fork.ID, "grep -rl 'echo hello-nix' . 2>/dev/null"); strings.TrimSpace(found.Content) != "" &&
		!strings.Contains(found.Content, "no output") {
		t.Errorf("the fork's directory holds a copy of the program: %q", found.Content)
	}
	// The origin going does not take the fork's packages with it.
	if w := callAPI(t, a, "DELETE", "/sessions/"+origin.ID, nil); w.Code >= 300 {
		t.Fatalf("delete = %d %s", w.Code, w.Body.String())
	}
	a.pkgs.idle()
	if out := shell(t, a, fork.ID, "hello-nix"); !out.OK {
		t.Errorf("after the origin was deleted, the fork cannot run it: %+v", out)
	}
}

// "Deleting a session deletes its directory, and removes from the store the
// packages no remaining session installed."
func TestDeletingAConversationRemovesWhatOnlyItInstalled(t *testing.T) {
	shared := fakePkg{attr: "shared-nix", version: "1.0", description: "Installed twice"}
	r := newFakeRelease(t, hello, shared)
	a := packagesApp(t, r)
	leaving, staying := newSession(t, a), newSession(t, a)
	if res := installPkg(t, a, leaving.ID, "hello-nix", "shared-nix"); !res.OK {
		t.Fatalf("install = %+v", res)
	}
	if res := installPkg(t, a, staying.ID, "shared-nix"); !res.OK {
		t.Fatalf("install = %+v", res)
	}
	if w := callAPI(t, a, "DELETE", "/sessions/"+leaving.ID, nil); w.Code >= 300 {
		t.Fatalf("delete = %d %s", w.Code, w.Body.String())
	}
	within(t, time.Minute, "the deleted conversation's own package leaves the store", func() bool {
		return !exists(r.paths["hello-nix"])
	})
	a.pkgs.idle()
	if !exists(r.paths["shared-nix"]) {
		t.Error("a package another conversation installed was removed")
	}
	if out := shell(t, a, staying.ID, "shared-nix"); !out.OK {
		t.Errorf("the remaining conversation cannot run its package: %+v", out)
	}
}

// "An import installs the exact packages its archive names, and the answer says
// which it could not fetch."
func TestAnImportFetchesWhatItsArchiveNames(t *testing.T) {
	r := newFakeRelease(t, hello)
	a := packagesApp(t, r)
	s := newSession(t, a)
	if res := installPkg(t, a, s.ID, "hello-nix"); !res.OK {
		t.Fatalf("install = %+v", res)
	}
	blob := exportSessionZip(t, a, s.ID)
	if w := callAPI(t, a, "DELETE", "/sessions/"+s.ID, nil); w.Code >= 300 {
		t.Fatalf("delete = %d %s", w.Code, w.Body.String())
	}
	within(t, time.Minute, "the package leaves the store with its only conversation", func() bool {
		return !exists(r.paths["hello-nix"])
	})

	w := importSessionZip(t, a, blob)
	if w.Code != 201 {
		t.Fatalf("import = %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "packages_missing") {
		t.Errorf("import says a package is missing: %s", w.Body.String())
	}
	if out := shell(t, a, s.ID, "hello-nix"); !out.OK || !strings.Contains(out.Content, "hello-nix 1.0") {
		t.Errorf("running it after the import = %+v", out)
	}

	// An archive naming a path the cache does not hold imports, and says so.
	other := newSession(t, a)
	lost := "/nix/store/" + strings.Repeat("0", 32) + "-lost-1.0"
	if _, err := a.store.UpdateSession(other.ID, func(x *Session) {
		x.Packages = []Package{{Name: "lost", Version: "1.0", Paths: []string{lost}}}
	}); err != nil {
		t.Fatal(err)
	}
	blob = exportSessionZip(t, a, other.ID)
	if w := callAPI(t, a, "DELETE", "/sessions/"+other.ID, nil); w.Code >= 300 {
		t.Fatalf("delete = %d", w.Code)
	}
	w = importSessionZip(t, a, blob)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"packages_missing":["`+lost+`"]`) {
		t.Errorf("import of an archive naming a lost package = %d %s", w.Code, w.Body.String())
	}
}

// "A conversation's packages are still on its PATH after the agent restarts,
// fetched again if the store lost them."
func TestPackagesSurviveARestartThatLostTheStore(t *testing.T) {
	r := newFakeRelease(t, hello)
	a := packagesApp(t, r)
	s := newSession(t, a)
	if res := installPkg(t, a, s.ID, "hello-nix"); !res.OK {
		t.Fatalf("install = %+v", res)
	}
	// A store that did not survive the restart: this agent's roots go, and
	// with them what they kept.
	if err := os.RemoveAll(filepath.Join(a.cfg.DataDir, "packages")); err != nil {
		t.Fatal(err)
	}
	a.pkgs.collect()
	if exists(r.paths["hello-nix"]) {
		t.Fatal("the package outlived its roots; this test cannot show a refetch")
	}

	a.pkgs.restore(a)
	if out := shell(t, a, s.ID, "hello-nix"); !out.OK || !strings.Contains(out.Content, "hello-nix 1.0") {
		t.Errorf("after the restart = %+v", out)
	}
}

func getSessionJSON(t *testing.T, a *App, id string) Session {
	t.Helper()
	w := callAPI(t, a, "GET", "/sessions/"+id, nil)
	var body struct {
		Session Session `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET /sessions/%s: %v %s", id, err, w.Body.String())
	}
	return body.Session
}
