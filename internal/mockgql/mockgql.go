// Package mockgql provides a reusable, hermetic GraphQL server for tests. It
// stands up an [httptest.Server] that answers HTTP queries and mutations with
// canned responses keyed by operation name, and — when subscriptions are
// configured — upgrades to a graphql-transport-ws WebSocket and streams canned
// events.
//
// The server records every request it receives (the ApiKey header, the
// operation name, and the raw variables), so a test can assert what the client
// sent without reaching for any network. It speaks exactly the wire shapes the
// stash client speaks: genqlient posts a JSON body of {operationName, query,
// variables}, and the subscription transport is graphql-transport-ws
// (connection_init/connection_ack, subscribe, next, complete, ping/pong).
//
// Construct one with [New] and configure it with the With* options:
//
//	srv := mockgql.New(t,
//		mockgql.WithResponse("Version", `{"version":{"version":"v0.31.1"}}`),
//		mockgql.WithSubscription("JobsSubscribe",
//			`{"jobsSubscribe":{"type":"ADD","job":{"id":"j1"}}}`),
//	)
//	c, _ := stash.NewClient(stash.WithURL(srv.URL()), stash.WithAPIKey("k"))
//
// The server and its goroutines are torn down automatically through
// [testing.T.Cleanup]; a test never closes it by hand.
package mockgql

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

// Server is a hermetic GraphQL test server. It is created by [New] and is safe
// for concurrent use by the handler goroutines it runs; the recording accessors
// take an internal lock.
type Server struct {
	t   *testing.T
	srv *httptest.Server

	// httpResponses maps an operation name to its canned HTTP response.
	httpResponses map[string]cannedResponse
	// subscriptions maps a subscription operation name to the frames emitted on
	// each subscribe.
	subscriptions map[string]subscription

	upgrader websocket.Upgrader

	mu       sync.Mutex
	requests []Request
	// connInitParams records the payload of the most recent WebSocket
	// connection_init frame, for asserting forwarded credentials.
	connInitParams map[string]any
}

// Request is one recorded HTTP request: the operation name, the ApiKey header
// the client sent (empty when none), and the raw variables JSON.
type Request struct {
	// OpName is the GraphQL operation name from the request body.
	OpName string
	// APIKey is the value of the ApiKey header, or "" if absent.
	APIKey string
	// Variables is the raw JSON of the request's variables, or nil if none.
	Variables json.RawMessage
}

// cannedResponse is a canned HTTP reply for one operation.
type cannedResponse struct {
	// status is the HTTP status code to write (200 when zero).
	status int
	// body is the full HTTP response body. When envelope is true it is a bare
	// data object that the server wraps in {"data": ...}; otherwise it is sent
	// verbatim (so a test can return a full {"data":...,"errors":...} envelope
	// or non-JSON for a transport-error case).
	body     string
	envelope bool
}

// subscription is the canned behaviour for one subscription operation: the
// frames it streams on each subscribe, and whether to send a terminating
// complete afterwards.
type subscription struct {
	// payloads are the bare data objects emitted as graphql-transport-ws "next"
	// frames, in order. Each is wrapped in {"data": ...}.
	payloads []string
	// complete, when true, sends a "complete" frame after the payloads, ending
	// the stream cleanly.
	complete bool
	// hold, when true, keeps the socket open and idle after the payloads instead
	// of completing — used to exercise client-side keepalive on an idle stream.
	hold bool
}

// Option configures a [Server].
type Option func(*Server)

// WithResponse registers a canned reply for an operation. dataJSON is the bare
// data object for the operation (for example `{"version":{"version":"v0.31.1"}}`
// for the Version operation); the server wraps it in a {"data": ...} envelope.
// Use [WithRawResponse] to return a full envelope or an error body.
func WithResponse(opName, dataJSON string) Option {
	return func(s *Server) {
		s.httpResponses[opName] = cannedResponse{status: http.StatusOK, body: dataJSON, envelope: true}
	}
}

