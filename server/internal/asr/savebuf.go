package asr

// Ring buffer of the most recent ASR inputs. The .wav (16 kHz mono int16) is
// written before whisper-server is called; the matching .txt sidecar with the
// returned transcript is written after. If whisper fails or never returns, the
// .wav is still there to re-run through a different model or flag set.
//
// On disk:
//
//	/tmp/stackchan-asr/<unix-millis>.wav
//	/tmp/stackchan-asr/<unix-millis>.txt
//
// Files past `saveLimit` are swept on every save so the directory size is
// bounded. At ~5 s of 24 kHz int16 mono per turn and saveLimit=50, the dir
// caps at ~12 MB. Saving is best-effort: errors are swallowed so a write
// failure never disrupts the live transcription path.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	saveDir   = "/tmp/stackchan-asr"
	saveLimit = 50
)

// saveAudio writes the encoded WAV to disk and returns the basename (without
// extension) used. Returns an empty string on any error.
func saveAudio(wav []byte) string {
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		return ""
	}
	name := fmt.Sprintf("%013d", time.Now().UnixMilli())
	if err := os.WriteFile(filepath.Join(saveDir, name+".wav"), wav, 0o644); err != nil {
		return ""
	}
	sweep()
	return name
}

// saveTranscript writes the sidecar text file. No-op when name is empty (the
// audio save failed earlier in this turn).
func saveTranscript(name, text string) {
	if name == "" {
		return
	}
	_ = os.WriteFile(filepath.Join(saveDir, name+".txt"), []byte(text+"\n"), 0o644)
}

func sweep() {
	entries, err := os.ReadDir(saveDir)
	if err != nil {
		return
	}
	wavs := make([]string, 0, len(entries))
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".wav" {
			wavs = append(wavs, e.Name())
		}
	}
	if len(wavs) <= saveLimit {
		return
	}
	sort.Strings(wavs) // lexicographic == chronological since names are unix-millis
	for _, old := range wavs[:len(wavs)-saveLimit] {
		base := old[:len(old)-len(".wav")]
		_ = os.Remove(filepath.Join(saveDir, old))
		_ = os.Remove(filepath.Join(saveDir, base+".txt"))
	}
}
