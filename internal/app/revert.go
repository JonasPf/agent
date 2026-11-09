package app

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// errCannotRevert is a revert to somewhere that is not the start of a turn still
// in the conversation.
var errCannotRevert = errors.New("cannot revert there")

// errRevertWhileWorking is a revert asked for while a turn is running or
// queued, which would go on writing after it.
var errRevertWhileWorking = errors.New("the conversation is working: stop the turn, or let it finish, then revert")

// Revert takes a conversation back to before one of its user messages. The
// transcript is append-only, so nothing is removed: one event is appended,
// naming the message, and the projection stops sending everything from it up
// to the event. The working directory goes back to the snapshot taken before
// that message's turn began, if one is kept, and what it held is kept in turn;
// the jobs stay as the reverted turns left them.
//
// The cut is only ever where a turn starts. Anywhere else could leave a tool
// call without its result, which a provider refuses.
func (a *App) Revert(sessionID string, seq int) (Entry, error) {
	var out Entry
	var err error
	ran := a.whenIdle(sessionID, func() {
		entries := a.store.Entries(sessionID)
		gone := takenBack(entries)
		i := entryAt(entries, seq)
		if i < 0 {
			err = fmt.Errorf("%w: there is no entry %d", errCannotRevert, seq)
			return
		}
		at := entries[i]
		if at.Type != "message" || at.Role != "user" {
			err = fmt.Errorf("%w: entry %d is not a user message, so no turn starts there", errCannotRevert, seq)
			return
		}
		if gone[seq] {
			err = fmt.Errorf("%w: entry %d was already taken back by an earlier revert", errCannotRevert, seq)
			return
		}
		// Counted as the warning before it counts them: the message, and what
		// came after it.
		n := 0
		for _, e := range entries {
			if e.Seq > seq && !gone[e.Seq] {
				n++
			}
		}
		who := "your message"
		if at.JobID != "" {
			who = "the wake of job " + at.JobID
		}
		said, _, _ := strings.Cut(strings.TrimSpace(at.Text), "\n")
		what := "it alone"
		switch {
		case n == 1:
			what = "it and the entry after it"
		case n > 1:
			what = fmt.Sprintf("it and the %d entries after it", n)
		}
		// Its files go back too, from the snapshot taken before that turn
		// began. What they are now is kept, under the revert's own event, so
		// a fork from before the event brings them back (ADR-082).
		files := "Files are as those turns left them: the snapshot from before it is no longer kept."
		keep := ""
		if from, ok := a.snapshotPath(sessionID, seq); ok {
			keep = filepath.Join(a.snapshotsOf(sessionID), fmt.Sprintf(".revert-%d", time.Now().UnixNano()))
			if ferr := a.replaceWorkspace(sessionID, from, keep); ferr != nil {
				keep = ""
				files = fmt.Sprintf("Files are as those turns left them: putting them back failed (%v).", ferr)
			} else {
				files = "Its files are back as they were before it."
			}
		}
		out = a.appendEvent(sessionID, Entry{EventKind: "revert", RevertsFrom: seq, Text: fmt.Sprintf(
			"reverted to before %s “%s” — %s taken back. %s Jobs are as those turns left them.",
			who, truncate(said, 60), what, files)})
		if keep != "" {
			if rerr := os.Rename(keep, filepath.Join(a.snapshotsOf(sessionID), strconv.Itoa(out.Seq))); rerr != nil {
				log.Printf("revert: keeping the replaced files of %s: %v", sessionID, rerr)
			}
			a.pruneSnapshots(sessionID)
		}
	})
	if !ran {
		return Entry{}, errRevertWhileWorking
	}
	if err != nil {
		return Entry{}, err
	}
	// What the status line says is cached no longer ends where the next call
	// will, so it is not claimed, as after a compaction; the next answer says
	// what it actually read.
	_, _ = a.store.UpdateSession(sessionID, func(x *Session) { x.Cache = nil })
	a.hub.Broadcast(wsEvent{Kind: "sessions"})
	return out, nil
}
