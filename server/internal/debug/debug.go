// Package debug exposes a small /debug/* HTTP namespace for poking at
// the running server without going through the device. Enabled only
// when WARBLE_DEBUG=1 (set in cmd/server/main.go). It is intentionally
// minimal: each endpoint serves one concrete use case so the surface
// area stays small until a second real consumer shows up.
//
// Endpoints:
//
//	GET  /debug/sessions          list active WS sessions
//	POST /debug/say  {device_id?, text}   inject a text turn into a session
//
// Routing rule for /debug/say: if device_id is omitted and exactly one
// session is active, that session is picked. Otherwise device_id is
// required. This keeps single-device use ergonomic without losing
// addressability when more than one device is connected.
package debug

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/rebelthor/warble/server/internal/ws"
)

type Server struct {
	ws  *ws.Server
	log *slog.Logger
}

func New(wsSrv *ws.Server, log *slog.Logger) *Server {
	return &Server{ws: wsSrv, log: log}
}

func (d *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("/debug/sessions", d.handleSessions)
	mux.HandleFunc("/debug/say", d.handleSay)
}

func (d *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	list := d.ws.ListSessions()
	out := make([]map[string]string, 0, len(list))
	for _, s := range list {
		out = append(out, map[string]string{
			"session_id": s.ID(),
			"device_id":  s.DeviceID(),
			"client_id":  s.ClientID(),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (d *Server) handleSay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		DeviceID string `json:"device_id"`
		Text     string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	body.Text = strings.TrimSpace(body.Text)
	if body.Text == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}

	sess, err := d.resolveSession(body.DeviceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	d.log.Info("debug.say", "device_id", sess.DeviceID(), "session_id", sess.ID(), "text", body.Text)
	sess.InjectText(body.Text)
	writeJSON(w, http.StatusAccepted, map[string]string{
		"status":     "queued",
		"device_id":  sess.DeviceID(),
		"session_id": sess.ID(),
	})
}

// resolveSession picks the target session given an optional device_id.
// If device_id is empty and exactly one session is alive, returns it;
// otherwise the caller must specify which device.
func (d *Server) resolveSession(deviceID string) (*ws.Session, error) {
	if deviceID != "" {
		sess := d.ws.FindByDeviceID(deviceID)
		if sess == nil {
			return nil, errNotFound{msg: "no session for device_id=" + deviceID}
		}
		return sess, nil
	}
	list := d.ws.ListSessions()
	switch len(list) {
	case 0:
		return nil, errNotFound{msg: "no active sessions"}
	case 1:
		return list[0], nil
	default:
		return nil, errNotFound{msg: "multiple sessions active; specify device_id"}
	}
}

type errNotFound struct{ msg string }

func (e errNotFound) Error() string { return e.msg }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
