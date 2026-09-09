package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	checkTimeout      = 60 * time.Second
	breakerWindow     = 90 * time.Second
	breakerProbeEvery = 5 * time.Minute
	// maxJobFailures caps the backoff between attempts at 2^n minutes rather than
	// ending the job: nothing deletes a job but the operator.
	maxJobFailures = 3
)

type Scheduler struct {
	app *App

	mu        sync.Mutex
	open      bool // circuit breaker open
	reason    string
	openedAt  time.Time
	lastProbe time.Time
	failures  map[string]time.Time // job id -> most recent failure
	inflight  map[string]bool
}

func NewScheduler(a *App) *Scheduler {
	return &Scheduler{app: a, failures: map[string]time.Time{}, inflight: map[string]bool{}}
}

type BreakerState struct {
	Open     bool      `json:"open"`
	Reason   string    `json:"reason"`
	OpenedAt time.Time `json:"opened_at"`
}

func (s *Scheduler) State() BreakerState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return BreakerState{Open: s.open, Reason: s.reason, OpenedAt: s.openedAt}
}

// Run ticks once per second and fires due jobs into their sessions.
func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	jobs, err := s.app.store.Jobs()
	if err != nil {
		return
	}
	now := time.Now()
	state := s.State()
	probing := false
	if state.Open && time.Since(s.lastProbeAt()) > breakerProbeEvery {
		probing = true
		s.setLastProbe(now)
	}

	for _, j := range jobs {
		if s.isInflight(j.ID) {
			continue
		}
		if j.Status == jobDone {
			continue
		}
		if j.NextRunAt.After(now) {
			continue
		}
		if state.Open && !probing {
			continue
		}
		// A job whose session is mid-turn defers to the next tick.
		sess := s.app.LiveSession(j.SessionID)
		if sess == nil {
			s.jobFailed(j, fmt.Errorf("the session this job belongs to no longer exists"))
			continue
		}
		if s.app.Busy(sess.ID) {
			continue
		}
		if probing {
			probing = false
		}
		s.setInflight(j.ID, true)
		job := j
		s.app.enqueue(sess.ID, func() {
			defer s.setInflight(job.ID, false)
			s.runJob(ctx, job, sess)
		})
		if state.Open {
			break // one probe per window
		}
	}
}

func (s *Scheduler) runJob(ctx context.Context, j *Job, sess *Session) {
	a := s.app
	due := j.NextRunAt
	j.RunCount++

	// One path. A check, if there is one, decides whether this wake acts.
	if j.Check != "" {
		met, out, err := s.runCheck(ctx, j, sess)
		if err != nil {
			s.jobFailed(j, err)
			return
		}
		if !met {
			j.LastStatus = "not_fired"
			s.logRun(j, sess.ID, due, jobSkipped, "check said no"+summarise(out))
			s.reschedule(j, false)
			return
		}
	}

	// The wake it was due at, not the moment it got to run.
	before := len(a.store.Entries(sess.ID))
	if err := a.runTurn(ctx, sess, turnOpts{UserText: j.Prompt, JobID: j.ID, DueAt: due}); err != nil {
		s.jobFailed(j, err)
		return
	}
	j.LastStatus = "fired"
	s.logRun(j, sess.ID, due, jobFired, saidBy(a.store.Entries(sess.ID)[before:]))
	s.succeeded(j)
	s.reschedule(j, true)
}

// runCheck runs the job's check command. It reports whether the condition is
// met. A non-zero exit is an answer, not a failure; a check that cannot run or
// will not finish is a failure, so that a check which never decides anything is
// surfaced rather than looping quietly until the job expires.
func (s *Scheduler) runCheck(ctx context.Context, j *Job, sess *Session) (bool, string, error) {
	cctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	// A check is a shell command in the session's directory, and is confined the
	// same way a tool is: it is a shell, so nothing else would confine it.
	ws := s.app.ensureWorkspace(sess.ID)
	// A check is a shell command the operator wrote, and it gets the runtime and
	// this session's directory: the same boundary as a tool that asked for
	// nothing beyond it.
	argv := s.app.sandbox.Wrap("/bin/sh", ws, "", nil, []string{"-c", j.Check})
	cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
	cmd.Dir = ws
	tmp := s.app.sandbox.TempDir(ws)
	_ = os.MkdirAll(tmp, 0o755)
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp)
	// Killing the shell does not close pipes a grandchild still holds, and
	// CombinedOutput waits for every writer. WaitDelay bounds that wait, so the
	// timeout above bounds the whole call.
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()

	if cctx.Err() != nil {
		return false, "", fmt.Errorf("check did not finish within %s: %s", checkTimeout, truncate(j.Check, 120))
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return false, "", fmt.Errorf("check could not run: %w", err)
	}

	met := err == nil
	status, verdict := "not_fired", "not met"
	if met {
		status, verdict = "fired", "met"
	}
	s.app.appendEvent(sess.ID, Entry{EventKind: "job_check", JobID: j.ID, Status: status,
		Text: fmt.Sprintf("check: %s → %s%s", truncate(j.Check, 120), verdict, summarise(string(out)))})
	return met, string(out), nil
}

func summarise(out string) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	return " · " + truncate(strings.ReplaceAll(out, "\n", " "), 160)
}

