// Package ota implements the bootstrap HTTP endpoint the firmware
// hits before opening the WebSocket. See PROTOCOL.md "OTA endpoint".
package ota

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/rebelthor/warble/server/internal/config"
)

// Synthetic client IDs we want filtered out of INFO logs (bin/backend health
// probes, manual smoke tests). These hit OTA every status call and bury the
// real device boot events otherwise.
var syntheticClientIDs = []string{"backend-script-health", "smoke-test"}

func isSyntheticClient(clientID string) bool {
	for _, prefix := range syntheticClientIDs {
		if strings.HasPrefix(clientID, prefix) {
			return true
		}
	}
	return false
}

type Handler struct {
	cfg config.Config
	log *slog.Logger
}

func New(cfg config.Config, log *slog.Logger) *Handler {
	return &Handler{cfg: cfg, log: log}
}

type serverTime struct {
	Timestamp      int64 `json:"timestamp"`
	TimezoneOffset int   `json:"timezone_offset"`
}

type firmwareBlock struct {
	Version string `json:"version"`
	URL     string `json:"url"`
}

type websocketBlock struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

type response struct {
	ServerTime serverTime     `json:"server_time"`
	Firmware   firmwareBlock  `json:"firmware"`
	Websocket  websocketBlock `json:"websocket"`
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/xiaozhi/ota/", h.handle)
}

func (h *Handler) handle(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodOptions:
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.WriteHeader(http.StatusOK)
		return
	case http.MethodGet:
		fmt.Fprintf(w, "OTA endpoint up. WS=%s\n", h.wsURL())
		return
	case http.MethodPost:
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, _ := io.ReadAll(r.Body)
	defer r.Body.Close()

	deviceID := r.Header.Get("Device-Id")
	clientID := r.Header.Get("Client-Id")
	deviceVer := firstHeader(r, "Device-Version", "Firmware-Version", "App-Version", "Application-Version")
	if deviceVer == "" {
		deviceVer = extractAppVersion(body)
	}
	if deviceVer == "" {
		deviceVer = "0.0.0"
	}

	logFn := h.log.Info
	if isSyntheticClient(clientID) {
		logFn = h.log.Debug
	}
	logFn("ota.post",
		"device_id", deviceID,
		"client_id", clientID,
		"device_version", deviceVer,
		"body_bytes", len(body),
	)

	resp := response{
		ServerTime: serverTime{
			Timestamp:      time.Now().UnixMilli(),
			TimezoneOffset: h.cfg.TimezoneOffset,
		},
		Firmware: firmwareBlock{
			Version: deviceVer, // echo so device never triggers OTA download
			URL:     "",
		},
		Websocket: websocketBlock{
			URL:   h.wsURL(),
			Token: "",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) wsURL() string {
	if h.cfg.WSURLOverride != "" {
		return h.cfg.WSURLOverride
	}
	return fmt.Sprintf("ws://%s:%d%s", config.LocalIP(), h.cfg.WSPort, h.cfg.WSPath)
}

func firstHeader(r *http.Request, keys ...string) string {
	for _, k := range keys {
		if v := r.Header.Get(k); v != "" {
			return v
		}
	}
	return ""
}

func extractAppVersion(body []byte) string {
	var data struct {
		Application struct {
			Version string `json:"version"`
		} `json:"application"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return ""
	}
	return data.Application.Version
}
