package asr

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// The clip format the app records (Sumrak T08/T09 recorder, kept by Aflulit):
// RIFF/WAVE, PCM (format 1), mono, 16 kHz, 16-bit. Anything else is refused
// with ErrUnsupportedAudio — the service has no decoder on purpose.
const (
	SampleRate     = 16000
	BytesPerSecond = SampleRate * 2
)

// ErrUnsupportedAudio is returned for anything that is not the one WAV
// format the service accepts (415 at the handler).
var ErrUnsupportedAudio = errors.New("unsupported audio format")

// WAV describes a validated clip.
type WAV struct {
	// PCM is the raw little-endian 16-bit mono sample data.
	PCM []byte
	// DurationMs is derived from the data length, never trusted from headers.
	DurationMs int64
}

// ParseWAV validates b as a 16 kHz mono 16-bit PCM WAV and returns its
// sample data. Extra chunks (LIST, fact, …) are skipped; the data chunk must
// be fully present (a declared length beyond the file is a truncated upload).
func ParseWAV(b []byte) (WAV, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return WAV{}, fmt.Errorf("%w: not a RIFF/WAVE file", ErrUnsupportedAudio)
	}
	var (
		haveFmt bool
		pcm     []byte
	)
	off := 12
	for off+8 <= len(b) {
		id := string(b[off : off+4])
		size := int(binary.LittleEndian.Uint32(b[off+4 : off+8]))
		body := off + 8
		if size < 0 || body+size > len(b) {
			if id == "data" {
				return WAV{}, fmt.Errorf("%w: truncated data chunk", ErrUnsupportedAudio)
			}
			return WAV{}, fmt.Errorf("%w: truncated %q chunk", ErrUnsupportedAudio, id)
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return WAV{}, fmt.Errorf("%w: short fmt chunk", ErrUnsupportedAudio)
			}
			format := binary.LittleEndian.Uint16(b[body : body+2])
			channels := binary.LittleEndian.Uint16(b[body+2 : body+4])
			rate := binary.LittleEndian.Uint32(b[body+4 : body+8])
			bits := binary.LittleEndian.Uint16(b[body+14 : body+16])
			switch {
			case format != 1:
				return WAV{}, fmt.Errorf("%w: format %d, want PCM (1)", ErrUnsupportedAudio, format)
			case channels != 1:
				return WAV{}, fmt.Errorf("%w: %d channels, want mono", ErrUnsupportedAudio, channels)
			case rate != SampleRate:
				return WAV{}, fmt.Errorf("%w: %d Hz, want %d", ErrUnsupportedAudio, rate, SampleRate)
			case bits != 16:
				return WAV{}, fmt.Errorf("%w: %d bits, want 16", ErrUnsupportedAudio, bits)
			}
			haveFmt = true
		case "data":
			if !haveFmt {
				return WAV{}, fmt.Errorf("%w: data before fmt", ErrUnsupportedAudio)
			}
			pcm = b[body : body+size]
			if len(pcm)%2 != 0 {
				return WAV{}, fmt.Errorf("%w: odd data length", ErrUnsupportedAudio)
			}
			return WAV{PCM: pcm, DurationMs: int64(len(pcm)) * 1000 / BytesPerSecond}, nil
		}
		// Chunks are word-aligned: an odd size carries one pad byte.
		off = body + size + size%2
	}
	return WAV{}, fmt.Errorf("%w: no data chunk", ErrUnsupportedAudio)
}

// RMS is the root-mean-square level of 16-bit PCM on a 0..1 scale — the
// silent-clip guard (Ilan's threshold 0.001) so silence never spins the GPU.
func RMS(pcm []byte) float64 {
	if len(pcm) < 2 {
		return 0
	}
	var sum float64
	for i := 0; i+1 < len(pcm); i += 2 {
		v := float64(int16(binary.LittleEndian.Uint16(pcm[i:]))) / 32768
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(pcm)/2))
}

// EncodeWAV frames 16 kHz mono 16-bit PCM as a canonical 44-byte-header WAV
// (used by tests and by the runner to stage a clean file for the CLI).
func EncodeWAV(pcm []byte) []byte {
	b := make([]byte, 44+len(pcm))
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+len(pcm)))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], SampleRate)
	binary.LittleEndian.PutUint32(b[28:], BytesPerSecond)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(pcm)))
	copy(b[44:], pcm)
	return b
}
