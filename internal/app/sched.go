package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	minJudgementInterval = 15 * time.Minute
	checkTimeout         = 60 * time.Second
	breakerWindow        = 90 * time.Second
	breakerProbeEvery    = 5 * time.Minute
	maxJobFailures       = 3
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
		if now.After(j.ExpiresAt) {
			s.expire(j)
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
			s.deadLetter(j, "unavailable", "session no longer exists")
			_ = s.app.store.DeleteJob(j.ID)
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
	j.RunCount++

	switch j.Kind {
	case "due":
		// No condition to test. The user asked for a time, the time arrived,
		// and arriving is the whole of what they asked for.
		if _, err := a.runTurn(ctx, sess, turnOpts{UserText: j.Prompt, JobID: j.ID}); err != nil {
			s.jobFailed(j, err)
			return
		}
		j.LastStatus = "fired"
		s.succeeded(j)
		s.reschedule(j, true)

	case "check":
		met, err := s.runCheck(ctx, j, sess)
		if err != nil {
			s.jobFailed(j, err)
			return
		}
		if !met {
			j.LastStatus = "not_fired"
			s.reschedule(j, false)
			return
		}
		j.LastStatus = "fired"
		if _, err := a.runTurn(ctx, sess, turnOpts{UserText: j.Prompt, JobID: j.ID}); err != nil {
			s.jobFailed(j, err)
			return
		}
		s.succeeded(j)
		s.reschedule(j, true)

	default:
		// The model decides, and signals by calling notify.
		fired, err := a.runTurn(ctx, sess, turnOpts{UserText: j.Prompt, JobID: j.ID, Buffered: true})
		if err != nil {
			s.jobFailed(j, err)
			return
		}
		s.succeeded(j)
		if fired {
			j.LastStatus = "fired"
		} else {
			j.LastStatus = "not_fired"
		}
		s.reschedule(j, fired)
	}
}

// runCheck runs the job's check command. It reports whether the condition is
// met. A non-zero exit is an answer, not a failure; a check that cannot run or
// will not finish is a failure, so that a check which never decides anything is
// surfaced rather than looping quietly until the job expires.
func (s *Scheduler) runCheck(ctx context.Context, j *Job, sess *Session) (bool, error) {
	cctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "/bin/sh", "-c", j.Check)
	cmd.Dir = s.app.cfg.Workspace
	// Killing the shell does not close pipes a grandchild still holds, and
	// CombinedOutput waits for every writer. WaitDelay bounds that wait, so the
	// timeout above bounds the whole call.
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()

	if cctx.Err() != nil {
		return false, fmt.Errorf("check did not finish within %s: %s", checkTimeout, truncate(j.Check, 120))
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return false, fmt.Errorf("check could not run: %w", err)
	}

	met := err == nil
	status, verdict := "not_fired", "not met"
	if met {
		status, verdict = "fired", "met"
	}
	s.app.appendEvent(sess.ID, Entry{EventKind: "job_check", JobID: j.ID, Status: status,
		Text: fmt.Sprintf("check: %s → %s%s", truncate(j.Check, 120), verdict, summarise(string(out)))})
	return met, nil
}

func summarise(out string) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	return " · " + truncate(strings.ReplaceAll(out, "\n", " "), 160)
}

func (s *Scheduler) reschedule(j *Job, fired bool) {
	a := s.app
	if fired && j.OnConditionMet == "delete" {
		_ = a.store.DeleteJob(j.ID)
		a.hub.Broadcast(wsEvent{Kind: "jobs"})
		return
	}
	next, err := nextRun(j.Schedule, time.Now())
	if err != nil {
		if errors.Is(err, error(errOneShotDone)) || !fired {
			s.deadLetterIfUnfired(j)
		}
		_ = a.store.DeleteJob(j.ID)
		a.hub.Broadcast(wsEvent{Kind: "jobs"})
		return
	}
	j.NextRunAt = next
	_ = a.store.PutJob(j)
	a.hub.Broadcast(wsEvent{Kind: "jobs"})
}

func (s *Scheduler) deadLetterIfUnfired(j *Job) {
	if j.LastStatus != "fired" {
		s.deadLetter(j, "expired", "schedule ended without the condition being met")
	}
}

