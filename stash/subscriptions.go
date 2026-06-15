package stash

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	conn graphql.WSConn

	writes   chan writeRequest
	stop     chan struct{}
	done     chan struct{}
	interval time.Duration

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
func newSerialConn(conn graphql.WSConn, interval time.Duration, newTicker func(time.Duration) *time.Ticker) *serialConn {
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
			req.result <- sc.conn.WriteMessage(req.msgType, req.data)
		case <-tickC:
			// A failed keepalive ping is not fatal here: the next ReadMessage
			// will observe the broken connection and surface the error through
			// genqlient's errChan. Ignoring it keeps the writer alive to serve
			// the close frame.
			_ = sc.conn.WriteMessage(websocket.PingMessage, nil)
		}
	}
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
// to call more than once.
func (sc *serialConn) Close() error {
	select {
	case <-sc.stop:
		// Already closing; wait for the writer to finish and close the conn once.
	default:
		close(sc.stop)
	}
	<-sc.done
	return sc.conn.Close()
}

// wsDialer adapts gorilla/websocket to genqlient's [graphql.Dialer]. Each dial
// returns a [serialConn] wrapping the gorilla connection, so writes are
// serialised and keepalive pings are sent on the configured interval.
type wsDialer struct {
	dialer    *websocket.Dialer
	interval  time.Duration
	newTicker func(time.Duration) *time.Ticker
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
	return newSerialConn(conn, d.interval, d.newTicker), nil
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
	wsClient := c.newSubscriptionClient(defaultKeepaliveInterval, nil)
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
func (c *Client) newSubscriptionClient(interval time.Duration, newTicker func(time.Duration) *time.Ticker) graphql.WebSocketClient {
	dialer := &wsDialer{
		dialer:    websocket.DefaultDialer,
		interval:  interval,
		newTicker: newTicker,
	}

	opts := []graphql.WebSocketOption{}
	if key := c.APIKey(); key != "" {
		opts = append(opts, graphql.WithConnectionParams(map[string]interface{}{"ApiKey": key}))
	}
	return graphql.NewClientUsingWebSocket(c.WebSocketURL(), dialer, opts...)
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
// The delay starts at base, doubles each attempt, and is capped at max.
// Non-positive values fall back to the defaults.
func WithBackoff(base, max time.Duration) SubOption {
	return func(o *subOptions) {
		if base > 0 {
			o.backoffBase = base
		}
		if max > 0 {
			o.backoffMax = max
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
	wsClient := c.newSubscriptionClient(o.interval, o.newTicker)
	errChan, startErr := wsClient.Start(ctx)
	if startErr != nil {
		return false, classifyWS(startErr)
	}
	defer func() { _ = wsClient.Close() }()

	stream, subErr := subscribe(ctx, wsClient)
	if subErr != nil {
		return false, subErr
	}

	for {
		select {
		case <-ctx.Done():
			return delivered, ctx.Err()
		case connErr := <-errChan:
			// A connection-level error ends this stream; the loop reconnects.
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

// sleepBackoff waits for the exponential backoff delay for the given attempt,
// honouring ctx. It reports false if ctx was cancelled during the wait.
func sleepBackoff(ctx context.Context, o subOptions, attempt int) bool {
	delay := o.backoffBase << (attempt - 1)
	if delay <= 0 || delay > o.backoffMax {
		delay = o.backoffMax
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// terminalCause returns a non-nil error describing why reconnect was abandoned,
// using the last observed cause when one exists.
func terminalCause(lastErr error) error {
	if lastErr != nil {
		return lastErr
	}
	return errors.New("stash: connection dropped repeatedly")
}
