package asr

import (
	"encoding/binary"
	"errors"
	"testing"
)

func pcmSeconds(sec float64, amplitude int16) []byte {
	n := int(sec * BytesPerSecond)
	n -= n % 2
	b := make([]byte, n)
	for i := 0; i < n; i += 2 {
		binary.LittleEndian.PutUint16(b[i:], uint16(amplitude))
	}
	return b
}

// wavWith builds a WAV with explicit fmt fields so each bad-input row flips
// exactly one thing.
func wavWith(format, channels uint16, rate uint32, bits uint16, pcm []byte) []byte {
	b := EncodeWAV(pcm)
	binary.LittleEndian.PutUint16(b[20:], format)
	binary.LittleEndian.PutUint16(b[22:], channels)
	binary.LittleEndian.PutUint32(b[24:], rate)
	binary.LittleEndian.PutUint16(b[34:], bits)
	return b
}

func TestParseWAVTable(t *testing.T) {
	voice := pcmSeconds(0.5, 800)
	good := EncodeWAV(voice)
	truncated := append([]byte{}, good[:len(good)-100]...)
	// A LIST chunk between fmt and data (what some recorders emit).
	withList := append([]byte{}, good[:36]...)
	withList = append(withList, []byte("LIST")...)
	withList = binary.LittleEndian.AppendUint32(withList, 5)
	withList = append(withList, 'I', 'N', 'F', 'O', 'x', 0) // odd size + pad byte
	withList = append(withList, good[36:]...)

	cases := []struct {
		name string
		in   []byte
		ok   bool
	}{
		{"valid 16 kHz mono 16-bit", good, true},
		{"valid with LIST chunk before data", withList, true},
		{"zero-length", nil, false},
		{"too short for a header", []byte("RIFF"), false},
		{"not RIFF", append([]byte("OggS"), good[4:]...), false},
		{"not WAVE", append(append([]byte{}, good[:8]...), append([]byte("AVI "), good[12:]...)...), false},
		{"truncated data chunk", truncated, false},
		{"wrong rate 44100", wavWith(1, 1, 44100, 16, voice), false},
		{"wrong rate 8000", wavWith(1, 1, 8000, 16, voice), false},
		{"stereo", wavWith(1, 2, SampleRate, 16, voice), false},
		{"8-bit", wavWith(1, 1, SampleRate, 8, voice), false},
		{"float format 3", wavWith(3, 1, SampleRate, 16, voice), false},
		{"odd data length", EncodeWAV([]byte{1, 2, 3}), false},
		{"no data chunk", good[:36], false},
	}
	for _, c := range cases {
		got, err := ParseWAV(c.in)
		if c.ok != (err == nil) {
			t.Errorf("%s: ok=%v err=%v", c.name, c.ok, err)
			continue
		}
		if err != nil && !errors.Is(err, ErrUnsupportedAudio) {
			t.Errorf("%s: error must wrap ErrUnsupportedAudio, got %v", c.name, err)
		}
		if c.ok && (got.DurationMs != 500 || len(got.PCM) != len(voice)) {
			t.Errorf("%s: duration %d, pcm %d", c.name, got.DurationMs, len(got.PCM))
		}
	}
}

func TestRMS(t *testing.T) {
	if RMS(pcmSeconds(0.1, 0)) != 0 {
		t.Fatal("silence must be 0")
	}
	if r := RMS(pcmSeconds(0.1, 800)); r < 0.02 || r > 0.03 {
		t.Fatalf("800/32768 ≈ 0.0244, got %f", r)
	}
	if RMS(nil) != 0 || RMS([]byte{1}) != 0 {
		t.Fatal("degenerate input must be 0")
	}
}
