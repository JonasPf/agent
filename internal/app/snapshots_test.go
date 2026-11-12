package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The criteria of ADR-082, driven through the API with a model that runs
// commands through the real bash tool: a session's files are snapshotted before
// every turn, a revert puts them back, and a fork shares them and can start from
// an earlier point.

// shellModel plays a model that runs any message containing "$ " through the
// bash tool, the rest of the message being the command, and answers anything
// else, and every tool result, with "done".
func shellModel(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
			Tools json.RawMessage `json:"tools"`
		}
		_ = json.Unmarshal(body, &req)
		delta := map[string]any{"content": "done"}
		if len(req.Tools) == 0 || string(req.Tools) == "null" {
			delta = map[string]any{"content": "A title"}
		} else if n := len(req.Messages); n > 0 && req.Messages[n-1].Role == "user" {
			text := contentText(req.Messages[n-1].Content)
			if i := strings.Index(text, "$ "); i >= 0 {
				args, _ := json.Marshal(map[string]string{"command": text[i+2:]})
				delta = map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": fmt.Sprintf("call_%d", time.Now().UnixNano()),
					"function": map[string]any{"name": "bash", "arguments": string(args)},
				}}}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta}},
			"usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 10}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// contentText is a message's text, whether it was sent as a string or as parts.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &parts)
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

// shellApp is an agent over the shipped tools, talking to shellModel.
func shellApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{DataDir: dir, Workspace: filepath.Join(dir, "workspace"), PluginsDir: shippedBundles(t)}
	a := &App{cfg: cfg, sandbox: NewSandbox(cfg), store: st,
		tools:  NewRegistry(cfg.PluginsDir),
		skills: NewSkills("", ""), personas: NewPersonas(filepath.Join(dir, "personas")),
		hub: NewHub(), queues: map[string]chan func(){}}
	a.registerBuiltins()
	dropSnapshots(t, a)
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("tools failed to load: %v", failures)
	}
	srv := shellModel(t)
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL
	return a
}

