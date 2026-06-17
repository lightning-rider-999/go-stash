package stash

import (
	"context"
	"errors"
	"fmt"
	"math/bits"
	"net/http"
	"sync"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/gorilla/websocket"
)

// defaultKeepaliveInterval is how often the wrapper sends a client-side ping on
// an otherwise idle subscription socket. Stash sends no server-initiated pings
// on graphql-transport-ws, so without this an idle socket can be dropped by an
// intermediary. The value is well under typical idle timeouts.
const defaultKeepaliveInterval = 25 * time.Second

// defaultMaxReconnects bounds how many times [Subscribe] re-establishes a
// dropped connection before reporting a terminal failure.
const defaultMaxReconnects = 10

// defaultBackoffBase and defaultBackoffMax bound the exponential backoff between
// reconnect attempts.
const (
	defaultBackoffBase = 500 * time.Millisecond
	defaultBackoffMax  = 30 * time.Second
)

// writeWait bounds every underlying WriteMessage with a deadline. gorilla's
// default write deadline is zero (never), so on a half-open TCP connection the
// kernel send buffer fills and WriteMessage blocks forever, wedging the single
// writer goroutine. A bounded deadline turns that into an error the loop can
// surface, keeping the writer responsive to its stop signal.
const writeWait = 10 * time.Second

// wsConn is the write side of a WebSocket connection with a settable deadline.
// genqlient's [graphql.WSConn] omits SetWriteDeadline, but the real gorilla
// *websocket.Conn provides it, so the wrapper holds this richer interface to
// bound each write.
type wsConn interface {
	graphql.WSConn
	SetWriteDeadline(time.Time) error
}

// serialConn wraps a [graphql.WSConn] (in practice a gorilla *websocket.Conn) to
// satisfy two requirements gorilla imposes and genqlient does not handle:
//
//   - Writes are serialised. gorilla forbids concurrent WriteMessage, yet
//     genqlient writes the connection_init, every subscribe frame, and the close
//     frame from different code paths, and the keepalive adds another writer. A
//     single dedicated goroutine owns the underlying write side; WriteMessage
//     enqueues a request and waits for the result.
//   - Keepalive is client-driven. A ticker periodically enqueues a gorilla ping
//     control frame through the same serialised writer. Gorilla answers a peer's
//     ping and absorbs the peer's pong at the protocol layer, so these frames
//     never surface to genqlient's ReadMessage.
//
// ReadMessage and Close delegate to the wrapped connection; Close also stops the
// writer goroutine and the keepalive ticker.
type serialConn struct {
	conn wsConn

	writes   chan writeRequest
	stop     chan struct{}
	done     chan struct{}
	interval time.Duration

	// stopOnce guards the close(stop) in Close so concurrent callers cannot
	// double-close the channel and panic.
	stopOnce sync.Once

	// newTicker builds the keepalive ticker. It is a field so a test can drive
	// timing; production uses time.NewTicker. It is never nil after construction.
	newTicker func(time.Duration) *time.Ticker
}

// writeRequest carries one frame to the writer goroutine and a channel for its
// result, so WriteMessage can report the underlying write error to its caller.
type writeRequest struct {
	msgType int
	data    []byte
	result  chan error
}

// errConnClosed is returned by WriteMessage once the wrapper has been closed.
var errConnClosed = errors.New("stash: websocket connection closed")

// newSerialConn wraps conn with a serialised writer and a keepalive ticker that
// fires every interval. A non-positive interval disables keepalive. newTicker
// may be nil, in which case time.NewTicker is used; a test can inject a ticker
// to drive the keepalive timing deterministically.
func newSerialConn(conn wsConn, interval time.Duration, newTicker func(time.Duration) *time.Ticker) *serialConn {
	if newTicker == nil {
		newTicker = time.NewTicker
	}
	sc := &serialConn{
		conn:      conn,
		writes:    make(chan writeRequest),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
		interval:  interval,
		newTicker: newTicker,
	}
	go sc.writeLoop()
	return sc
}

