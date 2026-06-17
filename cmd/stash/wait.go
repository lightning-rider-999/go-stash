package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/lightning-rider-999/go-stash/stash"
)

// waitTimeoutFlag and waitFlag are the flag names that turn a job-returning
// mutation into a tracked, blocking call.
const (
	waitFlag        = "wait"
	waitTimeoutFlag = "wait-timeout"
)

// addWaitFlags registers --wait and --wait-timeout on a job-returning leaf. They
// are local to job-returning leaves so they never appear where there is no job
// to track. --wait-timeout defaults to zero, which means no client-side bound:
// the command waits indefinitely for the job to reach a terminal state.
func addWaitFlags(leaf *cobra.Command, spec commandSpec) {
	if !spec.JobReturning {
		return
	}
	leaf.Flags().Bool(waitFlag, false,
		"block until the enqueued job reaches a terminal state; exit 0 on "+
			"FINISHED, 9 on FAILED/CANCELLED, 10 on --wait-timeout, 11 if the "+
			"job's outcome cannot be confirmed (re-attach with its id).")
	leaf.Flags().Duration(waitTimeoutFlag, 0,
		"with --wait, give up after this duration and exit 10 (still-running) "+
			"with the job id; the default (0) waits indefinitely.")
}

// waitRequested reports whether --wait was set on the command.
func waitRequested(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool(waitFlag)
	return v
}

// jobUpdate is one status transition for a tracked job, decoupled from the
// generated WebSocket response wrapper so the tracker's source can be faked in a
// test without constructing a genqlient BaseResponse. Job is nil only on a
// malformed update, which the tracker skips.
type jobUpdate struct {
	Type stash.JobStatusUpdateType
	Job  *stash.JobFields
}

// jobOutcome is the resolved end state of a tracked job: a terminal status and,
// for a failure, the job's reported error. jobErr is a pointer because the Job
// SDL types error as a nullable String: a job with no error is genuinely nil,
// not the empty string. It is the value the state machine converges on before
// mapping to an exit code.
type jobOutcome struct {
	status stash.JobStatus
	jobErr *string
}

// jobTracker runs the --wait state machine for one job. Every external
// dependency is an injected function or value, so a test drives the whole
// machine — seed, track, drop/reconcile, timeout — with no socket and no wall
// clock:
//
//   - findJob queries the job's current snapshot. A null job (evicted/unknown)
//     is reported as (nil, nil); a transport/GraphQL failure is a non-nil error.
//   - subscribe opens the status-update stream and its terminal error channel,
//     mirroring [stash.Subscribe]. It is called once per (re)subscribe.
//   - newTimer builds the timeout channel and its stop function. Production
//     leaves it nil, so track builds a real time.NewTimer it can Stop on every
//     exit; a testing/synctest test injects one driven by the bubble's virtual
//     clock to make the timeout deterministic. A zero timeout disables it
//     (newTimer is never called).
//   - progress receives one NDJSON line per observed transition, so an agent can
//     watch; it may be nil.
type jobTracker struct {
	findJob   func(ctx context.Context, id string) (*stash.FindJobFindJob, error)
	subscribe func(ctx context.Context) (<-chan jobUpdate, <-chan error)
	newTimer  func(d time.Duration) (<-chan time.Time, func() bool)
	progress  io.Writer
	timeout   time.Duration

	// maxResubscribes bounds how many times a drop triggers a fresh subscribe
	// after reconcile shows the job still in flight, so a flapping connection
	// cannot loop forever. Zero falls back to a small default.
	maxResubscribes int
}

// terminalStatus reports whether a JobStatus is an end state, and whether that
// end state is a success (FINISHED) or a failure (FAILED/CANCELLED). Ready,
// Running, and Stopping are explicitly in-flight: a STOPPING job has been asked
// to stop but has not yet settled into CANCELLED, so it is not terminal until it
// does. Listing all six JobStatus values keeps this exhaustive, so a new status
// added to the Stash schema is a visible gap here rather than a silent default.
func terminalStatus(s stash.JobStatus) (terminal, success bool) {
	switch s {
	case stash.JobStatusFinished:
		return true, true
	case stash.JobStatusFailed, stash.JobStatusCancelled:
		return true, false
	case stash.JobStatusReady, stash.JobStatusRunning, stash.JobStatusStopping:
		return false, false
	default:
		return false, false
	}
}

