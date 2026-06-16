package stash

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/gorilla/websocket"
)

// --- A hermetic graphql-transport-ws server, backed by gorilla over httptest.

// wsScript drives one connection: it completes connection_init, then on each
// subscribe message runs the supplied frame emitter.
type wsTestServer struct {
	t *testing.T
	// onSubscribe is called with the subscription ID for each subscribe frame;
	// it emits next/complete/error frames by calling the provided send func.
	onSubscribe func(send func(msgType string, payload string), id string)
	// connInitParams captures the connection_init payload params of the most
	// recent connection, for asserting the ApiKey was forwarded.
	connInitParams atomic.Value // map[string]any
	// dropAfterSubscribe, when > 0, closes the connection abruptly after this
	// many subscribe frames across the server's lifetime, to simulate a drop.
	dropAfterSubscribe int32
	subscribeCount     int32
	upgrader           websocket.Upgrader
}

func newWSTestServer(t *testing.T) (*Client, *wsTestServer) {
	t.Helper()
	srv := &wsTestServer{t: t}
	httpSrv := httptest.NewServer(http.HandlerFunc(srv.handle))
	t.Cleanup(httpSrv.Close)

	c, err := NewClient(WithURL(httpSrv.URL), WithAPIKey("ws-key"))
	if err != nil {
		t.Fatal(err)
	}
	return c, srv
}

// wsClientMessage is the graphql-transport-ws envelope the server reads/writes.
type wsClientMessage struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func (s *wsTestServer) handle(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.t.Logf("upgrade: %v", err)
		return
	}
	defer func() { _ = conn.Close() }()

	var writeMu sync.Mutex
	send := func(msgType, payload string) {
		writeMu.Lock()
		defer writeMu.Unlock()
		var raw json.RawMessage
		if payload != "" {
			raw = json.RawMessage(payload)
		}
		msg := wsClientMessage{Type: msgType, Payload: raw}
		_ = conn.WriteJSON(msg)
	}
	sendForSub := func(id, msgType, payload string) {
		writeMu.Lock()
		defer writeMu.Unlock()
		var raw json.RawMessage
		if payload != "" {
			raw = json.RawMessage(payload)
		}
		_ = conn.WriteJSON(wsClientMessage{Type: msgType, ID: id, Payload: raw})
	}

	for {
		var msg wsClientMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg.Type {
		case "connection_init":
			var params map[string]any
			if len(msg.Payload) > 0 {
				_ = json.Unmarshal(msg.Payload, &params)
			}
			s.connInitParams.Store(params)
			writeMu.Lock()
			_ = conn.WriteJSON(wsClientMessage{Type: "connection_ack"})
			writeMu.Unlock()
		case "subscribe":
			n := atomic.AddInt32(&s.subscribeCount, 1)
			if s.dropAfterSubscribe > 0 && n <= s.dropAfterSubscribe {
				// Simulate an abrupt mid-stream drop: hang up without a complete.
				_ = conn.Close()
				return
			}
			if s.onSubscribe != nil {
				id := msg.ID
				s.onSubscribe(func(mt, pl string) { sendForSub(id, mt, pl) }, id)
			}
		case "complete":
			// client unsubscribed
		case "ping":
			send("pong", "")
		}
	}
}

// --- Test (a): all three subscription response types stream through.

