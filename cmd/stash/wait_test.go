package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lightning-rider-999/go-stashapp/stash"
)

// jobFixture builds a *stash.FindJobFindJob with just the fields the tracker
// reads, so a test can hand the tracker canned snapshots.
func jobFixture(id string, status stash.JobStatus, jobErr string) *stash.FindJobFindJob {
	return &stash.FindJobFindJob{Id: id, Status: status, Error: jobErr}
}

// fakeFindJob returns a findJob function that yields the queued snapshots in
// order, repeating the last one once exhausted. A snapshot of nil models a null
// (evicted/unknown) job; an error entry models a query failure.
type findJobStep struct {
	snap *stash.FindJobFindJob
	err  error
}

func fakeFindJob(steps ...findJobStep) func(context.Context, string) (*stash.FindJobFindJob, error) {
	var mu sync.Mutex
	i := 0
	return func(_ context.Context, _ string) (*stash.FindJobFindJob, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(steps) == 0 {
			return nil, nil
		}
		s := steps[i]
		if i < len(steps)-1 {
			i++
		}
		return s.snap, s.err
	}
}

// updateOf builds a jobUpdate carrying a status transition for the given job.
func updateOf(id string, t stash.JobStatusUpdateType, status stash.JobStatus, jobErr string) jobUpdate {
	return jobUpdate{Type: t, Job: &stash.JobFields{Id: id, Status: status, Error: jobErr}}
}

// scriptedSource returns a subscribe function whose nth call drains the nth
// element of scripts: a slice of (updates, terminal-error) channels. Each script
// closes its updates channel itself when it wants the run to end via reconcile.
type subScript struct {
	updates <-chan jobUpdate
	errs    <-chan error
}

func scriptedSource(scripts ...subScript) func(context.Context) (<-chan jobUpdate, <-chan error) {
	var mu sync.Mutex
	i := 0
	return func(context.Context) (<-chan jobUpdate, <-chan error) {
		mu.Lock()
		defer mu.Unlock()
		if i >= len(scripts) {
			// No more scripts: a closed, empty stream so the tracker reconciles.
			closed := make(chan jobUpdate)
			close(closed)
			return closed, make(chan error)
		}
		s := scripts[i]
		i++
		return s.updates, s.errs
	}
}

const testJobID = "job-42"

// TestWaitFinishes drives a job to FINISHED via the update stream -> exit 0.
func TestWaitFinishes(t *testing.T) {
	updates := make(chan jobUpdate, 3)
	updates <- updateOf(testJobID, stash.JobStatusUpdateTypeUpdate, stash.JobStatusRunning, "")
	updates <- updateOf(testJobID, stash.JobStatusUpdateTypeUpdate, stash.JobStatusFinished, "")
	close(updates)

	jt := &jobTracker{
		findJob:   fakeFindJob(findJobStep{snap: jobFixture(testJobID, stash.JobStatusRunning, "")}),
		subscribe: scriptedSource(subScript{updates: updates, errs: make(chan error)}),
		progress:  io.Discard,
	}
	if err := jt.track(context.Background(), testJobID); err != nil {
		t.Fatalf("track returned %v, want nil (FINISHED -> exit 0)", err)
	}
}

// TestWaitSeedAlreadyTerminal finishes immediately when the seed snapshot is
// already terminal, without ever subscribing.
func TestWaitSeedAlreadyTerminal(t *testing.T) {
	subscribed := false
	jt := &jobTracker{
		findJob: fakeFindJob(findJobStep{snap: jobFixture(testJobID, stash.JobStatusFinished, "")}),
		subscribe: func(context.Context) (<-chan jobUpdate, <-chan error) {
			subscribed = true
			return make(chan jobUpdate), make(chan error)
		},
		progress: io.Discard,
	}
	if err := jt.track(context.Background(), testJobID); err != nil {
		t.Fatalf("track returned %v, want nil", err)
	}
	if subscribed {
		t.Fatal("an already-terminal seed must not open a subscription")
	}
}

// TestWaitFailedSurfacesError maps FAILED to job-failed (9) with the job error.
func TestWaitFailedSurfacesError(t *testing.T) {
	updates := make(chan jobUpdate, 1)
	updates <- updateOf(testJobID, stash.JobStatusUpdateTypeUpdate, stash.JobStatusFailed, "disk full")
	close(updates)

	jt := &jobTracker{
		findJob:   fakeFindJob(findJobStep{snap: jobFixture(testJobID, stash.JobStatusRunning, "")}),
		subscribe: scriptedSource(subScript{updates: updates, errs: make(chan error)}),
		progress:  io.Discard,
	}
	err := jt.track(context.Background(), testJobID)
	if err == nil {
		t.Fatal("expected a job-failed error, got nil")
	}
	if got := classifyExit(err); got != ExitJobFailed {
		t.Fatalf("exit = %v, want %v", got, ExitJobFailed)
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("error %q does not surface the job's error", err.Error())
	}
}