// track runs the state machine to a terminal outcome or a typed exit condition.
// It returns nil on a clean stop and an *exitCodeError carrying the precise
// taxonomy code otherwise:
//
//   - nil: the job FINISHED (exit 0), or ctx was cancelled (SIGINT) — a
//     cancellation is a clean stop, matching the subscription streamer, not a
//     failure.
//   - job-failed (9): the job ended FAILED or CANCELLED; the job's error is in
//     the envelope.
//   - still-running (10): --wait-timeout elapsed before a terminal state; the
//     job id is in the envelope so the agent can re-attach.
//   - unconfirmed (11): the outcome could not be confirmed — a drop reconcile
//     found the job null or erroring, or the resubscribe bound was exhausted with
//     the job still in flight. The job id is in the envelope, and the last
//     stream/transport cause is wrapped into the message so a flapping socket is
//     visible to whoever debugs it.
//
// The flow is: SEED (findJob; finish if already terminal) -> TRACK (follow
// updates) -> on a REMOVE or a stream drop, RECONCILE (re-query findJob) which
// either finishes (terminal -> exit), resumes tracking (still running ->
// resubscribe), or reports unconfirmed (indeterminate/exhausted, wrapping the
// cause).
func (jt *jobTracker) track(ctx context.Context, jobID string) error {
	// SEED: an already-terminal job finishes without ever subscribing.
	snap, err := jt.findJob(ctx, jobID)
	if err == nil && snap != nil {
		if term, _ := terminalStatus(snap.Status); term {
			jt.emit(jobID, snap.Status, snap.Progress)
			return outcomeToExit(jobID, jobOutcome{status: snap.Status, jobErr: snap.Error})
		}
		jt.emit(jobID, snap.Status, snap.Progress)
	}
	// A seed failure or a null job is not fatal yet: the subscription may still
	// deliver the terminal transition. Reconcile handles a confirmed-null later.

	var timer <-chan time.Time
	if jt.timeout > 0 {
		newTimer := jt.newTimer
		if newTimer == nil {
			// Production default: a real timer that is stopped on every exit from
			// track, so a job that finishes or drops before the deadline does not
			// leave the timer's goroutine and channel alive until it elapses.
			newTimer = func(d time.Duration) (<-chan time.Time, func() bool) {
				t := time.NewTimer(d)
				return t.C, t.Stop
			}
		}
		ch, stop := newTimer(jt.timeout)
		timer = ch
		defer stop()
	}

	maxResub := jt.maxResubscribes
	if maxResub <= 0 {
		maxResub = defaultMaxResubscribes
	}

	// lastStreamErr keeps the most recent terminal stream/transport cause so it
	// can be wrapped into the exhausted-unconfirmed error: an agent debugging a
	// flapping socket then sees the underlying WS failure, not just a count.
	var lastStreamErr error

	for resub := 0; ; resub++ {
		// Each subscription attempt gets its own cancellable context, cancelled
		// before the next attempt. Cancelling lets the prior stash.Subscribe
		// observe ctx.Err(), close its events channel, and unblock its projection
		// goroutine — otherwise a REMOVE-then-still-running resubscribe would
		// orphan the old socket and its goroutine on every loop.
		attemptCtx, cancel := context.WithCancel(ctx)
		outcome, action, streamErr := jt.trackOnce(attemptCtx, jobID, timer)
		cancel()
		if streamErr != nil {
			lastStreamErr = streamErr
		}
		switch action {
		case actionFinished:
			return outcomeToExit(jobID, outcome)
		case actionTimeout:
			return newExitCodeError(ExitStillRunning,
				fmt.Errorf("job %s still running after %s", jobID, jt.timeout))
		case actionCancelled:
			// SIGINT or parent cancellation: a clean stop (exit 0), matching the
			// subscription streamer, not an internal error.
			return nil
		case actionReconcile:
			// A REMOVE or a stream drop: re-query to decide.
			outcome, resolved, reErr := jt.reconcile(ctx, jobID)
			if reErr != nil {
				if errors.Is(reErr, context.Canceled) {
					// Cancelled mid-reconcile: a clean stop (exit 0), not an
					// unconfirmed failure.
					return nil
				}
				return reErr
			}
			if resolved {
				return outcomeToExit(jobID, outcome)
			}
			// Still in flight: resubscribe within the bound.
			if resub >= maxResub {
				return newExitCodeError(ExitUnconfirmed,
					exhaustedErr(jobID, maxResub, lastStreamErr))
			}
			continue
		}
	}
}

