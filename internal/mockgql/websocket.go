package mockgql

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

// wsMessage is one graphql-transport-ws envelope read from or written to the
// socket. Payload is raw so it can carry an arbitrary data object or the
// connection_init params.
type wsMessage struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// handleWebSocket upgrades the connection and runs the graphql-transport-ws
// state machine: it acknowledges connection_init (recording its params), and on
// each subscribe streams the configured frames for that operation. ping is
// answered with pong; the loop ends when the client hangs up or the server is
// torn down.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.t.Logf("mockgql: websocket upgrade: %v", err)
		return
	}
	defer func() { _ = conn.Close() }()

	// gorilla forbids concurrent writes; subscribe streams run in their own
	// goroutines, so every write goes through this lock.
	var writeMu sync.Mutex
	write := func(msg wsMessage) {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.WriteJSON(msg)
	}

	for {
		var msg wsMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg.Type {
		case "connection_init":
			var params map[string]any
			if len(msg.Payload) > 0 {
				_ = json.Unmarshal(msg.Payload, &params)
			}
			s.mu.Lock()
			s.connInitParams = params
			s.mu.Unlock()
			write(wsMessage{Type: "connection_ack"})
		case "subscribe":
			s.runSubscribe(write, msg)
		case "ping":
			write(wsMessage{Type: "pong"})
		case "complete":
			// The client unsubscribed; nothing more to send for this id.
		}
	}
}

// runSubscribe streams the frames configured for the subscribe frame's
// operation. The operation is identified by matching the configured operation
// names against the subscribe payload's query text, which carries the
// "subscription <Name>" declaration. Unknown operations fail the test.
func (s *Server) runSubscribe(write func(wsMessage), msg wsMessage) {
	name := s.matchSubscription(msg.Payload)
	if name == "" {
		s.t.Errorf("mockgql: no subscription registered for subscribe payload %s", msg.Payload)
		return
	}
	sub := s.subscriptions[name]
	id := msg.ID

	go func() {
		for _, payload := range sub.payloads {
			write(wsMessage{Type: "next", ID: id, Payload: json.RawMessage(`{"data":` + payload + `}`)})
		}
		// An idle subscription holds the socket open without completing, so the
		// client's keepalive is what keeps it alive. A normal one completes.
		if sub.complete {
			write(wsMessage{Type: "complete", ID: id})
		}
	}()
}

// subscribePayload is the body of a graphql-transport-ws subscribe frame: it
// carries the operation as a standard GraphQL request.
type subscribePayload struct {
	OperationName string `json:"operationName"`
	Query         string `json:"query"`
}

// matchSubscription returns the registered subscription operation name whose
// identifier appears in the subscribe payload, or "" if none matches. It prefers
// the explicit operationName and falls back to scanning the query text, so it
// works whether or not the client sets operationName.
func (s *Server) matchSubscription(payload json.RawMessage) string {
	var p subscribePayload
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &p)
	}
	if p.OperationName != "" {
		if _, ok := s.subscriptions[p.OperationName]; ok {
			return p.OperationName
		}
	}
	// The generated subscription query text contains "subscription <Name>", so a
	// plain substring match identifies the operation unambiguously among the
	// registered names.
	for name := range s.subscriptions {
		if name != "" && strings.Contains(p.Query, name) {
			return name
		}
	}
	return ""
}