// writeLoop owns the underlying connection's write side. It serves write
// requests one at a time and, on the keepalive interval, sends a ping. It exits
// when stop is closed, then closes done.
func (sc *serialConn) writeLoop() {
	defer close(sc.done)

	var tickC <-chan time.Time
	if sc.interval > 0 {
		ticker := sc.newTicker(sc.interval)
		defer ticker.Stop()
		tickC = ticker.C
	}

	for {
		select {
		case <-sc.stop:
			return
		case req := <-sc.writes:
			req.result <- sc.write(req.msgType, req.data)
		case <-tickC:
			// A failed keepalive ping is not fatal here: the next ReadMessage
			// will observe the broken connection and surface the error through
			// genqlient's errChan. Ignoring it keeps the writer alive to serve
			// the close frame.
			_ = sc.write(websocket.PingMessage, nil)
		}
	}
}

// write performs one underlying write under a bounded deadline. The deadline
// caps how long a write can block on a half-open connection, so the writer
// goroutine stays responsive to stop rather than wedging forever. A
// SetWriteDeadline failure is surfaced as the write result; if it cannot be
// set, the write is attempted anyway so behaviour degrades to the prior
// (unbounded) path rather than dropping the frame silently.
func (sc *serialConn) write(msgType int, data []byte) error {
	if err := sc.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
		return err
	}
	return sc.conn.WriteMessage(msgType, data)
}

// WriteMessage enqueues a frame for the serialised writer and returns the
// underlying write result. After Close it returns [errConnClosed].
func (sc *serialConn) WriteMessage(messageType int, data []byte) error {
	req := writeRequest{msgType: messageType, data: data, result: make(chan error, 1)}
	select {
	case sc.writes <- req:
		return <-req.result
	case <-sc.stop:
		return errConnClosed
	}
}

// ReadMessage delegates to the wrapped connection. gorilla permits one
// concurrent reader alongside the writer, which is exactly genqlient's usage.
func (sc *serialConn) ReadMessage() (messageType int, p []byte, err error) {
	return sc.conn.ReadMessage()
}

// Close stops the writer goroutine and keepalive ticker, waits for the writer to
// finish any in-flight frame, then closes the underlying connection. It is safe
// to call more than once and from multiple goroutines concurrently: stopOnce
// ensures the stop channel is closed exactly once, so a racing second caller
// cannot double-close it and panic. Both callers then wait on done and call the
// underlying Close, which is itself idempotent on a gorilla *websocket.Conn.
func (sc *serialConn) Close() error {
	sc.stopOnce.Do(func() { close(sc.stop) })
	<-sc.done
	return sc.conn.Close()
}

// wsDialer adapts gorilla/websocket to genqlient's [graphql.Dialer]. Each dial
// returns a [serialConn] wrapping the gorilla connection, so writes are
// serialised and keepalive pings are sent on the configured interval. It also
// records the most recently built [serialConn] so a caller can close it
// directly as a backstop when genqlient's own Close fails to tear it down.
type wsDialer struct {
	dialer    *websocket.Dialer
	interval  time.Duration
	newTicker func(time.Duration) *time.Ticker

	// last is the serialConn produced by the most recent successful dial. A
	// genqlient WebSocket client dials exactly once per Start, so after Start
	// returns this is the connection that client owns.
	last *serialConn
}

// DialContext dials urlStr with the graphql-transport-ws subprotocol and wraps
// the result. genqlient sets the Sec-WebSocket-Protocol header on requestHeader;
// the gorilla dialer also advertises the subprotocol via Subprotocols.
func (d *wsDialer) DialContext(ctx context.Context, urlStr string, requestHeader http.Header) (graphql.WSConn, error) {
	conn, resp, err := d.dialer.DialContext(ctx, urlStr, requestHeader)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("stash: dialing websocket %q: %w", urlStr, err)
	}
	sc := newSerialConn(conn, d.interval, d.newTicker)
	d.last = sc
	return sc, nil
}

// Subscriptions opens a subscription connection to the server and returns a
// ready [graphql.WebSocketClient] together with the error channel that reports
// connection-level failures.
//
// The handshake (connection_init carrying the ApiKey, then connection_ack) has
// completed by the time this returns. Pass the client to the generated
// subscription functions, for example stash.JobsSubscribe(ctx, wsClient).
//
// The caller owns the returned client and must Close it to release the
// connection; doing so also closes the error channel. For automatic reconnect
// on a mid-stream drop, prefer the [Subscribe] helper, which manages the client
// lifecycle itself.
func (c *Client) Subscriptions(ctx context.Context) (graphql.WebSocketClient, <-chan error, error) {
	wsClient, _ := c.newSubscriptionClient(defaultKeepaliveInterval, nil)
	errChan, err := wsClient.Start(ctx)
	if err != nil {
		return nil, nil, classifyWS(err)
	}
	return wsClient, errChan, nil
}

