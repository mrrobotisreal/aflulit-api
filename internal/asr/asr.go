// Package asr is AflulitAPI's speech-to-text runner (T12, ADR-0008 §2): a
// Whisper decode of one short 16 kHz mono WAV in Hebrew or English, shelled
// out to whisper-ctranslate2 on the home server's GPU. Ported from Ilan's
// CLIWorker with the model made configurable and the input validated in pure
// Go (no ffmpeg). ASR returns text only, never a grade (ADR-0007 §4); Hebrew
// normalisation and matching stay in the app.
package asr

import (
	"context"
	"errors"
)

// Languages a request may ask for. "he" is the default; "en" exists solely
// for the scenario «איך אומרים …» re-decode of the same clip
// (design/SPEAKING_SCENARIOS_HE.md §1.3). Whisper decodes with the requested
// constraint; it never translates.
var Languages = map[string]bool{"he": true, "en": true}

// ErrUnsupportedLanguage is returned before anything runs for a language
// outside Languages.
var ErrUnsupportedLanguage = errors.New("unsupported language")

// Word is one word-level timestamp (only when word timestamps are enabled
// and the CLI emitted them).
type Word struct {
	Word    string `json:"word"`
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
}

// Result is what a Runner returns. Its JSON shape (minus NoSpeech, which
// the handler folds into text "") is the response contract T14 consumes.
type Result struct {
	// Text is Whisper's transcript, trimmed and NFC-normalised. Empty for
	// silence.
	Text string `json:"text"`
	// Words is omitted unless word timestamps were requested and emitted.
	Words []Word `json:"words,omitempty"`
	// Model echoes the configured ASR_MODEL so the app can show what answered.
	Model string `json:"model"`
	// DurationMs is the clip length derived from the WAV data chunk.
	DurationMs int64 `json:"durationMs"`
	// NoSpeech marks the silent-clip short-circuit: the model never ran.
	NoSpeech bool `json:"-"`
}

// Runner decodes one WAV clip in the requested language.
type Runner interface {
	Transcribe(ctx context.Context, wav []byte, language string) (Result, error)
	// Ready reports whether the runner's executables and model are reachable
	// (cheap; no inference).
	Ready(ctx context.Context) error
}
