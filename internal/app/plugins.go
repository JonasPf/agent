package app

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Installing, configuring and removing bundles (ADR-087). Only the operator
// does any of it, through these routes: there is no tool for it, and the
// directory bundles are installed in is one no tool can write. An install is
// two requests, so that what a bundle is and asks for is on screen before it
// is on disk; what it is told is kept in a file of its own beside the data,
// which no tool can read and no route serves.

const (
	sourceImage     = "image"
	sourceInstalled = "installed"
)

// A bundle's archive, unpacked, and any one file in it, may be this large and
// no larger. A browser's worth of server fits; a disk's worth does not.
var (
	maxBundle     int64 = 300 << 20
	maxBundleFile int64 = 200 << 20
)

// maxSkillArchive bounds a skill's archive. A skill is text.
const maxSkillArchive = 20 << 20

// Plugin is one bundle as the Plugins screen lists it.
type Plugin struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	// Source is "image" for a bundle the agent ships with and "installed" for
	// one the operator added.
	Source string   `json:"source,omitempty"`
	Tools  []string `json:"tools"`
	// Config is what the bundle asks the operator for, and Needs the keys it
	// cannot run without and has no value for. While it needs any, its tools
	// are not offered.
	Config  []PluginSetting `json:"config"`
	Needs   []string        `json:"needs"`
	Reads   []string        `json:"reads"`
	Timeout int             `json:"timeout_seconds"`
	// Failure is why the bundle is not loaded, when it is not.
	Failure string `json:"failure,omitempty"`
	dir     string
}