// exhaustedErr builds the unconfirmed-after-exhaustion error, wrapping the last
// stream/transport cause with %w when there was one so errors.Is/As reach it and
// the message names the underlying failure.
func exhaustedErr(jobID string, maxResub int, cause error) error {
	if cause != nil {
		return fmt.Errorf("job %s: lost the update stream and could not confirm its outcome after %d attempts: %w", jobID, maxResub, cause)
	}
	return fmt.Errorf("job %s: lost the update stream and could not confirm its outcome after %d attempts", jobID, maxResub)
}

// defaultMaxResubscribes bounds resubscribe attempts after a drop that reconcile
// shows is still in flight.
const defaultMaxResubscribes = 10

// trackAction is what trackOnce observed when its subscription run ended.
type trackAction int

const (
	// actionReconcile: the stream ended (REMOVE or drop); re-query to decide.
	actionReconcile trackAction = iota
	// actionFinished: a terminal status was observed directly on the stream.
	actionFinished
	// actionTimeout: the --wait-timeout elapsed first.
	actionTimeout
	// actionCancelled: ctx was cancelled (SIGINT).
	actionCancelled
)

// trackOnce follows one subscription's update stream until the job reaches a
// terminal status (actionFinished), the stream ends, is REMOVEd, or errors
// terminally (actionReconcile), the timeout fires (actionTimeout), or ctx is
// cancelled (actionCancelled). It filters updates to jobID and emits progress
// for each.
//
// The third return is the terminal stream/transport cause when one ended the
// run; it always rides with actionReconcile (reconcile decides whether it
// matters) and is non-nil only for a real failure, so track can wrap it into the
// exhausted-unconfirmed diagnostic. It is nil for a clean end or a REMOVE.
func (jt *jobTracker) trackOnce(ctx context.Context, jobID string, timer <-chan time.Time) (jobOutcome, trackAction, error) {
	updates, errCh := jt.subscribe(ctx)
	for {
		select {
		case <-ctx.Done():
			return jobOutcome{}, actionCancelled, nil
		case <-timer:
			return jobOutcome{}, actionTimeout, nil
		case err := <-errCh:
			if err == nil || errors.Is(err, context.Canceled) {
				// A clean stream end maps to reconcile: confirm via findJob.
				return jobOutcome{}, actionReconcile, nil
			}
			// A terminal subscription failure: reconcile decides if it matters.
			return jobOutcome{}, actionReconcile, classifyStreamErr(err)
		case up, ok := <-updates:
			if !ok {
				return jobOutcome{}, actionReconcile, nil
			}
			if up.Job == nil || up.Job.Id != jobID {
				continue
			}
			if up.Type == stash.JobStatusUpdateTypeRemove {
				return jobOutcome{}, actionReconcile, nil
			}
			jt.emit(jobID, up.Job.Status, up.Job.Progress)
			if term, _ := terminalStatus(up.Job.Status); term {
				return jobOutcome{status: up.Job.Status, jobErr: up.Job.Error}, actionFinished, nil
			}
		}
	}
}

// reconcile re-queries the job after a drop or REMOVE to decide the next move. It
// returns an explicit (outcome, resolved, err) triple so the "keep waiting"
// signal is unambiguous — never a bare nil,nil the caller has to interpret:
//
//   - (terminal outcome, true, nil): findJob shows a terminal job. The caller
//     maps the outcome to an exit code.
//   - (zero, false, nil): findJob shows the job still in flight. resolved=false
//     with a nil error is the explicit "keep waiting" signal; the caller
//     resubscribes. The zero outcome must not be read.
//   - (zero, false, unconfirmed error): findJob returned null or errored, so the
//     outcome is indeterminate; the agent is told to re-attach with the job id. A
//     mid-reconcile context cancellation returns ctx.Err() instead, which the
//     caller treats as a clean stop.
func (jt *jobTracker) reconcile(ctx context.Context, jobID string) (jobOutcome, bool, error) {
	snap, err := jt.findJob(ctx, jobID)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return jobOutcome{}, false, ctx.Err()
		}
		return jobOutcome{}, false, newExitCodeError(ExitUnconfirmed,
			fmt.Errorf("job %s: could not confirm outcome after stream drop: %w", jobID, err))
	}
	if snap == nil {
		// A null job after a drop is indeterminate: it may have finished and been
		// evicted, or never existed. The agent re-attaches with the id.
		return jobOutcome{}, false, newExitCodeError(ExitUnconfirmed,
			fmt.Errorf("job %s: not found after stream drop; re-attach to confirm its outcome", jobID))
	}
	jt.emit(jobID, snap.Status, snap.Progress)
	if term, _ := terminalStatus(snap.Status); term {
		return jobOutcome{status: snap.Status, jobErr: snap.Error}, true, nil
	}
	// Still in flight: the explicit keep-waiting signal.
	return jobOutcome{}, false, nil
}

