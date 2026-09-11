package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// clearDeadlines removes the request scoped read and write deadlines before a
// connection is upgraded. Without this the server's ReadTimeout and
// WriteTimeout stay on the hijacked socket and close every long lived stream.
// clearDeadlines lifts the server's read and write timeouts for a connection
// that is about to become a long lived socket. The errors are reported rather
// than dropped: if a middleware wrapper ever stops forwarding to the underlying
// writer, this fails silently and every socket dies at the write timeout
// minutes later, which is a miserable thing to diagnose from the symptom.
func clearDeadlines(w http.ResponseWriter) error {
	controller := http.NewResponseController(w)
	if err := controller.SetReadDeadline(time.Time{}); err != nil {
		return err
	}
	return controller.SetWriteDeadline(time.Time{})
}

// The hub is an in-process fan-out for WebSocket subscribers. Topics keep order
// workspaces and personal notification inboxes on one mechanism.
func orderTopic(id uuid.UUID) string { return "order:" + id.String() }

func userTopic(id uuid.UUID) string { return "user:" + id.String() }

func (s *Server) subscribe(topic string) (chan []byte, func()) {
	updates := make(chan []byte, 16)
	s.hubMu.Lock()
	if s.topics == nil {
		s.topics = make(map[string]map[chan []byte]struct{})
	}
	if s.topics[topic] == nil {
		s.topics[topic] = make(map[chan []byte]struct{})
	}
	s.topics[topic][updates] = struct{}{}
	s.hubMu.Unlock()
	return updates, func() {
		s.hubMu.Lock()
		delete(s.topics[topic], updates)
		if len(s.topics[topic]) == 0 {
			delete(s.topics, topic)
		}
		s.hubMu.Unlock()
	}
}

func (s *Server) publish(topic string, event any) {
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	for subscriber := range s.topics[topic] {
		select {
		case subscriber <- payload:
		default:
		}
	}
}

// PublishUser lets the outbox dispatcher push an inbox update to a signed in
// user without importing the HTTP layer's internals.
func (s *Server) PublishUser(userID uuid.UUID, event any) { s.publish(userTopic(userID), event) }