func (s *Scheduler) expire(j *Job) {
	// Expiring without firing has not failed technically, yet the thing the user
	// asked about never happened. It becomes a dead letter.
	if j.LastStatus != "fired" {
		s.deadLetter(j, "expired", "expired without firing")
	}
	_ = s.app.store.DeleteJob(j.ID)
	s.app.hub.Broadcast(wsEvent{Kind: "jobs"})
}

func (s *Scheduler) deadLetter(j *Job, reason, detail string) {
	a := s.app
	if a.store.OpenDeadLetterFor(j.ID) {
		return // one open dead letter per job, not one per failure
	}
	d := &DeadLetter{ID: newID(), JobID: j.ID, SessionID: j.SessionID, JobSpec: *j,
		Reason: reason, Detail: detail, RunCount: j.RunCount, Status: "open", CreatedAt: time.Now()}
	_ = a.store.PutDeadLetter(d)
	a.appendEvent(j.SessionID, Entry{EventKind: "dead_letter", JobID: j.ID,
		Text: fmt.Sprintf("job gave up (%s): %s", reason, detail)})
	a.Notify(Notification{Title: "Job gave up",
		Body:      fmt.Sprintf("%s — %s", truncate(j.Prompt, 80), detail),
		SessionID: j.SessionID, JobID: j.ID, Force: true})
	a.hub.Broadcast(wsEvent{Kind: "dead_letters"})
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
		s.app.Notify(Notification{Title: "Scheduler resumed",
			Body: "A held job succeeded. The schedule is running again.", Force: true})
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
		a.Notify(Notification{Title: "Scheduler paused",
			Body: "Jobs are failing: " + truncate(err.Error(), 160), Force: true})
		a.hub.Broadcast(wsEvent{Kind: "status"})
	}
	a.appendEvent(j.SessionID, Entry{EventKind: "job_error", JobID: j.ID,
		Text: "job run failed: " + truncate(err.Error(), 300)})

	if open {
		// Jobs failing while the breaker is open do not become dead letters.
		j.NextRunAt = time.Now().Add(breakerProbeEvery)
		_ = a.store.PutJob(j)
		return
	}
	j.FailCount++
	if j.FailCount >= maxJobFailures {
		s.deadLetter(j, "error", truncate(err.Error(), 300))
		_ = a.store.DeleteJob(j.ID)
		a.hub.Broadcast(wsEvent{Kind: "jobs"})
		return
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
	SessionID      string `json:"session_id"`
	Schedule       string `json:"schedule"`
	Check          string `json:"check"`
	Prompt         string `json:"prompt"`
	ExpiresAt      string `json:"expires_at"`
	OnConditionMet string `json:"on_condition_met"`
	ReasonNoCheck  string `json:"reason_no_check"`
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
	// A one-shot instant with no check is a reminder: it has no condition to
	// test, so neither the tick floor nor reason_no_check applies to it.
	if spec.Check == "" && !oneShot(spec.Schedule) {
		if d := scheduleInterval(spec.Schedule); d > 0 && d < minJudgementInterval {
			return nil, fmt.Errorf("a job without a check may not tick more often than every 15 minutes (got %s); express the condition as a check command, or schedule a single RFC 3339 instant if there is no condition", d)
		}
		if strings.TrimSpace(spec.ReasonNoCheck) == "" {
			return nil, fmt.Errorf("a repeating job without a check must state reason_no_check: why no shell command could decide this condition")
		}
	}
	next, err := nextRun(spec.Schedule, time.Now())
	if err != nil {
		return nil, err
	}
	expires := time.Now().Add(24 * time.Hour)
	if spec.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, spec.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("expires_at: %w", err)
		}
		expires = t
	}
	onMet := spec.OnConditionMet
	if onMet != "continue" {
		onMet = "delete"
	}
	j := &Job{ID: newID(), SessionID: sess.ID, Schedule: spec.Schedule, Check: spec.Check,
		Prompt: spec.Prompt, ExpiresAt: expires, OnConditionMet: onMet,
		NextRunAt: next, CreatedAt: time.Now()}
	j.Kind = jobKind(j)
	if err := a.store.PutJob(j); err != nil {
		return nil, err
	}
	kind := j.Kind
	if j.Kind == "judgement" {
		kind = "judgement, no command could decide it: " + spec.ReasonNoCheck
	}
	a.appendEvent(sess.ID, Entry{EventKind: "job_created", JobID: j.ID,
		Text: fmt.Sprintf("job %s created · %s · %s · expires %s · %s",
			j.ID, j.Schedule, kind, expires.Format(time.RFC3339), truncate(j.Prompt, 120))})
	a.hub.Broadcast(wsEvent{Kind: "jobs"})
	return j, nil
}