// newSubscriptionClient builds a genqlient WebSocket client whose dialer wraps
// gorilla with the serialised writer and keepalive. The ApiKey, when set, is
// sent in the connection_init payload under the "ApiKey" key, matching the HTTP
// header name Stash also accepts. newTicker may be nil for the real clock.
//
// The dialer is returned alongside the client so a caller that manages the
// client lifecycle ([Subscribe] via runSubscription) can reach the serialConn
// the dialer builds and close it directly as a backstop. Callers that hand the
// client to an external owner can ignore it.
func (c *Client) newSubscriptionClient(interval time.Duration, newTicker func(time.Duration) *time.Ticker) (graphql.WebSocketClient, *wsDialer) {
	dialer := &wsDialer{
		dialer:    websocket.DefaultDialer,
		interval:  interval,
		newTicker: newTicker,
	}

	var opts []graphql.WebSocketOption
	if key := c.APIKey(); key != "" {
		opts = append(opts, graphql.WithConnectionParams(map[string]any{"ApiKey": key}))
	}
	return graphql.NewClientUsingWebSocket(c.WebSocketURL(), dialer, opts...), dialer
}

// classifyWS maps a WebSocket handshake or connection error into the package
// transport error model. genqlient's WebSocket path does not produce the
// gqlerror.List or *graphql.HTTPError shapes classify recognises, so a raw
// dial/handshake failure becomes a [*TransportError].
func classifyWS(err error) error {
	if err == nil {
		return nil
	}
	return &TransportError{err: err}
}

// subOptions configures [Subscribe]'s reconnect policy and keepalive.
type subOptions struct {
	maxReconnects int
	backoffBase   time.Duration
	backoffMax    time.Duration
	interval      time.Duration
	newTicker     func(time.Duration) *time.Ticker
}

// defaultSubOptions returns the reconnect policy used when no options override
// it.
func defaultSubOptions() subOptions {
	return subOptions{
		maxReconnects: defaultMaxReconnects,
		backoffBase:   defaultBackoffBase,
		backoffMax:    defaultBackoffMax,
		interval:      defaultKeepaliveInterval,
	}
}

// SubOption configures the behaviour of [Subscribe].
type SubOption func(*subOptions)

// WithMaxReconnects caps how many times [Subscribe] re-establishes a dropped
// connection before reporting a terminal failure. A value of zero or less means
// never reconnect: the first drop is terminal.
func WithMaxReconnects(n int) SubOption {
	return func(o *subOptions) { o.maxReconnects = n }
}

// WithBackoff sets the exponential backoff bounds between reconnect attempts.
// The delay starts at base, doubles each attempt, and is capped at maxDelay.
// Non-positive values fall back to the defaults.
func WithBackoff(base, maxDelay time.Duration) SubOption {
	return func(o *subOptions) {
		if base > 0 {
			o.backoffBase = base
		}
		if maxDelay > 0 {
			o.backoffMax = maxDelay
		}
	}
}

// WithKeepaliveInterval overrides the client-side keepalive interval used for a
// [Subscribe] stream. A non-positive interval disables keepalive.
func WithKeepaliveInterval(d time.Duration) SubOption {
	return func(o *subOptions) { o.interval = d }
}

// Subscribe streams typed events from a subscription and transparently
// reconnects on a connection drop, within a bounded backoff.
//
// subscribe is called with a ready [graphql.WebSocketClient] for each
// connection (the initial one and every reconnect) and must return a channel of
// typed events, for example by wrapping the generated stash.JobsSubscribe. The
// returned events channel forwards those events; the returned error channel
// reports a single terminal condition and is then closed alongside the events
// channel:
//
//   - a clean stop when ctx is cancelled (no error, or context.Canceled),
//   - an unrecoverable failure once the reconnect bound is exhausted (an error
//     mentioning reconnect exhaustion, wrapping the last cause).
//
// A reconnect is attempted whenever the per-connection event channel closes
// before ctx is done, which is how both a server-side complete and a transport
// drop appear. The backoff between attempts grows exponentially within the
// configured bounds; a successful event resets the attempt counter so a
// long-lived stream that drops occasionally is not starved.
func Subscribe[T any](
	ctx context.Context,
	c *Client,
	subscribe func(ctx context.Context, wc graphql.WebSocketClient) (<-chan T, error),
	opts ...SubOption,
) (<-chan T, <-chan error) {
	o := defaultSubOptions()
	for _, opt := range opts {
		opt(&o)
	}

	events := make(chan T)
	errCh := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errCh)

		attempt := 0
		var lastErr error

		for {
			if ctx.Err() != nil {
				return
			}

			delivered, runErr := runSubscription(ctx, c, subscribe, o, events)
			if delivered {
				// Progress was made; reset the reconnect budget.
				attempt = 0
			}
			if ctx.Err() != nil {
				return
			}
			if runErr != nil {
				lastErr = runErr
			}

			attempt++
			if attempt > o.maxReconnects {
				errCh <- fmt.Errorf("stash: subscription terminated after %d reconnect attempts: %w", o.maxReconnects, terminalCause(lastErr))
				return
			}

			if !sleepBackoff(ctx, o, attempt) {
				return
			}
		}
	}()

	return events, errCh
}