// reschedule decides whether a job wakes again. A job that acted and is set to
// stop is done. Otherwise the schedule is asked for the next wake, and a
// schedule with none left — a single instant, now passed — ends the job too.
func (s *Scheduler) reschedule(j *Job, acted bool) {
	a := s.app
	if acted && j.AfterActing == afterStop {
		s.finish(j)
		return
	}
	next, err := nextRun(j.Schedule, time.Now())
	if err != nil {
		// The schedule has no further wake. The job is over, and stays as the
		// record of what it did.
		s.finish(j)
		return
	}
	j.NextRunAt = next
	_ = a.store.PutJob(j)
	a.hub.Broadcast(wsEvent{Kind: "jobs"})
}

// finish marks a job as having nothing left to do. It is kept rather than
// deleted: its log is the only record of what it did, and deleting the row
// would take the answer with it.
func (s *Scheduler) finish(j *Job) {
	j.Status = jobDone
	_ = s.app.store.PutJob(j)
	s.app.hub.Broadcast(wsEvent{Kind: "jobs"})
}

// logRun writes one line of the job's own history.
func (s *Scheduler) logRun(j *Job, sessionID string, due time.Time, outcome, message string) {
	_ = s.app.store.PutJobRun(JobRun{ID: newID(), JobID: j.ID, SessionID: sessionID,
		At: time.Now(), DueAt: due, Outcome: outcome, Message: truncate(message, 1000)})
	s.app.hub.Broadcast(wsEvent{Kind: "jobs"})
}

// saidBy is what the agent said during a run, which is what the operator wants
// to see beside the time it ran. The transcript keeps the whole exchange.
func saidBy(produced []Entry) string {
	var said []string
	for _, e := range produced {
		if e.Type == "message" && e.Role == "assistant" && e.Text != "" {
			said = append(said, e.Text)
		}
	}
	if len(said) == 0 {
		return "(the agent said nothing)"
	}
	return strings.Join(said, "\n")
}

func (s *Scheduler) succeeded(j *Job) {
	j.FailCount = 0
	s.mu.Lock()
	delete(s.failures, j.ID)
	wasOpen := s.open
	if wasOpen {
		s.open, s.reason = false, ""
	}
	s.mu.Unlock()
	if wasOpen {
		s.app.hub.Broadcast(wsEvent{Kind: "status"})
	}
}

func (s *Scheduler) jobFailed(j *Job, err error) {
	a := s.app
	var perm *PermanentError
	permanent := errors.As(err, &perm)

	s.mu.Lock()
	now := time.Now()
	s.failures[j.ID] = now
	distinct := 0
	for _, at := range s.failures {
		if now.Sub(at) < breakerWindow {
			distinct++
		}
	}
	trip := !s.open && (permanent || distinct >= 3)
	if trip {
		s.open, s.reason, s.openedAt = true, err.Error(), now
		s.lastProbe = now
	}
	open := s.open
	s.mu.Unlock()

	if trip {
		a.hub.Broadcast(wsEvent{Kind: "status"})
	}
	a.appendEvent(j.SessionID, Entry{EventKind: "job_error", JobID: j.ID,
		Text: "job run failed: " + truncate(err.Error(), 300)})

	s.logRun(j, j.SessionID, j.NextRunAt, jobFailed, truncate(err.Error(), 1000))

	if open {
		// A job failing while the breaker is open waits for the probe rather than
		// counting the outage against itself.
		j.NextRunAt = time.Now().Add(breakerProbeEvery)
		_ = a.store.PutJob(j)
		return
	}
	// A failing job is kept and keeps trying, its failures piling up in its own
	// log. Deleting it would delete the record explaining why it stopped working,
	// which is the one thing the operator needs in order to decide.
	j.FailCount++
	if j.FailCount > maxJobFailures {
		j.FailCount = maxJobFailures
	}
	backoff := time.Duration(1<<j.FailCount) * time.Minute
	j.NextRunAt = time.Now().Add(backoff)
	_ = a.store.PutJob(j)
}

func (s *Scheduler) isInflight(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inflight[id]
}

func (s *Scheduler) setInflight(id string, v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v {
		s.inflight[id] = true
	} else {
		delete(s.inflight, id)
	}
}

func (s *Scheduler) lastProbeAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastProbe
}

func (s *Scheduler) setLastProbe(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastProbe = t
}

// ---- job creation ----

type JobSpec struct {
	SessionID   string `json:"session_id"`
	Schedule    string `json:"schedule"`
	Check       string `json:"check"`
	Prompt      string `json:"prompt"`
	AfterActing string `json:"after_acting"` // stop | continue; empty means continue
}

func (a *App) CreateJob(spec JobSpec) (*Job, error) {
	if spec.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	sess := a.LiveSession(spec.SessionID)
	if sess == nil {
		return nil, fmt.Errorf("no session %s", spec.SessionID)
	}
	if strings.TrimSpace(spec.Prompt) == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	after := spec.AfterActing
	if after == "" {
		after = afterContinue
	}
	if after != afterStop && after != afterContinue {
		return nil, fmt.Errorf("after_acting must be %q or %q, got %q", afterStop, afterContinue, after)
	}
	next, err := nextRun(spec.Schedule, time.Now())
	if err != nil {
		return nil, err
	}
	j := &Job{ID: newID(), SessionID: sess.ID, Schedule: spec.Schedule, Check: spec.Check,
		Prompt: spec.Prompt, AfterActing: after, Status: jobScheduled,
		NextRunAt: next, CreatedAt: time.Now()}
	if err := a.store.PutJob(j); err != nil {
		return nil, err
	}
	// No transcript entry: the job row is the record, and a job the model
	// created has already said so in the schedule tool's result.
	a.hub.Broadcast(wsEvent{Kind: "jobs"})
	return j, nil
}