func TestSubscriptionStreamsAllThreeTypes(t *testing.T) {
	c, srv := newWSTestServer(t)
	srv.onSubscribe = func(send func(msgType, payload string), id string) {
		// The query text tells us which subscription this is; emit a matching
		// next frame then complete.
		go func() {
			send("next", `{"data":{"jobsSubscribe":{"type":"ADD","job":{"id":"j1","status":"RUNNING","subTasks":null,"description":"scan","progress":0.5,"startTime":null,"endTime":null,"addTime":"2025-01-01T00:00:00Z","error":null}}}}`)
			send("next", `{"data":{"loggingSubscribe":[{"time":"2025-01-01T00:00:00Z","level":"Info","message":"hello"}]}}`)
			send("next", `{"data":{"scanCompleteSubscribe":true}}`)
			send("complete", "")
		}()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsClient, errChan, err := c.Subscriptions(ctx)
	if err != nil {
		t.Fatalf("Subscriptions: %v", err)
	}
	defer func() { _ = wsClient.Close() }()

	// Confirm the ApiKey rode in on connection_init.
	if params, _ := srv.connInitParams.Load().(map[string]any); params == nil || params["ApiKey"] != "ws-key" {
		t.Errorf("connection_init params = %v, want ApiKey=ws-key", params)
	}

	jobsCh, _, err := JobsSubscribe(ctx, wsClient)
	if err != nil {
		t.Fatalf("JobsSubscribe: %v", err)
	}
	gotJob := false
	for resp := range jobsCh {
		if len(resp.Errors) > 0 {
			t.Fatalf("jobs errors: %v", resp.Errors)
		}
		if resp.Data != nil && resp.Data.JobsSubscribe != nil && resp.Data.JobsSubscribe.Job != nil {
			if resp.Data.JobsSubscribe.Job.Id != "j1" {
				t.Errorf("job id = %q, want j1", resp.Data.JobsSubscribe.Job.Id)
			}
			gotJob = true
		}
	}
	if !gotJob {
		t.Error("did not receive a JobsSubscribe event")
	}

	// loggingSubscribe on a fresh connection (genqlient closes the chan on
	// complete; reuse the same client which is still open).
	logCh, _, err := LoggingSubscribe(ctx, wsClient)
	if err != nil {
		t.Fatalf("LoggingSubscribe: %v", err)
	}
	gotLog := false
	for resp := range logCh {
		if resp.Data != nil && len(resp.Data.LoggingSubscribe) > 0 {
			if resp.Data.LoggingSubscribe[0].Message != "hello" {
				t.Errorf("log message = %q, want hello", resp.Data.LoggingSubscribe[0].Message)
			}
			gotLog = true
		}
	}
	if !gotLog {
		t.Error("did not receive a LoggingSubscribe event")
	}

	scanCh, _, err := ScanCompleteSubscribe(ctx, wsClient)
	if err != nil {
		t.Fatalf("ScanCompleteSubscribe: %v", err)
	}
	gotScan := false
	for resp := range scanCh {
		if resp.Data != nil && resp.Data.ScanCompleteSubscribe {
			gotScan = true
		}
	}
	if !gotScan {
		t.Error("did not receive a ScanCompleteSubscribe event")
	}

	// errChan should not have produced an error during normal operation.
	select {
	case err := <-errChan:
		if err != nil {
			t.Errorf("unexpected errChan error: %v", err)
		}
	default:
	}
}

// --- Fake WSConn / Dialer for hermetic, network-free timing & concurrency.

// fakeConn is an in-memory WSConn. Reads come from an incoming channel; writes
// are recorded (and classified by gorilla message type) so a test can assert
// keepalive pings and serialisation without any network.
type fakeConn struct {
	incoming  chan fakeFrame
	mu        sync.Mutex
	writes    []fakeFrame
	closed    bool
	closeCh   chan struct{}
	deadlines int
}

type fakeFrame struct {
	msgType int
	data    []byte
}

func newFakeConn() *fakeConn {
	return &fakeConn{
		incoming: make(chan fakeFrame, 64),
		closeCh:  make(chan struct{}),
	}
}

func (f *fakeConn) SetWriteDeadline(time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deadlines++
	return nil
}

func (f *fakeConn) WriteMessage(messageType int, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return errors.New("write on closed conn")
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	f.writes = append(f.writes, fakeFrame{msgType: messageType, data: cp})
	return nil
}

func (f *fakeConn) ReadMessage() (int, []byte, error) {
	select {
	case fr := <-f.incoming:
		return fr.msgType, fr.data, nil
	case <-f.closeCh:
		return 0, nil, errors.New("conn closed")
	}
}

func (f *fakeConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.closeCh)
	}
	return nil
}

func (f *fakeConn) pingCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, w := range f.writes {
		if w.msgType == websocket.PingMessage {
			n++
		}
	}
	return n
}

func (f *fakeConn) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *fakeConn) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.writes)
}

// --- Test (c): keepalive fires on the interval, driven by synctest.

func TestSubscriptionKeepaliveFiresOnInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fc := newFakeConn()
		const interval = 25 * time.Second
		sc := newSerialConn(fc, interval, nil)
		defer func() { _ = sc.Close() }()

		// No pings before the first tick.
		synctest.Wait()
		if got := fc.pingCount(); got != 0 {
			t.Fatalf("pings before first interval = %d, want 0", got)
		}

		// Advance three intervals; expect three pings, each a gorilla ping frame.
		for i := 1; i <= 3; i++ {
			time.Sleep(interval)
			synctest.Wait()
			if got := fc.pingCount(); got != i {
				t.Fatalf("after %d intervals: pings = %d, want %d", i, got, i)
			}
		}
	})
}