// sessionAPI is the session as the API reports it.
func sessionAPI(t *testing.T, a *App, id string) Session {
	t.Helper()
	w := callAPI(t, a, "GET", "/sessions/"+id, nil)
	if w.Code != 200 {
		t.Fatalf("GET session: status %d (%s)", w.Code, w.Body.String())
	}
	var out struct {
		Session Session `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Session
}

// fileIn reads a file in a session's working directory; ok is false when
// there is none.
func fileIn(t *testing.T, a *App, id, name string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(a.sessionWorkspace(id), name))
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

func wantFile(t *testing.T, a *App, id, name, want string) {
	t.Helper()
	got, ok := fileIn(t, a, id, name)
	if !ok {
		t.Errorf("%s is missing, want %q", name, want)
	} else if got != want {
		t.Errorf("%s reads %q, want %q", name, got, want)
	}
}

func wantNoFile(t *testing.T, a *App, id, name string) {
	t.Helper()
	if got, ok := fileIn(t, a, id, name); ok {
		t.Errorf("%s is there (%q), want none", name, got)
	}
}

func lastEvent(t *testing.T, a *App, id string) Entry {
	t.Helper()
	es := a.store.Entries(id)
	for i := len(es) - 1; i >= 0; i-- {
		if es[i].Type == "event" {
			return es[i]
		}
	}
	t.Fatal("no event")
	return Entry{}
}

func forkAPI(t *testing.T, a *App, id string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return callAPI(t, a, "POST", "/sessions/"+id+"/fork", body)
}

func forkedSession(t *testing.T, w *httptest.ResponseRecorder) Session {
	t.Helper()
	if w.Code != 201 {
		t.Fatalf("fork: status %d (%s)", w.Code, w.Body.String())
	}
	var s Session
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func hasSeq(xs []int, x int) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// The criterion: before every turn the session's directory is snapshotted,
// with what was uploaded for the turn's message in it.
func TestEveryTurnSnapshotsTheFilesBeforeItBegins(t *testing.T) {
	a := shellApp(t)
	s := newSession(t, a)
	if !isSubvolume(a.sessionWorkspace(s.ID)) {
		t.Fatal("a session's working directory is not a subvolume")
	}
	say(t, a, s.ID, "$ printf one > notes.txt")
	first := seqOf(t, a, s.ID, "$ printf one > notes.txt")
	if w := upload(t, a, s.ID, "brief.txt", "uploaded"); w.Code >= 300 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	say(t, a, s.ID, "$ printf two > notes.txt")
	second := seqOf(t, a, s.ID, "$ printf two > notes.txt")
	wantFile(t, a, s.ID, "notes.txt", "two")

	got := sessionAPI(t, a, s.ID).Snapshots
	if !hasSeq(got, first) || !hasSeq(got, second) {
		t.Fatalf("the session reports snapshots %v, want both turns' messages %d and %d", got, first, second)
	}
	p, _ := a.snapshotPath(s.ID, first)
	if _, err := os.Stat(filepath.Join(p, "notes.txt")); !os.IsNotExist(err) {
		t.Errorf("the snapshot before the first turn holds what that turn wrote: %v", err)
	}
	p, _ = a.snapshotPath(s.ID, second)
	for name, want := range map[string]string{"notes.txt": "one", "brief.txt": "uploaded"} {
		if b, err := os.ReadFile(filepath.Join(p, name)); err != nil || string(b) != want {
			t.Errorf("the snapshot before the second turn has %s = %q (%v), want %q", name, b, err, want)
		}
	}
	err := os.WriteFile(filepath.Join(p, "notes.txt"), []byte("x"), 0o644)
	if err == nil {
		t.Error("a snapshot can be written")
	}
}

// The criterion: a turn that begins with nothing changed since the last
// snapshot takes none, so turns that write nothing do not push older files out.
func TestTurnsThatChangeNothingShareTheLastSnapshot(t *testing.T) {
	a := shellApp(t)
	s := newSession(t, a)
	say(t, a, s.ID, "$ printf kept > notes.txt")
	say(t, a, s.ID, "$ printf changed > notes.txt")
	before := seqOf(t, a, s.ID, "$ printf changed > notes.txt")
	for i := 0; i < keptSnapshots+3; i++ {
		say(t, a, s.ID, fmt.Sprintf("just talking, %d", i))
	}
	if _, ok := a.snapshotPath(s.ID, before); !ok {
		t.Fatalf("%d turns that wrote nothing pushed out the snapshot before the last one that did", keptSnapshots+3)
	}
	if w := revertTo(t, a, s.ID, before); w.Code != 200 {
		t.Fatalf("revert: %d %s", w.Code, w.Body.String())
	}
	wantFile(t, a, s.ID, "notes.txt", "kept")
}

// The criterion: a session keeps its newest twenty snapshots.
func TestASessionKeepsItsNewestTwentySnapshots(t *testing.T) {
	a := shellApp(t)
	s := newSession(t, a)
	for i := 0; i < keptSnapshots+2; i++ {
		say(t, a, s.ID, fmt.Sprintf("$ printf %d > n%d.txt", i, i))
	}
	got := sessionAPI(t, a, s.ID).Snapshots
	if len(got) != keptSnapshots {
		t.Fatalf("%d turns, each writing, left %d snapshots, want %d", keptSnapshots+2, len(got), keptSnapshots)
	}
	if hasSeq(got, seqOf(t, a, s.ID, "$ printf 0 > n0.txt")) || hasSeq(got, seqOf(t, a, s.ID, "$ printf 1 > n1.txt")) {
		t.Errorf("the oldest snapshots were kept: %v", got)
	}
	if !hasSeq(got, seqOf(t, a, s.ID, fmt.Sprintf("$ printf %d > n%d.txt", keptSnapshots+1, keptSnapshots+1))) {
		t.Errorf("the newest snapshot was not kept: %v", got)
	}
}

// The criterion: a revert puts the working directory back as it was when the
// reverted-to message's turn began, its upload included.
func TestARevertPutsTheFilesBackAsTheyWereBeforeTheTurn(t *testing.T) {
	a := shellApp(t)
	s := newSession(t, a)
	say(t, a, s.ID, "$ printf one > notes.txt")
	if w := upload(t, a, s.ID, "brief.txt", "uploaded"); w.Code >= 300 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	say(t, a, s.ID, "$ printf two > notes.txt && printf x > extra.txt && rm brief.txt")
	say(t, a, s.ID, "$ printf three > notes.txt")
	wantFile(t, a, s.ID, "notes.txt", "three")

	w := revertTo(t, a, s.ID, seqOf(t, a, s.ID, "$ printf two > notes.txt && printf x > extra.txt && rm brief.txt"))
	if w.Code != 200 {
		t.Fatalf("revert: %d %s", w.Code, w.Body.String())
	}
	wantFile(t, a, s.ID, "notes.txt", "one")
	wantFile(t, a, s.ID, "brief.txt", "uploaded")
	wantNoFile(t, a, s.ID, "extra.txt")
	if ev := lastEvent(t, a, s.ID); !strings.Contains(ev.Text, "files are back as they were before it") {
		t.Errorf("the revert event does not say the files were put back: %q", ev.Text)
	}
	if !isSubvolume(a.sessionWorkspace(s.ID)) {
		t.Error("after a revert the working directory is not a subvolume")
	}

	// And the next turn carries on from there, in a directory its tools can use.
	say(t, a, s.ID, "$ cat notes.txt > seen.txt")
	wantFile(t, a, s.ID, "seen.txt", "one")
}

// The criterion: the files a revert replaces are kept, and a fork from before
// its revert event starts from them — which is how a revert is undone.
func TestAForkFromBeforeARevertUndoesIt(t *testing.T) {
	a := shellApp(t)
	s := newSession(t, a)
	say(t, a, s.ID, "$ printf one > notes.txt")
	say(t, a, s.ID, "$ printf two > notes.txt")
	if w := revertTo(t, a, s.ID, seqOf(t, a, s.ID, "$ printf two > notes.txt")); w.Code != 200 {
		t.Fatalf("revert: %d %s", w.Code, w.Body.String())
	}
	revert := lastEvent(t, a, s.ID)
	if revert.EventKind != "revert" {
		t.Fatalf("the last event is %q, not the revert", revert.EventKind)
	}
	if !hasSeq(sessionAPI(t, a, s.ID).Snapshots, revert.Seq) {
		t.Fatalf("the files the revert replaced were not kept under its event %d", revert.Seq)
	}
	f := forkedSession(t, forkAPI(t, a, s.ID, map[string]any{"before": revert.Seq}))
	wantFile(t, a, f.ID, "notes.txt", "two")
	wantFile(t, a, s.ID, "notes.txt", "one")
	var texts []string
	for _, e := range a.store.Entries(f.ID) {
		if e.Type == "message" && e.Role == "user" {
			texts = append(texts, e.Text)
		}
	}
	if len(texts) != 2 || texts[1] != "$ printf two > notes.txt" {
		t.Errorf("the fork from before the revert does not hold the turn it took back: %q", texts)
	}
	for _, e := range a.store.Entries(f.ID) {
		if isRevert(e) {
			t.Errorf("the fork from before the revert carries the revert: %+v", e)
		}
	}
}

// The criterion: a revert to a message whose snapshot is no longer kept takes
// back the same entries, leaves the files, and says so.
func TestARevertWithoutItsSnapshotLeavesTheFilesAndSaysSo(t *testing.T) {
	a := shellApp(t)
	s := newSession(t, a)
	for i := 0; i < keptSnapshots+1; i++ {
		say(t, a, s.ID, fmt.Sprintf("$ printf %d > notes.txt", i))
	}
	oldest := seqOf(t, a, s.ID, "$ printf 0 > notes.txt")
	if _, ok := a.snapshotPath(s.ID, oldest); ok {
		t.Fatal("the snapshot before the oldest turn is still kept")
	}
	if w := revertTo(t, a, s.ID, oldest); w.Code != 200 {
		t.Fatalf("revert: %d %s", w.Code, w.Body.String())
	}
	wantFile(t, a, s.ID, "notes.txt", fmt.Sprint(keptSnapshots))
	ev := lastEvent(t, a, s.ID)
	if ev.EventKind != "revert" || ev.RevertsFrom != oldest {
		t.Fatalf("the conversation was not reverted: %+v", ev)
	}
	if !strings.Contains(ev.Text, "no longer kept") {
		t.Errorf("the revert event does not say the files were not put back: %q", ev.Text)
	}
}

// The criteria: a fork can start before a message that started a turn; it
// copies the entries before it and none after, and starts from the files as
// they were there. The original goes on as it was.
func TestAForkFromAnEarlierMessage(t *testing.T) {
	a := shellApp(t)
	s := newSession(t, a)
	say(t, a, s.ID, "$ printf one > notes.txt")
	say(t, a, s.ID, "$ printf two > notes.txt && printf x > extra.txt")
	say(t, a, s.ID, "$ printf three > notes.txt")
	point := seqOf(t, a, s.ID, "$ printf two > notes.txt && printf x > extra.txt")
	origin := len(a.store.Entries(s.ID))

	f := forkedSession(t, forkAPI(t, a, s.ID, map[string]any{"before": point}))
	wantFile(t, a, f.ID, "notes.txt", "one")
	wantNoFile(t, a, f.ID, "extra.txt")
	wantFile(t, a, s.ID, "notes.txt", "three")
	if n := len(a.store.Entries(s.ID)); n != origin+1 {
		t.Errorf("the origin gained %d entries, want only its fork event", n-origin)
	}
	var texts []string
	for _, e := range a.store.Entries(f.ID) {
		if e.Type == "message" && e.Role == "user" {
			texts = append(texts, e.Text)
		}
	}
	if len(texts) != 1 || texts[0] != "$ printf one > notes.txt" {
		t.Errorf("the fork holds the user messages %q, want only the one before the point", texts)
	}
	if !isSubvolume(a.sessionWorkspace(f.ID)) {
		t.Error("the fork's working directory is not a subvolume")
	}
	say(t, a, f.ID, "$ printf fork > notes.txt")
	wantFile(t, a, f.ID, "notes.txt", "fork")
	wantFile(t, a, s.ID, "notes.txt", "three")
}

// The criterion: a fork from a point whose snapshot is no longer kept, or from
// an entry that started no turn, is refused, says why, and creates nothing.
func TestAForkFromAPointWithoutFilesIsRefused(t *testing.T) {
	a := shellApp(t)
	s := newSession(t, a)
	for i := 0; i < keptSnapshots+1; i++ {
		say(t, a, s.ID, fmt.Sprintf("$ printf %d > notes.txt", i))
	}
	var answer int
	for _, e := range a.store.Entries(s.ID) {
		if e.Type == "message" && e.Role == "assistant" {
			answer = e.Seq
		}
	}
	sessions := len(a.store.Sessions())
	for name, before := range map[string]int{
		"a message whose snapshot is gone": seqOf(t, a, s.ID, "$ printf 0 > notes.txt"),
		"an answer":                        answer,
		"an entry that does not exist":     99999,
	} {
		w := forkAPI(t, a, s.ID, map[string]any{"before": before})
		if w.Code != 400 {
			t.Errorf("a fork from %s: status %d (%s), want 400", name, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "before") {
			t.Errorf("a fork from %s is refused without saying why: %s", name, w.Body.String())
		}
	}
	if n := len(a.store.Sessions()); n != sessions {
		t.Errorf("refused forks created %d sessions", n-sessions)
	}
}

// The criterion: a revert in a fork to before a message it copied puts back
// the files from before that message.
func TestAForkCanRevertIntoTheHistoryItCopied(t *testing.T) {
	a := shellApp(t)
	s := newSession(t, a)
	say(t, a, s.ID, "$ printf one > notes.txt")
	say(t, a, s.ID, "$ printf two > notes.txt")
	f := forkedSession(t, forkAPI(t, a, s.ID, map[string]any{}))
	wantFile(t, a, f.ID, "notes.txt", "two")
	if w := revertTo(t, a, f.ID, seqOf(t, a, f.ID, "$ printf two > notes.txt")); w.Code != 200 {
		t.Fatalf("revert in the fork: %d %s", w.Code, w.Body.String())
	}
	wantFile(t, a, f.ID, "notes.txt", "one")
	wantFile(t, a, s.ID, "notes.txt", "two")
	// And it is the fork's own copy: deleting the origin leaves it.
	if err := a.DeleteSession(s.ID); err != nil {
		t.Fatal(err)
	}
	if w := revertTo(t, a, f.ID, seqOf(t, a, f.ID, "$ printf one > notes.txt")); w.Code != 200 {
		t.Fatalf("second revert in the fork: %d %s", w.Code, w.Body.String())
	}
	wantNoFile(t, a, f.ID, "notes.txt")
}

// The criterion: a fork takes no space for the file contents it copies until
// either side changes them.
func TestAForkTakesNoSpaceForTheFilesItCopies(t *testing.T) {
	a := shellApp(t)
	s := newSession(t, a)
	const size = 64 << 20
	big := make([]byte, size)
	for i := range big {
		big[i] = byte(i*7 + i>>9)
	}
	if err := os.WriteFile(filepath.Join(a.ensureWorkspace(s.ID), "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	free := func() int64 {
		t.Helper()
		ws := a.sessionWorkspace(s.ID)
		if _, err := subvolumeGeneration(ws); err != nil { // writes everything out
			t.Fatal(err)
		}
		var st syscall.Statfs_t
		if err := syscall.Statfs(ws, &st); err != nil {
			t.Fatal(err)
		}
		return int64(st.Bavail) * st.Bsize
	}
	before := free()
	f := forkedSession(t, forkAPI(t, a, s.ID, map[string]any{}))
	if used := before - free(); used > size/8 {
		t.Errorf("a fork of a %d MB file used %d MB", size>>20, used>>20)
	}
	if b, err := os.ReadFile(filepath.Join(a.sessionWorkspace(f.ID), "big.bin")); err != nil || len(b) != size {
		t.Fatalf("the fork does not have the file: %d bytes, %v", len(b), err)
	}
}

// The criterion: deleting a session deletes its snapshots.
func TestDeletingASessionDeletesItsSnapshots(t *testing.T) {
	a := shellApp(t)
	s := newSession(t, a)
	say(t, a, s.ID, "$ printf one > notes.txt")
	say(t, a, s.ID, "$ printf two > notes.txt")
	if _, err := os.Stat(a.snapshotsOf(s.ID)); err != nil {
		t.Fatalf("no snapshots to delete: %v", err)
	}
	if w := callAPI(t, a, "DELETE", "/sessions/"+s.ID, nil); w.Code >= 300 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Lstat(a.snapshotsOf(s.ID)); !os.IsNotExist(err) {
		t.Errorf("the deleted session's snapshots are still there: %v", err)
	}
	if _, err := os.Lstat(a.sessionWorkspace(s.ID)); !os.IsNotExist(err) {
		t.Errorf("the deleted session's directory is still there: %v", err)
	}
}

// A session's directory from before ADR-082 is a plain directory. The agent
// makes it a subvolume when it starts, keeping what a tool installed in it
// running: modes, links as links, and a link into the directory pointing where
// it pointed.
func TestAPlainWorkingDirectoryBecomesASubvolume(t *testing.T) {
	a := shellApp(t)
	s := newSession(t, a)
	ws := a.sessionWorkspace(s.ID)
	if err := deleteSubvolume(ws); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(ws, "bin", "tool"), "#!/bin/sh\necho ran\n")
	if err := os.Chmod(filepath.Join(ws, "bin", "tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(ws, "bin", "tool"), filepath.Join(ws, "abs")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("bin/tool", filepath.Join(ws, "rel")); err != nil {
		t.Fatal(err)
	}
	if isSubvolume(ws) {
		t.Fatal("the setup left a subvolume")
	}

	if err := a.convertWorkspaces(); err != nil {
		t.Fatal(err)
	}
	if !isSubvolume(ws) {
		t.Fatal("the plain directory was not made a subvolume")
	}
	if fi, err := os.Stat(filepath.Join(ws, "bin", "tool")); err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("the tool lost its mode: %v %v", fi, err)
	}
	for name, want := range map[string]string{"abs": filepath.Join(ws, "bin", "tool"), "rel": "bin/tool"} {
		if got, err := os.Readlink(filepath.Join(ws, name)); err != nil || got != want {
			t.Errorf("link %s points at %q (%v), want %q", name, got, err, want)
		}
	}
	say(t, a, s.ID, "$ ./abs > out.txt")
	wantFile(t, a, s.ID, "out.txt", "ran\n")
	if entries, _ := os.ReadDir(a.cfg.Workspace); len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the conversion left %v in the workspace", names)
	}
}

// The criterion: the agent refuses to start when its state is not on btrfs,
// and says why.
func TestTheAgentRefusesToStartOffBtrfs(t *testing.T) {
	shm, err := os.MkdirTemp("/dev/shm", "agent-state-")
	if err != nil {
		t.Fatalf("this test needs a tmpfs at /dev/shm: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(shm) })
	t.Setenv("AGENT_STATE", shm)
	t.Setenv("AGENT_HOME", t.TempDir())
	t.Setenv("AGENT_ADDR", "127.0.0.1:0")
	err = Run()
	if err == nil {
		t.Fatal("the agent started on tmpfs")
	}
	if !strings.Contains(err.Error(), "not on btrfs") || !strings.Contains(err.Error(), "ADR-082") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// The criterion: an export carries the working directory as it is and none of
// its snapshots, and an imported conversation has none to put back.
func TestAnExportCarriesNoSnapshots(t *testing.T) {
	a := shellApp(t)
	s := newSession(t, a)
	say(t, a, s.ID, "$ printf one > notes.txt")
	say(t, a, s.ID, "$ printf two > notes.txt")
	blob := exportSessionZip(t, a, s.ID)
	names := zipNames(t, blob)
	if got := names["files/notes.txt"]; got != "two" {
		t.Errorf("the export's notes.txt is %q, want the directory as it is", got)
	}
	for name := range names {
		if strings.HasPrefix(name, "files/") && name != "files/notes.txt" && !strings.HasSuffix(name, "/") {
			t.Errorf("the export carries %s beside the working directory's one file", name)
		}
	}

	b := shellApp(t)
	if w := importSessionZip(t, b, blob); w.Code >= 300 {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	if !isSubvolume(b.sessionWorkspace(s.ID)) {
		t.Error("an imported conversation's directory is not a subvolume")
	}
	if got := sessionAPI(t, b, s.ID).Snapshots; len(got) != 0 {
		t.Errorf("an imported conversation reports snapshots %v", got)
	}
	if w := revertTo(t, b, s.ID, seqOf(t, b, s.ID, "$ printf two > notes.txt")); w.Code != 200 {
		t.Fatalf("revert after import: %d %s", w.Code, w.Body.String())
	}
	wantFile(t, b, s.ID, "notes.txt", "two")
}