// TestWaitDropResumeFinish: the stream ends (drop) while findJob still shows the
// job running, so the tracker resubscribes; the second stream finishes it.
func TestWaitDropResumeFinish(t *testing.T) {
	// First stream: one RUNNING update, then closes (a drop).
	first := make(chan jobUpdate, 1)
	first <- updateOf(testJobID, stash.JobStatusUpdateTypeUpdate, stash.JobStatusRunning, "")
	close(first)
	// Second stream: FINISHED.
	second := make(chan jobUpdate, 1)
	second <- updateOf(testJobID, stash.JobStatusUpdateTypeUpdate, stash.JobStatusFinished, "")
	close(second)

	jt := &jobTracker{
		// Seed RUNNING; reconcile after the drop also RUNNING (resume); then the
		// second stream delivers FINISHED.
		findJob: fakeFindJob(
			findJobStep{snap: jobFixture(testJobID, stash.JobStatusRunning, "")},
			findJobStep{snap: jobFixture(testJobID, stash.JobStatusRunning, "")},
		),
		subscribe: scriptedSource(
			subScript{updates: first, errs: make(chan error)},
			subScript{updates: second, errs: make(chan error)},
		),
		progress: io.Discard,
	}
	if err := jt.track(context.Background(), testJobID); err != nil {
		t.Fatalf("track returned %v, want nil after resume->finish", err)
	}
}

// TestWaitDropIndeterminateUnconfirmed: the stream drops and findJob then returns
// null, so the outcome is indeterminate -> unconfirmed (11) carrying the job id.
func TestWaitDropIndeterminateUnconfirmed(t *testing.T) {
	dropped := make(chan jobUpdate)
	close(dropped) // an immediate drop

	jt := &jobTracker{
		// Seed RUNNING; reconcile returns null (evicted/unknown).
		findJob: fakeFindJob(
			findJobStep{snap: jobFixture(testJobID, stash.JobStatusRunning, "")},
			findJobStep{snap: nil},
		),
		subscribe: scriptedSource(subScript{updates: dropped, errs: make(chan error)}),
		progress:  io.Discard,
	}
	err := jt.track(context.Background(), testJobID)
	if err == nil {
		t.Fatal("expected unconfirmed, got nil")
	}
	if got := classifyExit(err); got != ExitUnconfirmed {
		t.Fatalf("exit = %v, want %v", got, ExitUnconfirmed)
	}
	if !strings.Contains(err.Error(), testJobID) {
		t.Fatalf("unconfirmed error %q must carry the job id", err.Error())
	}
}

// TestWaitReconcileQueryErrorUnconfirmed: a drop followed by a findJob error is
// also indeterminate -> unconfirmed (11) with the job id.
func TestWaitReconcileQueryErrorUnconfirmed(t *testing.T) {
	dropped := make(chan jobUpdate)
	close(dropped)

	jt := &jobTracker{
		findJob: fakeFindJob(
			findJobStep{snap: jobFixture(testJobID, stash.JobStatusRunning, "")},
			findJobStep{err: errors.New("connection refused")},
		),
		subscribe: scriptedSource(subScript{updates: dropped, errs: make(chan error)}),
		progress:  io.Discard,
	}
	err := jt.track(context.Background(), testJobID)
	if got := classifyExit(err); got != ExitUnconfirmed {
		t.Fatalf("exit = %v, want %v (err=%v)", got, ExitUnconfirmed, err)
	}
	if !strings.Contains(err.Error(), testJobID) {
		t.Fatalf("error %q must carry the job id", err.Error())
	}
}

// TestWaitTimeoutStillRunning drives the --wait-timeout path under
// testing/synctest: a job that never reaches a terminal state, a timeout that
// elapses in virtual time -> still-running (10) carrying the job id.
func TestWaitTimeoutStillRunning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// A stream that delivers one RUNNING update and then blocks forever, so
		// only the timeout can end the wait.
		updates := make(chan jobUpdate, 1)
		updates <- updateOf(testJobID, stash.JobStatusUpdateTypeUpdate, stash.JobStatusRunning, "")
		// deliberately not closed and never sends terminal.

		jt := &jobTracker{
			findJob: fakeFindJob(findJobStep{snap: jobFixture(testJobID, stash.JobStatusRunning, "")}),
			subscribe: func(context.Context) (<-chan jobUpdate, <-chan error) {
				return updates, make(chan error)
			},
			newTimer: time.After,
			progress: io.Discard,
			timeout:  30 * time.Second,
		}

		errc := make(chan error, 1)
		go func() { errc <- jt.track(context.Background(), testJobID) }()

		// Let all goroutines settle on their blocking selects, then advance the
		// bubble's virtual clock past the timeout.
		synctest.Wait()
		time.Sleep(31 * time.Second)

		err := <-errc
		if got := classifyExit(err); got != ExitStillRunning {
			t.Fatalf("exit = %v, want %v (err=%v)", got, ExitStillRunning, err)
		}
		if !strings.Contains(err.Error(), testJobID) {
			t.Fatalf("still-running error %q must carry the job id", err.Error())
		}
	})
}

// TestWaitContextCancel: a cancelled context stops the wait cleanly, returning
// the context error (not a taxonomy failure).
func TestWaitContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	jt := &jobTracker{
		findJob: fakeFindJob(findJobStep{snap: jobFixture(testJobID, stash.JobStatusRunning, "")}),
		subscribe: func(context.Context) (<-chan jobUpdate, <-chan error) {
			return make(chan jobUpdate), make(chan error)
		},
		progress: io.Discard,
	}
	errc := make(chan error, 1)
	go func() { errc <- jt.track(ctx, testJobID) }()
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("track did not return within 2s of cancel")
	}
}

// TestJobIDFromData checks the scalar job-id extraction across shapes.
func TestJobIDFromData(t *testing.T) {
	id, err := jobIDFromData([]byte(`{"metadataScan":"abc123"}`))
	if err != nil || id != "abc123" {
		t.Fatalf("got (%q,%v), want (abc123,nil)", id, err)
	}
	if _, err := jobIDFromData([]byte(`{"metadataScan":{"nested":true}}`)); err == nil {
		t.Fatal("expected an error for a non-scalar job field")
	}
}