// --- Test (b): concurrent keepalive + protocol writes are serialised, race-clean.

func TestSubscriptionWritesSerialised(t *testing.T) {
	fc := newFakeConn()
	// A short interval so the keepalive writer competes with protocol writes.
	sc := newSerialConn(fc, time.Millisecond, nil)
	defer func() { _ = sc.Close() }()

	var wg sync.WaitGroup
	const writers = 8
	const each = 200
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if err := sc.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("w%d-%d", w, i))); err != nil {
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// Give the serial writer a moment to drain the queue.
	deadline := time.Now().Add(2 * time.Second)
	want := writers * each
	for time.Now().Before(deadline) {
		text := 0
		fc.mu.Lock()
		for _, fr := range fc.writes {
			if fr.msgType == websocket.TextMessage {
				text++
			}
		}
		fc.mu.Unlock()
		if text >= want {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	text := 0
	fc.mu.Lock()
	for _, fr := range fc.writes {
		if fr.msgType == websocket.TextMessage {
			text++
		}
	}
	fc.mu.Unlock()
	if text != want {
		t.Errorf("text writes serialised = %d, want %d", text, want)
	}
	if fc.writeCount() < want {
		t.Errorf("total writes = %d, want at least %d", fc.writeCount(), want)
	}
}

// --- Test: Close stops the writer cleanly and subsequent writes fail.

func TestSubscriptionCloseStopsWriter(t *testing.T) {
	fc := newFakeConn()
	sc := newSerialConn(fc, time.Hour, nil)
	if err := sc.WriteMessage(websocket.TextMessage, []byte("before")); err != nil {
		t.Fatalf("write before close: %v", err)
	}
	if err := sc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := sc.WriteMessage(websocket.TextMessage, []byte("after")); err == nil {
		t.Error("write after close should fail")
	}
}

// --- Test (d): a mid-stream drop triggers bounded reconnect + resubscribe,
// and exhausting the bound surfaces a terminal error.

func TestSubscriptionReconnectsThenSucceeds(t *testing.T) {
	c, srv := newWSTestServer(t)
	// Drop the very first subscribe; the second (after reconnect) succeeds.
	srv.dropAfterSubscribe = 1
	srv.onSubscribe = func(send func(msgType, payload string), id string) {
		go func() {
			send("next", `{"data":{"scanCompleteSubscribe":true}}`)
			// Keep the subscription open so the stream stays alive.
		}()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	events, errCh := Subscribe(ctx, c, func(ctx context.Context, wc graphql.WebSocketClient) (<-chan bool, error) {
		ch, _, err := ScanCompleteSubscribe(ctx, wc)
		if err != nil {
			return nil, err
		}
		out := make(chan bool)
		go func() {
			defer close(out)
			for resp := range ch {
				if resp.Data != nil {
					out <- resp.Data.ScanCompleteSubscribe
				}
			}
		}()
		return out, nil
	}, WithMaxReconnects(5), WithBackoff(time.Millisecond, 20*time.Millisecond))

	select {
	case v := <-events:
		if !v {
			t.Errorf("event = %v, want true", v)
		}
	case err := <-errCh:
		t.Fatalf("unexpected terminal error before any event: %v", err)
	case <-ctx.Done():
		t.Fatal("timed out waiting for a reconnected event")
	}

	// At least two subscribe frames means a reconnect happened.
	if n := atomic.LoadInt32(&srv.subscribeCount); n < 2 {
		t.Errorf("subscribe count = %d, want >= 2 (a reconnect)", n)
	}
}

func TestSubscriptionExhaustsBoundAndReportsTerminal(t *testing.T) {
	c, srv := newWSTestServer(t)
	// Drop every subscribe; reconnect can never establish a stream.
	srv.dropAfterSubscribe = 1000
	srv.onSubscribe = func(send func(msgType, payload string), id string) {}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	events, errCh := Subscribe(ctx, c, func(ctx context.Context, wc graphql.WebSocketClient) (<-chan bool, error) {
		ch, _, err := ScanCompleteSubscribe(ctx, wc)
		if err != nil {
			return nil, err
		}
		out := make(chan bool)
		go func() {
			defer close(out)
			for resp := range ch {
				if resp.Data != nil {
					out <- resp.Data.ScanCompleteSubscribe
				}
			}
		}()
		return out, nil
	}, WithMaxReconnects(3), WithBackoff(time.Millisecond, 10*time.Millisecond))

	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("got an event, want a terminal error after the bound is exhausted")
		}
	case err := <-errCh:
		if err == nil {
			t.Fatal("terminal error channel closed without an error")
		}
		if !strings.Contains(err.Error(), "reconnect") {
			t.Errorf("terminal error = %v, want one mentioning reconnect exhaustion", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out; bound was never declared exhausted")
	}
}

func TestSubscriptionStopsOnContextCancel(t *testing.T) {
	c, srv := newWSTestServer(t)
	srv.onSubscribe = func(send func(msgType, payload string), id string) {
		// Never emit; just keep the subscription open.
	}

	ctx, cancel := context.WithCancel(context.Background())
	events, errCh := Subscribe(ctx, c, func(ctx context.Context, wc graphql.WebSocketClient) (<-chan bool, error) {
		ch, _, err := ScanCompleteSubscribe(ctx, wc)
		if err != nil {
			return nil, err
		}
		out := make(chan bool)
		go func() {
			defer close(out)
			for resp := range ch {
				if resp.Data != nil {
					out <- resp.Data.ScanCompleteSubscribe
				}
			}
		}()
		return out, nil
	})

	// Cancel and expect both channels to close without a terminal error.
	cancel()
	timeout := time.After(5 * time.Second)
	for events != nil || errCh != nil {
		select {
		case _, ok := <-events:
			if !ok {
				events = nil
			}
		case err, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("error on cancel = %v, want nil or context.Canceled", err)
			}
		case <-timeout:
			t.Fatal("Subscribe did not stop on context cancel")
		}
	}
}

// --- Finding A: a write deadline keeps the single writer from wedging forever
// on a half-open connection.

// blockingConn models a half-open socket: WriteMessage blocks until a write
// deadline is armed, then returns a timeout error (gorilla's contract: a
// deadline already in the past aborts the blocked write). Without the deadline
// the write would block forever and the writer goroutine would never return to
// honour stop, so Close would hang.
type blockingConn struct {
	mu          sync.Mutex
	deadlineSet bool
	armed       chan struct{} // closed once a deadline is first armed
	closeCh     chan struct{}
}

func newBlockingConn() *blockingConn {
	return &blockingConn{armed: make(chan struct{}), closeCh: make(chan struct{})}
}

func (b *blockingConn) SetWriteDeadline(time.Time) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.deadlineSet {
		b.deadlineSet = true
		close(b.armed)
	}
	return nil
}

func (b *blockingConn) WriteMessage(int, []byte) error {
	b.mu.Lock()
	armed := b.deadlineSet
	b.mu.Unlock()
	if armed {
		// A deadline is in force; the half-open write times out rather than
		// blocking, exactly as gorilla reports it.
		return errors.New("i/o timeout")
	}
	// No deadline: the kernel send buffer is full and the write blocks until the
	// connection is torn down. A correct writer must never reach a state where it
	// is parked here with no deadline ever applied.
	<-b.closeCh
	return errors.New("conn closed")
}

func (b *blockingConn) ReadMessage() (int, []byte, error) {
	<-b.closeCh
	return 0, nil, errors.New("conn closed")
}

func (b *blockingConn) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	select {
	case <-b.closeCh:
	default:
		close(b.closeCh)
	}
	return nil
}

