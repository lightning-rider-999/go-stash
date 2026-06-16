package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// streamEvent is a trivial event type for the streamer tests. NDJSON encodes it
// verbatim, so each line round-trips back to the value sent.
type streamEvent struct {
	Seq int    `json:"seq"`
	Msg string `json:"msg"`
}

// syncWriter is an io.Writer that records output under a mutex and signals on a
// channel each time a newline-terminated line is written, so a test can wait for
// exactly N lines before cancelling — making the stream assertions deterministic
// rather than time-dependent.
type syncWriter struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	lines chan struct{}
}

func newSyncWriter() *syncWriter { return &syncWriter{lines: make(chan struct{}, 64)} }

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	for _, b := range p {
		if b == '\n' {
			select {
			case w.lines <- struct{}{}:
			default:
			}
		}
	}
	return n, err
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// TestStreamEmitsOneLinePerEvent drives streamEvents with an in-memory event
// source: N events must produce N NDJSON lines in order, and a ctx cancel must
// stop the streamer cleanly (nil error, exit 0).
func TestStreamEmitsOneLinePerEvent(t *testing.T) {
	events := make(chan streamEvent)
	errCh := make(chan error)
	out := newSyncWriter()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- streamEvents[streamEvent](ctx, events, errCh, out) }()

	want := []streamEvent{{1, "first"}, {2, "second"}, {3, "third"}}
	for _, ev := range want {
		events <- ev
		select {
		case <-out.lines:
		case <-time.After(2 * time.Second):
			t.Fatal("event was not emitted within 2s")
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("streamEvents returned %v, want nil on cancel", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("streamEvents did not stop within 2s of cancel")
	}

	lines := nonEmptyLines(out.String())
	if len(lines) != len(want) {
		t.Fatalf("got %d NDJSON lines, want %d:\n%s", len(lines), len(want), out.String())
	}
	for i, line := range lines {
		var got streamEvent
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d not valid JSON (%q): %v", i, line, err)
		}
		if got != want[i] {
			t.Fatalf("line %d = %+v, want %+v", i, got, want[i])
		}
	}
}

func TestStreamStopsCleanlyOnCancel(t *testing.T) {
	events := make(chan streamEvent)
	errCh := make(chan error)
	out := newSyncWriter()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- streamEvents[streamEvent](ctx, events, errCh, out) }()

	events <- streamEvent{Seq: 1, Msg: "before-cancel"}
	select {
	case <-out.lines:
	case <-time.After(2 * time.Second):
		t.Fatal("event was not emitted within 2s")
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancel produced %v, want nil (clean stop)", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("streamEvents did not stop within 2s of ctx cancel")
	}

	if got := len(nonEmptyLines(out.String())); got != 1 {
		t.Fatalf("emitted %d lines before cancel, want 1", got)
	}
}

// TestStreamRedactsAPIKey: a subscription event whose string field carries a
// pre-signed ?apikey=<JWT> URL must be scrubbed before the NDJSON line reaches
// stdout, the same no-leak invariant the success path holds via writeOutput.
func TestStreamRedactsAPIKey(t *testing.T) {
	const jwt = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.sig"
	events := make(chan streamEvent, 1)
	errCh := make(chan error)
	out := newSyncWriter()

	events <- streamEvent{Seq: 1, Msg: "http://stash.local/scene/42/stream?apikey=" + jwt}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- streamEvents[streamEvent](ctx, events, errCh, out) }()

	select {
	case <-out.lines:
	case <-time.After(2 * time.Second):
		t.Fatal("event was not emitted within 2s")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("streamEvents did not stop within 2s of cancel")
	}

	s := out.String()
	if strings.Contains(s, jwt) {
		t.Errorf("stream line leaked the JWT:\n%s", s)
	}
	if !strings.Contains(s, "apikey=REDACTED") {
		t.Errorf("stream line is missing apikey=REDACTED:\n%s", s)
	}
	// Each emitted line is still valid NDJSON.
	for _, line := range nonEmptyLines(s) {
		var got streamEvent
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("stream line not valid JSON (%q): %v", line, err)
		}
	}
}

// TestStreamMapsTerminalErrorToTransport: a terminal error on the source's error
// channel must be classified as transport, exit 4.
func TestStreamMapsTerminalErrorToTransport(t *testing.T) {
	events := make(chan streamEvent)
	errCh := make(chan error, 1)
	out := newSyncWriter()

	errCh <- &transportError{err: errors.New("simulated terminal subscription failure")}

	err := streamEvents[streamEvent](context.Background(), events, errCh, out)
	if err == nil {
		t.Fatal("expected a terminal error, got nil")
	}
	if got := classifyExit(err); got != ExitTransport {
		t.Fatalf("terminal subscription error classified as %v, want %v", got, ExitTransport)
	}
}

// TestStreamCleanEndOnClosedEvents: when the events channel closes with no
// terminal error, the streamer stops cleanly (nil).
func TestStreamCleanEndOnClosedEvents(t *testing.T) {
	events := make(chan streamEvent)
	errCh := make(chan error)
	out := newSyncWriter()

	// Subscribe closes both channels together on a clean return; mirror that.
	close(events)
	close(errCh)
	if err := streamEvents[streamEvent](context.Background(), events, errCh, out); err != nil {
		t.Fatalf("closed events produced %v, want nil", err)
	}
}