func (a *App) scheduleTool(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error) {
	var in struct {
		JobSpec
		Action string `json:"action"`
		ID     string `json:"id"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	switch in.Action {
	case "create":
		spec := in.JobSpec
		if spec.SessionID == "" {
			spec.SessionID = tc.SessionID
		}
		j, err := a.CreateJob(spec)
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("job %s created, next run %s, expires %s",
			j.ID, j.NextRunAt.Format(time.RFC3339), j.ExpiresAt.Format(time.RFC3339)), nil
	case "list":
		jobs, err := a.store.SessionJobs(tc.SessionID)
		if err != nil {
			return nil, err
		}
		if len(jobs) == 0 {
			return "no jobs in this session", nil
		}
		var sb strings.Builder
		for _, j := range jobs {
			check := j.Check
			if check == "" {
				check = "(judgement, calls the model every tick)"
			}
			fmt.Fprintf(&sb, "%s · %s · check: %s · next %s · expires %s · %s\n",
				j.ID, j.Schedule, check, j.NextRunAt.Format(time.RFC3339),
				j.ExpiresAt.Format(time.RFC3339), truncate(j.Prompt, 100))
		}
		return sb.String(), nil
	case "edit":
		j, err := a.store.Job(in.ID)
		if err != nil || j == nil {
			return nil, fmt.Errorf("no job %s", in.ID)
		}
		if in.Schedule != "" {
			next, err := nextRun(in.Schedule, time.Now())
			if err != nil {
				return nil, err
			}
			j.Schedule, j.NextRunAt = in.Schedule, next
		}
		if in.Prompt != "" {
			j.Prompt = in.Prompt
		}
		if in.Check != "" {
			j.Check = in.Check
		}
		if in.ExpiresAt != "" {
			t, err := time.Parse(time.RFC3339, in.ExpiresAt)
			if err != nil {
				return nil, err
			}
			j.ExpiresAt = t
		}
		if in.OnConditionMet != "" {
			j.OnConditionMet = in.OnConditionMet
		}
		j.Kind = jobKind(j)
		if err := a.store.PutJob(j); err != nil {
			return nil, err
		}
		a.hub.Broadcast(wsEvent{Kind: "jobs"})
		return "job " + j.ID + " updated", nil
	case "delete":
		if err := a.store.DeleteJob(in.ID); err != nil {
			return nil, err
		}
		a.appendEvent(tc.SessionID, Entry{EventKind: "job_deleted", JobID: in.ID,
			Text: "job " + in.ID + " deleted"})
		a.hub.Broadcast(wsEvent{Kind: "jobs"})
		return "job " + in.ID + " deleted", nil
	}
	return nil, fmt.Errorf("unknown action %q", in.Action)
}

// ReplayDeadLetter recreates the job from its stored specification with a fresh
// expiry, attached to the live session of the chain.
func (a *App) ReplayDeadLetter(id string) (*Job, error) {
	d, err := a.store.DeadLetter(id)
	if err != nil {
		return nil, err
	}
	live := a.LiveSession(d.SessionID)
	if live == nil {
		return nil, fmt.Errorf("session %s no longer exists", d.SessionID)
	}
	spec := JobSpec{SessionID: live.ID, Schedule: d.JobSpec.Schedule, Check: d.JobSpec.Check,
		Prompt: d.JobSpec.Prompt, OnConditionMet: d.JobSpec.OnConditionMet,
		ExpiresAt:     time.Now().Add(24 * time.Hour).Format(time.RFC3339),
		ReasonNoCheck: "replayed from dead letter " + d.ID}
	j, err := a.CreateJob(spec)
	if err != nil {
		return nil, err
	}
	if err := a.store.CloseDeadLetter(d.ID); err != nil {
		return nil, err
	}
	a.hub.Broadcast(wsEvent{Kind: "dead_letters"})
	return j, nil
}