func TestSerialConnWriteDeadlinePreventsWedge(t *testing.T) {
	bc := newBlockingConn()
	sc := newSerialConn(bc, time.Hour, nil) // keepalive parked; drive writes ourselves

	// Issue a write. Because writeLoop arms a deadline before each WriteMessage,
	// the half-open write returns a timeout instead of blocking the writer.
	werr := make(chan error, 1)
	go func() { werr <- sc.WriteMessage(websocket.TextMessage, []byte("frame")) }()

	select {
	case <-bc.armed:
		// Good: a deadline was set before the write, so the writer cannot wedge.
	case <-time.After(2 * time.Second):
		t.Fatal("no write deadline was armed; the writer wrote with an unbounded deadline")
	}

	select {
	case err := <-werr:
		if err == nil {
			t.Fatal("expected the deadline-bounded write to return an error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WriteMessage never returned; the writer wedged despite the deadline")
	}

	// The decisive property: Close completes (writeLoop honoured stop and exited)
	// rather than blocking forever on <-sc.done.
	done := make(chan error, 1)
	go func() { done <- sc.Close() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked: the writer goroutine wedged on an unbounded write")
	}
}

// --- Finding B: the serialConn writer + ticker are released even when
// genqlient's graceful Close fails to send the close frame.

// fakeWSClient stands in for genqlient's webSocketClient. Its Close mirrors the
// v0.8.1 bug: a failed close-frame write returns early without tearing down the
// inner connection, so the serialConn backstop must do it.
type fakeWSClient struct {
	closeErr error
	errChan  chan error
}

func (f *fakeWSClient) Start(context.Context) (chan error, error) { return f.errChan, nil }

func (f *fakeWSClient) Close() error {
	if f.closeErr != nil {
		// Genqlient returns here on a close-frame write error and never closes
		// the inner conn or the errChan.
		return f.closeErr
	}
	close(f.errChan)
	return nil
}

func (f *fakeWSClient) Subscribe(*graphql.Request, any, graphql.ForwardDataFunction) (string, error) {
	return "", nil
}
func (f *fakeWSClient) Unsubscribe(string) error { return nil }

func TestCleanupSubscriptionBacksStopWhenCloseFails(t *testing.T) {
	fc := newFakeConn()
	ticker := time.NewTicker(time.Hour)
	sc := newSerialConn(fc, time.Hour, func(time.Duration) *time.Ticker { return ticker })

	wsc := &fakeWSClient{closeErr: errors.New("failed to send closure message"), errChan: make(chan error)}

	cleanupSubscription(wsc, sc, wsc.errChan)

	// The backstop must have stopped the writer goroutine: done is closed, so a
	// further write reports the closed connection.
	select {
	case <-sc.done:
	case <-time.After(2 * time.Second):
		t.Fatal("writeLoop goroutine was not released after cleanup")
	}
	if err := sc.WriteMessage(websocket.TextMessage, []byte("x")); err == nil {
		t.Error("write after cleanup should fail; the writer was not stopped")
	}
	if !fc.isClosed() {
		t.Error("underlying conn was not closed by the backstop")
	}
}

// --- Finding C: cleanup does not deadlock when a read-error handleErr send is
// in flight on the unbuffered errChan as the close path runs.

// lockingWSClient faithfully reproduces genqlient v0.8.1's deadlock shape: a
// transport read error is reported by handleErr, which holds the client mutex
// while sending on the unbuffered errChan, and Close must take that same mutex.
// If the errChan is never drained, handleErr is parked under the lock and Close
// blocks forever acquiring it.
type lockingWSClient struct {
	mu      sync.Mutex
	errChan chan error
	closing bool
}

func (c *lockingWSClient) Start(context.Context) (chan error, error) { return c.errChan, nil }

// handleErr mirrors genqlient: send under the lock on the unbuffered errChan.
func (c *lockingWSClient) handleErr(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closing {
		c.errChan <- err
	}
}

func (c *lockingWSClient) Close() error {
	// Genqlient's Close on a dead socket: the close-frame write fails first and
	// it returns early, but it still must take the mutex to do so under the bug's
	// fixed variant; faithfully, Close contends for the same lock handleErr holds.
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closing = true
	close(c.errChan)
	return nil
}

func (c *lockingWSClient) Subscribe(*graphql.Request, any, graphql.ForwardDataFunction) (string, error) {
	return "", nil
}
func (c *lockingWSClient) Unsubscribe(string) error { return nil }

func TestCleanupSubscriptionDrainsInFlightHandleErr(t *testing.T) {
	fc := newFakeConn()
	sc := newSerialConn(fc, time.Hour, nil)

	// errChan is unbuffered, mirroring genqlient.
	wsc := &lockingWSClient{errChan: make(chan error)}

	// A read error is reported under the lock just as cleanup begins. handleErr
	// parks on the unbuffered send while holding the mutex; only a drainer can
	// release it, after which Close can take the mutex.
	go wsc.handleErr(errors.New("transport read error"))

	// Give handleErr a moment to acquire the lock and park on the send, so the
	// race the finding describes is actually set up before cleanup runs.
	time.Sleep(20 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		cleanupSubscription(wsc, sc, wsc.errChan)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanupSubscription deadlocked: Close could not take the mutex handleErr held")
	}
}
