// Package proto defines the xiaozhi WebSocket message shapes.
// See PROTOCOL.md for the wire spec and source-line citations.
package proto

// ClientHello is the first text frame the device sends after WS open.
type ClientHello struct {
	Type        string         `json:"type"` // "hello"
	Version     int            `json:"version"`
	Features    map[string]any `json:"features,omitempty"`
	Transport   string         `json:"transport"`
	AudioParams ClientAudio    `json:"audio_params"`
}

type ClientAudio struct {
	Format        string `json:"format"`
	SampleRate    int    `json:"sample_rate"`
	Channels      int    `json:"channels"`
	FrameDuration int    `json:"frame_duration"`
}

// ServerHello is our reply. transport must be "websocket".
type ServerHello struct {
	Type        string      `json:"type"` // "hello"
	Transport   string      `json:"transport"`
	SessionID   string      `json:"session_id"`
	AudioParams ServerAudio `json:"audio_params"`
}

type ServerAudio struct {
	SampleRate    int `json:"sample_rate"`
	FrameDuration int `json:"frame_duration"`
}

// Envelope is the minimum shape shared by every C→S text frame.
type Envelope struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id,omitempty"`
}

// Listen carries listen.{start,stop,detect}.
type Listen struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	State     string `json:"state"`          // "start"|"stop"|"detect"
	Mode      string `json:"mode,omitempty"` // "auto"|"manual"|"realtime"
	Text      string `json:"text,omitempty"` // wake phrase on "detect"
}

// Abort is sent when the user interrupts.
type Abort struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Reason    string `json:"reason,omitempty"` // e.g. "wake_word_detected"
}

// STT is sent by the server with the user's transcript.
type STT struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

// TTS drives the device's speak state machine.
type TTS struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	State     string `json:"state"`          // "start"|"sentence_start"|"stop"
	Text      string `json:"text,omitempty"` // for sentence_start
}

// LLM carries an optional emotion tag.
type LLM struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Emotion   string `json:"emotion"`
	Text      string `json:"text,omitempty"`
}

// Alert pops a notification on the device.
type Alert struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Emotion string `json:"emotion"`
}