// WithRawResponse registers a canned reply sent verbatim, with the given HTTP
// status. Use it to return a full {"data":...,"errors":...} envelope, an error
// status with a non-GraphQL body, or any shape [WithResponse] cannot express.
func WithRawResponse(opName string, status int, body string) Option {
	return func(s *Server) {
		s.httpResponses[opName] = cannedResponse{status: status, body: body, envelope: false}
	}
}

// WithSubscription registers a subscription that, on each subscribe frame,
// streams the given bare data payloads as "next" frames and then sends a
// "complete". Registering any subscription makes the server accept WebSocket
// upgrades.
func WithSubscription(opName string, payloads ...string) Option {
	return func(s *Server) {
		s.subscriptions[opName] = subscription{payloads: payloads, complete: true}
	}
}

// WithIdleSubscription registers a subscription that streams the given payloads
// (if any) and then holds the socket open and idle instead of completing. It is
// the building block for exercising client-side keepalive against a real-ish
// server: the connection establishes, then nothing flows until the client (or
// the test's context) closes it.
func WithIdleSubscription(opName string, payloads ...string) Option {
	return func(s *Server) {
		s.subscriptions[opName] = subscription{payloads: payloads, hold: true}
	}
}

// New starts a hermetic GraphQL server configured by opts and registers its
// teardown with t. Use [Server.URL] as the stash client's base URL.
func New(t *testing.T, opts ...Option) *Server {
	t.Helper()
	s := &Server{
		t:             t,
		httpResponses: map[string]cannedResponse{},
		subscriptions: map[string]subscription{},
	}
	for _, opt := range opts {
		opt(s)
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

// URL returns the server's base URL. Pass it to stash.WithURL; the client
// appends /graphql, which this server serves on every path.
func (s *Server) URL() string { return s.srv.URL }

// Requests returns a copy of the HTTP requests recorded so far, in order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Request, len(s.requests))
	copy(out, s.requests)
	return out
}

// LastRequest returns the most recent recorded HTTP request and whether one
// exists.
func (s *Server) LastRequest() (Request, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return Request{}, false
	}
	return s.requests[len(s.requests)-1], true
}

// ConnInitParams returns the payload of the most recent WebSocket
// connection_init frame (nil if no subscription connection has been made). Use
// it to assert the ApiKey rode in on the handshake.
func (s *Server) ConnInitParams() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connInitParams
}

// handle dispatches a request to the WebSocket upgrade path when it is a
// subscription handshake, and to the HTTP path otherwise.
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if isWebSocketUpgrade(r) {
		s.handleWebSocket(w, r)
		return
	}
	s.handleHTTP(w, r)
}

// isWebSocketUpgrade reports whether r is a WebSocket upgrade request.
func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// httpRequestBody is the JSON shape genqlient posts for an HTTP operation.
type httpRequestBody struct {
	OperationName string          `json:"operationName"`
	Query         string          `json:"query"`
	Variables     json.RawMessage `json:"variables"`
}

// handleHTTP records the request and writes the canned response for its
// operation. An unregistered operation is a test failure, since a silent empty
// reply would surface far from its cause.
func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	var body httpRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.t.Errorf("mockgql: decoding request body: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.requests = append(s.requests, Request{
		OpName:    body.OperationName,
		APIKey:    r.Header.Get("ApiKey"),
		Variables: body.Variables,
	})
	s.mu.Unlock()

	resp, ok := s.httpResponses[body.OperationName]
	if !ok {
		s.t.Errorf("mockgql: no response registered for operation %q", body.OperationName)
		http.Error(w, "no response", http.StatusNotImplemented)
		return
	}

	status := resp.status
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if resp.envelope {
		_, _ = fmt.Fprintf(w, `{"data":%s}`, resp.body)
		return
	}
	_, _ = fmt.Fprint(w, resp.body)
}