// runSubscription establishes one connection, forwards its events to out, and
// returns whether any event was delivered and the connection-level error, if
// any, observed when the stream ended. It always closes its connection before
// returning.
func runSubscription[T any](
	ctx context.Context,
	c *Client,
	subscribe func(ctx context.Context, wc graphql.WebSocketClient) (<-chan T, error),
	o subOptions,
	out chan<- T,
) (delivered bool, err error) {
	wsClient, dialer := c.newSubscriptionClient(o.interval, o.newTicker)
	errChan, startErr := wsClient.Start(ctx)
	if startErr != nil {
		return false, classifyWS(startErr)
	}

	stream, subErr := subscribe(ctx, wsClient)
	if subErr != nil {
		// No stream was established, so there is nothing feeding it to drain.
		cleanupSubscription[T](wsClient, dialer.last, errChan, nil)
		return false, subErr
	}
	// Hand the stream to cleanup so a teardown that races a pending event can
	// drain it. The defer runs only after this loop has stopped consuming stream,
	// so cleanup is the sole reader and steals no events from delivery.
	defer cleanupSubscription(wsClient, dialer.last, errChan, stream)

	for {
		select {
		case <-ctx.Done():
			return delivered, ctx.Err()
		case connErr, ok := <-errChan:
			// A connection-level error ends this stream; the loop reconnects. A
			// closed errChan (ok is false) is genqlient signalling end-of-stream
			// after its own Close, which is equivalent to a drop with no error.
			if !ok {
				return delivered, nil
			}
			if connErr != nil {
				return delivered, connErr
			}
		case ev, ok := <-stream:
			if !ok {
				// The per-connection stream closed: a complete or a drop. Return
				// so the caller decides whether to reconnect.
				return delivered, nil
			}
			select {
			case out <- ev:
				delivered = true
			case <-ctx.Done():
				return delivered, ctx.Err()
			}
		}
	}
}

// cleanupWindow bounds how long cleanupSubscription lets its drainers run before
// giving up and returning. It caps the teardown cost per reconnect. It only has
// to outlast the moment between sc.Close releasing a parked send and the read
// goroutine reaching its next ReadMessage, which is near-instant; the window is
// the fail-safe ceiling for the dead-socket path where a channel is never
// closed, not the expected wait.
const cleanupWindow = 250 * time.Millisecond