// PluginSetting is one thing a bundle asks the operator for. Whether it has a
// value is said; the value never is.
type PluginSetting struct {
	Key         string `json:"key"`
	Type        string `json:"type,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
	Sensitive   bool   `json:"sensitive"`
	Set         bool   `json:"set"`
}

func (a *App) pluginValuesPath() string { return filepath.Join(a.cfg.DataDir, "plugins.json") }

// pluginValues is what the operator told each bundle, by bundle and key. Not
// in the database: every tool is handed that.
func (a *App) pluginValues() map[string]map[string]string {
	out := map[string]map[string]string{}
	if b, err := os.ReadFile(a.pluginValuesPath()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func (a *App) savePluginValues(v map[string]map[string]string) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := a.pluginValuesPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, a.pluginValuesPath())
}

func (a *App) plugin(name string) (Plugin, bool) {
	for _, p := range a.tools.Plugins() {
		if p.Name == name {
			return p, true
		}
	}
	return Plugin{}, false
}

// archiveFrom spills an archive to a file beside the data and answers where it
// is and its sha256. The archive is body, or what an https address serves
// when one is given; the caller removes the file.
func (a *App) archiveFrom(address string, body io.Reader, limit int64) (string, string, error) {
	if address != "" {
		u, err := url.Parse(address)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return "", "", fmt.Errorf("an archive is fetched from an https address, and %q is not one", address)
		}
		client := a.fetch
		if client == nil {
			client = &http.Client{Timeout: 5 * time.Minute}
		}
		resp, err := client.Get(address)
		if err != nil {
			return "", "", fmt.Errorf("fetching %s: %v", address, err)
		}
		defer resp.Body.Close()
		if resp.Request.URL.Scheme != "https" {
			return "", "", fmt.Errorf("%s led to an address that is not https", address)
		}
		if resp.StatusCode != 200 {
			return "", "", fmt.Errorf("fetching %s: the server answered %s", address, resp.Status)
		}
		body = resp.Body
	}
	tmp, err := os.CreateTemp(a.cfg.DataDir, "archive-*.zip")
	if err != nil {
		return "", "", err
	}
	defer tmp.Close()
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, sum), io.LimitReader(body, limit+1))
	if err == nil && n > limit {
		err = fmt.Errorf("archives larger than %d MB are refused", limit>>20)
	}
	if err == nil && n == 0 {
		err = fmt.Errorf("no archive was sent")
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", "", err
	}
	return tmp.Name(), hex.EncodeToString(sum.Sum(nil)), nil
}

// sentArchive is the archive a request carries: its body, or the address its
// JSON body names.
func (a *App) sentArchive(w http.ResponseWriter, r *http.Request, limit int64) (string, string, error) {
	address := ""
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var in struct {
			URL string `json:"url"`
		}
		if err := readJSON(r, &in); err != nil {
			return "", "", err
		}
		if in.URL == "" {
			return "", "", fmt.Errorf("send the archive as the body, or {\"url\": …} naming where it is")
		}
		address = in.URL
	}
	return a.archiveFrom(address, http.MaxBytesReader(w, r.Body, limit+uploadEnvelope), limit)
}

// unpack writes a zip's files under dir, which it is opened as the root of, so
// that no entry's name can reach outside it. Only folders and ordinary files
// are written; an archive holding anything else is refused whole. A file keeps
// whether it may be run and nothing else of its mode.
func unpack(archive, dir string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("not a zip archive: %v", err)
	}
	defer zr.Close()
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	var total int64
	for _, f := range zr.File {
		name := path.Clean(f.Name)
		if !filepath.IsLocal(f.Name) || strings.Contains(f.Name, `\`) {
			return fmt.Errorf("%q is not a path inside the archive", f.Name)
		}
		mode := f.Mode()
		if mode.IsDir() {
			if err := root.MkdirAll(name, 0o755); err != nil {
				return err
			}
			continue
		}
		if !mode.IsRegular() {
			return fmt.Errorf("%s is a link or a device; a bundle holds files and folders", f.Name)
		}
		if int64(f.UncompressedSize64) > maxBundleFile {
			return fmt.Errorf("%s is larger than %d MB", f.Name, maxBundleFile>>20)
		}
		if err := root.MkdirAll(path.Dir(name), 0o755); err != nil {
			return err
		}
		perm := os.FileMode(0o644)
		if mode&0o111 != 0 {
			perm = 0o755
		}
		n, err := func() (int64, error) {
			in, err := f.Open()
			if err != nil {
				return 0, err
			}
			defer in.Close()
			out, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
			if err != nil {
				return 0, err
			}
			defer out.Close()
			return io.Copy(out, io.LimitReader(in, maxBundleFile+1))
		}()
		if err != nil {
			return fmt.Errorf("%s: %v", f.Name, err)
		}
		if n > maxBundleFile {
			return fmt.Errorf("%s is larger than %d MB", f.Name, maxBundleFile>>20)
		}
		if total += n; total > maxBundle {
			return fmt.Errorf("the archive unpacks to more than %d MB", maxBundle>>20)
		}
	}
	return nil
}

// unpacked unpacks an archive beside where bundles are installed and examines
// it there, as an install would leave it. The caller removes the folder, or
// moves it into place.
func (a *App) unpacked(archive string) (string, Plugin, error) {
	if err := os.MkdirAll(a.cfg.UserPluginsDir, 0o755); err != nil {
		return "", Plugin{}, err
	}
	// A name the loader passes over, so a reload meanwhile does not offer it.
	dir, err := os.MkdirTemp(a.cfg.UserPluginsDir, ".new-")
	if err != nil {
		return "", Plugin{}, err
	}
	if err = os.Chmod(dir, 0o755); err == nil {
		err = unpack(archive, dir)
	}
	if err != nil {
		os.RemoveAll(dir)
		return "", Plugin{}, err
	}
	p, tools := a.examine(dir, a.pluginValues())
	p.Source = sourceInstalled
	if p.Failure == "" {
		a.tools.mu.RLock()
		p.Failure = a.tools.taken(p, tools, true)
		a.tools.mu.RUnlock()
	}
	return dir, p, nil
}

func (a *App) hPlugins(w http.ResponseWriter, r *http.Request) {
	plugins := a.tools.Plugins()
	if plugins == nil {
		plugins = []Plugin{}
	}
	writeJSON(w, 200, map[string]any{"plugins": plugins})
}

// hInspectPlugin says what an archive is and installs nothing.
func (a *App) hInspectPlugin(w http.ResponseWriter, r *http.Request) {
	archive, sum, err := a.sentArchive(w, r, maxBundle)
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	defer os.Remove(archive)
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	dir, p, err := a.unpacked(archive)
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	os.RemoveAll(dir)
	writeJSON(w, 200, struct {
		Plugin
		SHA256 string `json:"sha256"`
	}{p, sum})
}

// hInstallPlugin installs the archive an inspect described: the request names
// that archive's sha256, and one that is not it is refused.
func (a *App) hInstallPlugin(w http.ResponseWriter, r *http.Request) {
	want := r.URL.Query().Get("sha256")
	if want == "" {
		fail(w, 400, "name the archive's sha256, as POST /plugins/inspect answered it")
		return
	}
	archive, sum, err := a.sentArchive(w, r, maxBundle)
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	defer os.Remove(archive)
	if !strings.EqualFold(sum, want) {
		fail(w, 409, "this is not the archive that was inspected: its sha256 is %s", sum)
		return
	}
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	dir, p, err := a.unpacked(archive)
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	defer os.RemoveAll(dir)
	if p.Failure != "" {
		fail(w, 409, "%s %s is not installed: %s", p.Name, p.Version, p.Failure)
		return
	}
	// Swapped in whole, so an install that fails leaves the version that was
	// there.
	target := filepath.Join(a.cfg.UserPluginsDir, p.Name)
	old := dir + ".old"
	if err := os.Rename(target, old); err != nil && !os.IsNotExist(err) {
		fail(w, 500, "%v", err)
		return
	}
	if err := os.Rename(dir, target); err != nil {
		os.Rename(old, target)
		fail(w, 500, "%v", err)
		return
	}
	os.RemoveAll(old)
	a.ReloadTools("")
	installed, _ := a.plugin(p.Name)
	writeJSON(w, 201, installed)
}

// hConfigurePlugin sets or clears what a bundle asked the operator for. It
// answers the bundle as it now stands, which says only whether each is set.
func (a *App) hConfigurePlugin(w http.ResponseWriter, r *http.Request) {
	var in map[string]*string
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "send {key: value}, each value a string, or null to clear it: %v", err)
		return
	}
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	p, ok := a.plugin(r.PathValue("name"))
	if !ok {
		fail(w, 404, "no such plugin")
		return
	}
	asked := map[string]bool{}
	for _, c := range p.Config {
		asked[c.Key] = true
	}
	values := a.pluginValues()
	mine := values[p.Name]
	if mine == nil {
		mine = map[string]string{}
	}
	for key, v := range in {
		if !asked[key] {
			fail(w, 400, "%s does not ask for %q", p.Name, key)
			return
		}
		if v == nil {
			delete(mine, key)
		} else {
			mine[key] = *v
		}
	}
	values[p.Name] = mine
	if err := a.savePluginValues(values); err != nil {
		fail(w, 500, "%v", err)
		return
	}
	a.ReloadTools("")
	p, _ = a.plugin(p.Name)
	writeJSON(w, 200, p)
}

// hDeletePlugin removes a bundle the operator installed, and what it was told.
func (a *App) hDeletePlugin(w http.ResponseWriter, r *http.Request) {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	p, ok := a.plugin(r.PathValue("name"))
	if !ok {
		fail(w, 404, "no such plugin")
		return
	}
	if p.Source != sourceInstalled {
		fail(w, 409, "%s ships with the agent and is removed with it", p.Name)
		return
	}
	if err := os.RemoveAll(p.dir); err != nil {
		fail(w, 500, "%v", err)
		return
	}
	values := a.pluginValues()
	delete(values, p.Name)
	if err := a.savePluginValues(values); err != nil {
		fail(w, 500, "%v", err)
		return
	}
	a.ReloadTools("")
	w.WriteHeader(204)
}

// archivedSkill reads a skill from a zip of its folder: SKILL.md and the files
// beside it, at the top of the archive or inside the one folder it holds.
func archivedSkill(archive string) (*Skill, []SkillFile, error) {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return nil, nil, fmt.Errorf("not a zip archive: %v", err)
	}
	defer zr.Close()
	// `zip -r name.zip name/` puts the folder in the archive; zipping its
	// contents does not. Either is one skill's folder.
	prefix := ""
	for _, f := range zr.File {
		if dir, file := path.Split(f.Name); file == skillMain && strings.Count(dir, "/") <= 1 && (prefix == "" || dir == "") {
			prefix = dir
			if dir == "" {
				break
			}
		}
	}
	var main []byte
	var files []SkillFile
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if !filepath.IsLocal(f.Name) || !strings.HasPrefix(f.Name, prefix) {
			return nil, nil, fmt.Errorf("%q is not inside the skill's folder", f.Name)
		}
		if !f.Mode().IsRegular() {
			return nil, nil, fmt.Errorf("%s is a link or a device; a skill holds text files", f.Name)
		}
		if f.UncompressedSize64 > maxSkillArchive {
			return nil, nil, fmt.Errorf("%s is too large for a skill", f.Name)
		}
		body, err := readZipFile(f)
		if err != nil {
			return nil, nil, err
		}
		name := strings.TrimPrefix(f.Name, prefix)
		if name == skillMain {
			main = body
			continue
		}
		files = append(files, SkillFile{Path: name, Body: string(body), Bytes: len(body)})
	}
	if main == nil {
		return nil, nil, fmt.Errorf("the archive holds no %s at its top or in its one folder", skillMain)
	}
	sk, err := skillOf(main, files)
	return sk, files, err
}
