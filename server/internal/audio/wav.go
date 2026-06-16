package audio

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// WriteWAVMono16 writes a mono 16-bit PCM WAV to w. Used to package
// whisper-server's expected input from raw int16 samples.
func WriteWAVMono16(w io.Writer, pcm []int16, sampleHz int) error {
	dataLen := len(pcm) * 2
	totalLen := 36 + dataLen

	hdr := make([]byte, 44)
	copy(hdr[0:4], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(totalLen))
	copy(hdr[8:12], "WAVE")
	copy(hdr[12:16], "fmt ")
	binary.LittleEndian.PutUint32(hdr[16:20], 16) // fmt chunk size
	binary.LittleEndian.PutUint16(hdr[20:22], 1)  // PCM
	binary.LittleEndian.PutUint16(hdr[22:24], 1)  // channels
	binary.LittleEndian.PutUint32(hdr[24:28], uint32(sampleHz))
	binary.LittleEndian.PutUint32(hdr[28:32], uint32(sampleHz*2)) // byte rate
	binary.LittleEndian.PutUint16(hdr[32:34], 2)                  // block align
	binary.LittleEndian.PutUint16(hdr[34:36], 16)                 // bits/sample
	copy(hdr[36:40], "data")
	binary.LittleEndian.PutUint32(hdr[40:44], uint32(dataLen))

	if _, err := w.Write(hdr); err != nil {
		return err
	}
	return binary.Write(w, binary.LittleEndian, pcm)
}

// ParseWAVMono16 reads a mono 16-bit PCM WAV and returns samples + sample rate.
// Tolerates extra chunks (LIST, etc.) between fmt and data.
func ParseWAVMono16(data []byte) ([]int16, int, error) {
	if len(data) < 44 {
		return nil, 0, fmt.Errorf("wav too short: %d bytes", len(data))
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, fmt.Errorf("not a WAV")
	}

	r := bytes.NewReader(data[12:])
	var sampleHz int
	var bitsPerSample uint16
	var channels uint16

	for r.Len() > 0 {
		var id [4]byte
		if _, err := io.ReadFull(r, id[:]); err != nil {
			return nil, 0, fmt.Errorf("read chunk id: %w", err)
		}
		var size uint32
		if err := binary.Read(r, binary.LittleEndian, &size); err != nil {
			return nil, 0, fmt.Errorf("read chunk size: %w", err)
		}

		switch string(id[:]) {
		case "fmt ":
			fmtBuf := make([]byte, size)
			if _, err := io.ReadFull(r, fmtBuf); err != nil {
				return nil, 0, fmt.Errorf("read fmt: %w", err)
			}
			if size < 16 {
				return nil, 0, fmt.Errorf("fmt chunk too small")
			}
			channels = binary.LittleEndian.Uint16(fmtBuf[2:4])
			sampleHz = int(binary.LittleEndian.Uint32(fmtBuf[4:8]))
			bitsPerSample = binary.LittleEndian.Uint16(fmtBuf[14:16])
		case "data":
			if channels != 1 {
				return nil, 0, fmt.Errorf("wav channels=%d, want mono", channels)
			}
			if bitsPerSample != 16 {
				return nil, 0, fmt.Errorf("wav bits=%d, want 16", bitsPerSample)
			}
			pcm := make([]int16, size/2)
			if err := binary.Read(r, binary.LittleEndian, pcm); err != nil {
				return nil, 0, fmt.Errorf("read data: %w", err)
			}
			return pcm, sampleHz, nil
		default:
			// Skip unknown chunk
			if _, err := r.Seek(int64(size), io.SeekCurrent); err != nil {
				return nil, 0, fmt.Errorf("skip chunk %s: %w", id, err)
			}
		}
	}
	return nil, 0, fmt.Errorf("no data chunk")
}