// cleanupSubscription tears down one subscription connection, defending against
// three genqlient v0.8.1 behaviours observed under a dead or half-open socket:
//
//   - genqlient's Close sends the close frame first and, on a write error,
//     returns early without calling the inner connection's Close. That inner
//     Close is the sole path that stops the serialConn writer goroutine and
//     keepalive ticker, so a failed close frame would otherwise leak both on
//     every reconnect. sc.Close is idempotent and always run as a backstop.
//   - genqlient's read goroutine reports a transport error by sending on the
//     unbuffered errChan while holding genqlient's mutex. If runSubscription's
//     select returns via ctx/stream-close in the same tick that a read error
//     fires, that send blocks forever and genqlient's Close deadlocks acquiring
//     the same mutex. A concurrent drainer lets the pending send complete so
//     Close can proceed.
//   - on ctx-cancel racing a pending event, the read goroutine is instead parked
//     on a blocking send into the unbuffered per-subscription data channel that
//     ultimately feeds stream. Closing the socket does not release that send and
//     nothing is reading stream once runSubscription's loop has returned, so the
//     read goroutine never reaches its next ReadMessage and leaks. Draining
//     stream lets the in-flight forwardDataFunc send complete so the goroutine
//     can exit.
//
// Both drainers are bounded by a single one-shot timer: they must terminate when
// the window elapses even if neither channel is ever closed. That is the
// dead-socket/reconnect path, where genqlient closes errChan only after a
// successful close-frame write — which never happens — so a drainer that waited
// for closure alone would leak for the life of the process. sc may be nil only
// if no connection was dialed (no backstop needed) or no stream was established.
func cleanupSubscription[T any](wsClient graphql.WebSocketClient, sc *serialConn, errChan <-chan error, stream <-chan T) {
	// One shared deadline for every drainer, broadcast by closing stopDrain. A
	// time.Timer's own channel delivers its tick to exactly one receiver, so two
	// drainers cannot both select on it — the second would block on that branch
	// forever on a dead socket where its data channel is never closed. Closing a
	// channel, by contrast, fans the deadline out to every drainer at once. A
	// NewTimer with a deferred Stop also avoids leaking the one-shot timer
	// time.After would orphan for the whole window on each of the many teardowns a
	// long-lived stream performs.
	timer := time.NewTimer(cleanupWindow)
	defer timer.Stop()
	stopDrain := make(chan struct{})
	var stopOnce sync.Once
	closeStop := func() { stopOnce.Do(func() { close(stopDrain) }) }
	// Fire the deadline into stopDrain when the timer elapses. The watcher itself
	// is released by closeStop (called once the drainers finish), so it never
	// outlives the cleanup.
	go func() {
		select {
		case <-timer.C:
			closeStop()
		case <-stopDrain:
		}
	}()

	var wg sync.WaitGroup

	// Drain errChan so a handleErr send blocked under genqlient's mutex can
	// complete, letting Close acquire that mutex. The select on stopDrain is the
	// release condition that does not depend on errChan ever being closed: when
	// the close-frame write fails genqlient never closes errChan, so a drainer
	// waiting only for closure would leak for the life of the process.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case _, ok := <-errChan:
				if !ok {
					return
				}
			case <-stopDrain:
				return
			}
		}
	}()

	// Drain stream so a forwardDataFunc send parked on the unbuffered data channel
	// can complete, releasing the read goroutine to reach its next ReadMessage.
	// Same deadline-bounded release: a dropped socket never closes the data
	// channel feeding stream, so the drainer must not depend on closure.
	if stream != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case _, ok := <-stream:
					if !ok {
						return
					}
				case <-stopDrain:
					return
				}
			}
		}()
	}

	_ = wsClient.Close() // best-effort graceful close frame
	if sc != nil {
		_ = sc.Close() // idempotent: always stops the writer goroutine and ticker
	}

	// Wait for the drainers to finish — they exit promptly once Close/sc.Close
	// release the parked sends, or at the latest when the shared deadline fires.
	// Then release the deadline watcher so it never outlives the cleanup.
	wg.Wait()
	closeStop()
}

// sleepBackoff waits for the exponential backoff delay for the given attempt,
// honouring ctx. It reports false if ctx was cancelled during the wait.
func sleepBackoff(ctx context.Context, o subOptions, attempt int) bool {
	delay := backoffDelay(o.backoffBase, o.backoffMax, attempt)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// backoffDelay computes the exponential backoff for the given attempt (1-based),
// clamped to maxDelay. The shift exponent is capped before the shift so a large
// attempt — driven by a large WithMaxReconnects — cannot overflow base << shift
// and wrap to a small or negative duration before the clamp. Once base would
// need to shift past the headroom in an int64, the result is already going to
// exceed maxDelay, so it saturates there directly.
func backoffDelay(base, maxDelay time.Duration, attempt int) time.Duration {
	if base <= 0 {
		return maxDelay
	}
	shift := attempt - 1
	if shift < 0 {
		shift = 0
	}
	// LeadingZeros64 on base is the number of high zero bits; shifting base by
	// more than that overflows int64. Cap the shift there, which guarantees the
	// shifted value stays representable, then the clamp below handles the rest.
	if maxShift := bits.LeadingZeros64(uint64(base)); shift >= maxShift {
		return maxDelay
	}
	delay := base << shift
	if delay <= 0 || delay > maxDelay {
		return maxDelay
	}
	return delay
}

// terminalCause returns a non-nil error describing why reconnect was abandoned,
// using the last observed cause when one exists.
func terminalCause(lastErr error) error {
	if lastErr != nil {
		return lastErr
	}
	return errors.New("stash: connection dropped repeatedly")
}