// outcomeToExit maps a terminal outcome to its exit. FINISHED is success (nil);
// FAILED/CANCELLED is job-failed (9) with the job error surfaced. A non-terminal
// status here is a logic error and is reported as unconfirmed rather than a
// false success.
//
// A server-side CANCELLED job (someone stopped the job from the Stash UI or
// another client) deliberately maps to job-failed (9), the same as FAILED, not
// to a clean exit. This conflates "another actor cancelled the job" with
// "failure", which is intentional: the frozen exit-code taxonomy has no distinct
// "job cancelled by a third party" code, and from this client's standpoint a job
// it was waiting on did not finish its work — a non-zero exit is the safer signal
// for a script. It is distinct from THIS client's own SIGINT, which cancels the
// wait locally (the ctx-cancel path) and exits 0 without ever reading a CANCELLED
// status off the wire.
func outcomeToExit(jobID string, o jobOutcome) error {
	term, success := terminalStatus(o.status)
	switch {
	case term && success:
		return nil
	case term:
		msg := fmt.Sprintf("job %s ended %s", jobID, o.status)
		if o.jobErr != nil && *o.jobErr != "" {
			msg = *o.jobErr
		}
		return newExitCodeError(ExitJobFailed,
			fmt.Errorf("job %s %s: %s", jobID, o.status, msg))
	default:
		return newExitCodeError(ExitUnconfirmed,
			fmt.Errorf("job %s: ended in non-terminal status %s", jobID, o.status))
	}
}

// emit writes one progress line (NDJSON) describing an observed transition, so
// an agent can watch the job advance. Progress goes to the tracker's writer
// (stderr in production) to keep stdout free of interleaving; the final outcome
// is the exit code and the error envelope. A nil writer disables progress.
//
// progress is a pointer because the Job SDL types progress as a nullable Float:
// a job that has not reported progress yet marshals to JSON null, not a
// fabricated 0.
func (jt *jobTracker) emit(jobID string, status stash.JobStatus, progress *float64) {
	if jt.progress == nil {
		return
	}
	line := struct {
		Job      string   `json:"job"`
		Status   string   `json:"status"`
		Progress *float64 `json:"progress"`
	}{Job: jobID, Status: string(status), Progress: progress}
	b, err := json.Marshal(line)
	if err != nil {
		return
	}
	b = append(b, '\n')
	_, _ = jt.progress.Write(b)
}

