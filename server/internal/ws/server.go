// Package ws implements the xiaozhi audio-session WebSocket endpoint.
// See PROTOCOL.md "WebSocket session".
package ws

import (
	"log/slog"
	"net/http"
	"sync"

	"github.com/coder/websocket"

	"github.com/rebelthor/warble/server/internal/config"
	"github.com/rebelthor/warble/server/internal/pipeline"
)

type Server struct {
	cfg      config.Config
	log      *slog.Logger
	pipeline *pipeline.Pipeline

	// sessions tracks live WS sessions by Device-Id so the /debug endpoints
	// can address an active device. One device = one entry. If the same
	// device reconnects while an old entry is stale, the new connection
	// overwrites; the old entry is cleared on its own close in handle().
	sessions sync.Map // map[string]*Session
}

func New(cfg config.Config, pipe *pipeline.Pipeline, log *slog.Logger) *Server {
	return &Server{cfg: cfg, pipeline: pipe, log: log}
}

func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc(s.cfg.WSPath, s.handle)
}

// FindByDeviceID returns the live session for a given Device-Id, or nil.
// Public so the /debug endpoints can route by it without exporting the
// internal map type.
func (s *Server) FindByDeviceID(deviceID string) *Session {
	if v, ok := s.sessions.Load(deviceID); ok {
		return v.(*Session)
	}
	return nil
}

// ListSessions returns a snapshot of live sessions. Order is not stable.
func (s *Server) ListSessions() []*Session {
	var out []*Session
	s.sessions.Range(func(_, v any) bool {
		out = append(out, v.(*Session))
		return true
	})
	return out
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	deviceID := r.Header.Get("Device-Id")
	clientID := r.Header.Get("Client-Id")
	protoVer := r.Header.Get("Protocol-Version")

	if deviceID == "" {
		// Fall back to query string, matching xinnan-tech's behavior so
		// the browser test_page.html keeps working.
		deviceID = r.URL.Query().Get("device-id")
		clientID = firstNonEmpty(clientID, r.URL.Query().Get("client-id"))
	}

	if deviceID == "" {
		http.Error(w, "missing Device-Id", http.StatusBadRequest)
		return
	}

	s.log.Info("ws.upgrade", "device_id", deviceID, "client_id", clientID, "protocol_version", protoVer)

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // closed LAN; allow any Origin
	})
	if err != nil {
		s.log.Error("ws.accept", "err", err)
		return
	}

	sess := newSession(conn, deviceID, clientID, s.cfg, s.pipeline, s.log)
	s.sessions.Store(deviceID, sess)
	defer func() {
		// Only clear if we still own the slot; a reconnect from the same
		// device while the old session was tearing down would have stored
		// a newer pointer that we shouldn't blow away.
		if v, ok := s.sessions.Load(deviceID); ok && v.(*Session) == sess {
			s.sessions.Delete(deviceID)
		}
	}()
	if err := sess.run(r.Context()); err != nil {
		s.log.Error("session.run", "session_id", sess.id, "err", err)
		_ = conn.Close(websocket.StatusInternalError, "session error")
		return
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
