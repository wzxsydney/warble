package config

import (
	"net"
	"os"
	"strconv"
)

type Config struct {
	Host           string
	OTAPort        int
	WSPort         int
	WSPath         string
	AudioSampleHz  int
	AudioFrameMS   int
	WSURLOverride  string // if set, OTA returns this instead of computing
	TimezoneOffset int    // minutes

	WhisperURL   string
	OllamaURL    string
	OllamaAPIKey string
	PiperURL     string
	ConfigDir    string // dir holding prompt.txt + runtime.json (hot-reloadable)

	// VAD sidecar (Silero). Empty disables server-side end-of-speech detection;
	// in that case the device must send listen.stop itself (manual/PTT mode).
	VADURL          string
	VADMinSilenceMS int
	VADSpeechPadMS  int
	VADMinSpeechMS  int
	VADThreshold    float64
}

func Default() Config {
	return Config{
		Host:           "0.0.0.0",
		OTAPort:        8003,
		WSPort:         8000,
		WSPath:         "/xiaozhi/v1/",
		AudioSampleHz:  24000,
		AudioFrameMS:   60,
		WSURLOverride:  envOr("WARBLE_WS_URL", ""),
		TimezoneOffset: 0, // minutes east of UTC; set for your locale (e.g. 60 = UTC+1)

		WhisperURL:   envOr("WARBLE_WHISPER_URL", "http://127.0.0.1:8082/inference"),
		OllamaURL:    envOr("WARBLE_OLLAMA_URL", "http://127.0.0.1:11434"),
		PiperURL:     envOr("WARBLE_PIPER_URL", "http://127.0.0.1:8001/v1/audio/speech"),
		OllamaAPIKey: envOr("OLLAMA_API_KEY", ""),
		ConfigDir:    envOr("WARBLE_CONFIG_DIR", "./config"),

		VADURL:          envOr("WARBLE_VAD_URL", "ws://127.0.0.1:8005/vad"),
		VADMinSilenceMS: envIntOr("WARBLE_VAD_MIN_SILENCE_MS", 600),
		VADSpeechPadMS:  envIntOr("WARBLE_VAD_SPEECH_PAD_MS", 30),
		VADMinSpeechMS:  envIntOr("WARBLE_VAD_MIN_SPEECH_MS", 250),
		VADThreshold:    envFloatOr("WARBLE_VAD_THRESHOLD", 0.5),
	}
}

func envIntOr(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envFloatOr(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// LocalIP returns the first non-loopback IPv4 the device can reach us on.
// Falls back to 127.0.0.1.
func LocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		v4 := ipnet.IP.To4()
		if v4 == nil {
			continue
		}
		return v4.String()
	}
	return "127.0.0.1"
}