// runWait is the production entrypoint: it runs the job-returning mutation, reads
// the job id from the response, and tracks the job to a terminal state with a
// jobTracker wired to the real SDK (FindJob + a jobsSubscribe-backed Subscribe).
// newTimer is left nil so track builds a stoppable time.NewTimer; a
// testing/synctest test of the timeout path injects its own timer over the
// bubble's virtual time. Progress lines go to stderr; the job id (and, on
// success, the final response) go to stdout.
func runWait(cmd *cobra.Command, c *stash.Client, spec commandSpec, vars map[string]json.RawMessage, format string) error {
	ctx := cmd.Context()

	// Run the mutation and capture both the rendered response (for stdout) and
	// the raw data, from which the job id is extracted.
	jobID, err := runJobMutation(ctx, c, spec, vars, format, cmd.OutOrStdout())
	if err != nil {
		// SIGINT (or parent cancellation) during the enqueuing mutation is a clean
		// stop (exit 0), the same disposition as a cancellation during the wait
		// phase below — without this, classifyError would wrap the cancelled
		// context as a transport failure (exit 4), making Ctrl-C's exit code
		// depend on whether the job had been enqueued yet. The cancelled context
		// stays reachable through the SDK's transportError chain.
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	if jobID == "" {
		return fmt.Errorf("%s: --wait set but the response carried no job id", spec.OpName)
	}

	return trackJob(cmd, c, jobID)
}

// trackJob builds a jobTracker wired to the real SDK (FindJob + a
// jobsSubscribe-backed Subscribe) and follows jobID to a terminal state. It is
// shared by runWait (after the enqueuing mutation) and the import-objects path
// (after the multipart upload), so --wait behaves identically for both.
// --wait-timeout is read from the leaf's flag; progress lines go to stderr.
// newTimer is left nil so track builds a stoppable time.NewTimer it Stops on
// exit, so a job that finishes before --wait-timeout does not leak the timer.
func trackJob(cmd *cobra.Command, c *stash.Client, jobID string) error {
	ctx := cmd.Context()
	timeout, _ := cmd.Flags().GetDuration(waitTimeoutFlag)
	jt := &jobTracker{
		findJob: func(ctx context.Context, id string) (*stash.FindJobFindJob, error) {
			resp, err := stash.FindJob(ctx, c.GraphQL(), &stash.FindJobInput{Id: id})
			if err != nil {
				return nil, classifyError(err)
			}
			return resp.FindJob, nil
		},
		subscribe: jobsSubscribeSource(ctx, c),
		progress:  cmd.ErrOrStderr(),
		timeout:   timeout,
	}
	return jt.track(ctx, jobID)
}

// jobsSubscribeSource adapts the typed jobsSubscribe stream to the tracker's
// jobUpdate source: it opens [stash.Subscribe] over the generated JobsSubscribe
// function and projects each WebSocket response to a jobUpdate, dropping
// malformed frames. The returned closure is called once per (re)subscribe.
func jobsSubscribeSource(_ context.Context, c *stash.Client) func(context.Context) (<-chan jobUpdate, <-chan error) {
	return func(ctx context.Context) (<-chan jobUpdate, <-chan error) {
		raw, errCh := stash.Subscribe(ctx, c, adaptSubscribe(stash.JobsSubscribe))
		out := make(chan jobUpdate)
		go func() {
			defer close(out)
			for resp := range raw {
				u := projectJobUpdate(resp)
				if u == nil {
					continue
				}
				select {
				case out <- *u:
				case <-ctx.Done():
					return
				}
			}
		}()
		return out, errCh
	}
}

// projectJobUpdate pulls the jobUpdate out of a JobsSubscribe WebSocket response,
// returning nil for an empty or malformed frame.
func projectJobUpdate(resp stash.JobsSubscribeWsResponse) *jobUpdate {
	if resp.Data == nil || resp.Data.JobsSubscribe == nil {
		return nil
	}
	u := resp.Data.JobsSubscribe
	return &jobUpdate{Type: u.Type, Job: u.Job}
}

// runJobMutation executes a job-returning mutation, renders its response to out,
// and returns the enqueued job's id. The job id is the scalar string under the
// operation's single root field (for example {"metadataScan": "<id>"}).
func runJobMutation(ctx context.Context, c *stash.Client, spec commandSpec, vars map[string]json.RawMessage, format string, out io.Writer) (string, error) {
	var data json.RawMessage
	req := requestFor(spec, vars)
	resp := &graphql.Response{Data: &data}
	if err := c.GraphQL().MakeRequest(ctx, req, resp); err != nil {
		return "", classifyError(err)
	}
	if err := writeOutput(out, format, spec, data); err != nil {
		return "", err
	}
	return jobIDFromData(data)
}

// jobIDFromData extracts the bare job-id string a job-returning mutation returns
// under its single root field. The response data is {"<rootField>": "<jobid>"};
// the root field is unwrapped and decoded as a JSON string. A non-string or
// absent value yields an empty id, which runWait reports as an error.
func jobIDFromData(data json.RawMessage) (string, error) {
	inner := unwrapResult(data)
	switch v := inner.(type) {
	case string:
		return v, nil
	case nil:
		return "", nil
	default:
		return "", fmt.Errorf("job-returning mutation did not return a scalar job id (got %T)", inner)
	}
}
