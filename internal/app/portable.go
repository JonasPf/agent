package app

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// A session is portable: everything it owns goes into one zip and comes back out
// of it. The archive mirrors what is on disk — meta.json, transcript.jsonl, the
// jobs and their run logs that belong to the session, and the working directory
// under files/ — so it can be read without this program.
//
// Memory is deliberately not in it. Memory is durable across every conversation
// rather than owned by one, and importing an archive would otherwise change what
// every later session is told.

const (
	exportMeta       = "meta.json"
	exportTranscript = "transcript.jsonl"
	exportJobs       = "jobs.json"
	exportJobRuns    = "job_runs.json"
	exportFiles      = "files/"
)

// ExportSession writes a session's archive.
func (a *App) ExportSession(w io.Writer, id string) error {
	s := a.store.Session(id)
	if s == nil {
		return fmt.Errorf("no session %s", id)
	}
	zw := zip.NewWriter(w)

	put := func(name string, body []byte) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = f.Write(body)
		return err
	}

	meta, _ := json.MarshalIndent(s, "", "  ")
	if err := put(exportMeta, meta); err != nil {
		return err
	}

	var transcript strings.Builder
	for _, e := range a.store.Entries(id) {
		line, _ := json.Marshal(e)
		transcript.Write(line)
		transcript.WriteByte('\n')
	}
	if err := put(exportTranscript, []byte(transcript.String())); err != nil {
		return err
	}

	jobs, err := a.store.SessionJobs(id)
	if err != nil {
		return err
	}
	if jobs == nil {
		jobs = []*Job{}
	}
	body, _ := json.MarshalIndent(jobs, "", "  ")
	if err := put(exportJobs, body); err != nil {
		return err
	}

	// A job's log travels with the job. A restored reminder that has forgotten
	// everything it ever did is not the same reminder.
	runs := []JobRun{}
	for _, j := range jobs {
		rs, err := a.store.JobRuns(j.ID)
		if err != nil {
			return err
		}
		runs = append(runs, rs...)
	}
	body, _ = json.MarshalIndent(runs, "", "  ")
	if err := put(exportJobRuns, body); err != nil {
		return err
	}

	root := a.sessionWorkspace(id)
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		dst, err := zw.Create(exportFiles + filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		_, err = io.Copy(dst, f)
		return err
	})
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return zw.Close()
}

// ImportSession restores a session from an archive. It keeps the session's own
// identifier, so an archive read back is the conversation it was rather than a
// copy of it — and an identifier already present is a conflict, not a merge.
func (a *App) ImportSession(r io.ReaderAt, size int64) (*Session, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("not a session archive: %w", err)
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	metaFile, ok := files[exportMeta]
	if !ok {
		return nil, fmt.Errorf("not a session archive: no %s", exportMeta)
	}
	metaBody, err := readZipFile(metaFile)
	if err != nil {
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(metaBody, &sess); err != nil {
		return nil, fmt.Errorf("%s: %w", exportMeta, err)
	}
	if !validSessionID(sess.ID) {
		return nil, fmt.Errorf("%s carries no usable session id", exportMeta)
	}
	if a.store.Session(sess.ID) != nil {
		return nil, fmt.Errorf("session %s is already here; delete it first or import into another agent", sess.ID)
	}

	var entries []Entry
	if f, ok := files[exportTranscript]; ok {
		body, err := readZipFile(f)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(body), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var e Entry
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				return nil, fmt.Errorf("%s: %w", exportTranscript, err)
			}
			entries = append(entries, e)
		}
	}
	if err := a.store.ImportSession(&sess, entries); err != nil {
		return nil, err
	}

	if f, ok := files[exportJobs]; ok {
		body, err := readZipFile(f)
		if err != nil {
			return nil, err
		}
		var jobs []*Job
		if err := json.Unmarshal(body, &jobs); err != nil {
			return nil, fmt.Errorf("%s: %w", exportJobs, err)
		}
		for _, j := range jobs {
			j.SessionID = sess.ID
			if err := a.store.PutJob(j); err != nil {
				return nil, err
			}
		}
	}
	if f, ok := files[exportJobRuns]; ok {
		body, err := readZipFile(f)
		if err != nil {
			return nil, err
		}
		var runs []JobRun
		if err := json.Unmarshal(body, &runs); err != nil {
			return nil, fmt.Errorf("%s: %w", exportJobRuns, err)
		}
		for _, r := range runs {
			r.SessionID = sess.ID
			if err := a.store.PutJobRun(r); err != nil {
				return nil, err
			}
		}
	}

	a.ensureWorkspace(sess.ID)
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !strings.HasPrefix(f.Name, exportFiles) {
			continue
		}
		rel := path.Clean(strings.TrimPrefix(f.Name, exportFiles))
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		_, _, err = a.writeWorkspaceFile(sess.ID, rel, rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	a.hub.Broadcast(wsEvent{Kind: "sessions"})
	return &sess, nil
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, maxUpload))
}
