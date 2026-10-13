package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mrrobotisreal/aflulit-api/internal/asr"
)

// transcriptionResponse is the wire shape T14 consumes (ADR-0008 §2):
// asr.Result plus the decode language that was applied.
type transcriptionResponse struct {
	Text       string     `json:"text"`
	Words      []asr.Word `json:"words,omitempty"`
	Model      string     `json:"model"`
	DurationMs int64      `json:"durationMs"`
	Language   string     `json:"language"`
}

// handleTranscribe implements POST /v1/transcriptions. It never logs the
// transcript, the audio or any token — only request id, language, bytes,
// durationMs, wall ms and outcome.
func (s *server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	o := s.asr
	started := time.Now()
	reqID := newRequestID()
	outcome := func(status int, code string, lang string, bytes int, durationMs int64) {
		s.log.Info("transcription",
			"reqId", reqID, "status", status, "outcome", code, "language", lang,
			"bytes", bytes, "durationMs", durationMs, "wallMs", time.Since(started).Milliseconds())
	}

	// Multipart overhead beyond the audio itself gets a small allowance.
	r.Body = http.MaxBytesReader(w, r.Body, o.MaxUploadBytes+64<<10)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			outcome(http.StatusRequestEntityTooLarge, "invalid_audio_size", "", 0, 0)
			s.respond(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "invalid_audio_size"})
			return
		}
		outcome(http.StatusBadRequest, "audio_file_required", "", 0, 0)
		s.respond(w, http.StatusBadRequest, map[string]any{"error": "audio_file_required"})
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, _, err := r.FormFile("audio")
	if err != nil {
		outcome(http.StatusBadRequest, "audio_file_required", "", 0, 0)
		s.respond(w, http.StatusBadRequest, map[string]any{"error": "audio_file_required"})
		return
	}
	defer file.Close()

	language := strings.ToLower(strings.TrimSpace(r.FormValue("language")))
	if language == "" {
		language = "he"
	}
	if !asr.Languages[language] {
		outcome(http.StatusBadRequest, "unsupported_language", "", 0, 0)
		s.respond(w, http.StatusBadRequest, map[string]any{"error": "unsupported_language"})
		return
	}

	data, err := io.ReadAll(io.LimitReader(file, o.MaxUploadBytes+1))
	if err != nil || len(data) == 0 || int64(len(data)) > o.MaxUploadBytes {
		outcome(http.StatusRequestEntityTooLarge, "invalid_audio_size", language, len(data), 0)
		s.respond(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "invalid_audio_size"})
		return
	}
	clip, err := asr.ParseWAV(data)
	if err != nil {
		outcome(http.StatusUnsupportedMediaType, "unsupported_audio_format", language, len(data), 0)
		s.respond(w, http.StatusUnsupportedMediaType, map[string]any{"error": "unsupported_audio_format"})
		return
	}
	if clip.DurationMs > int64(o.MaxAudioSeconds)*1000 {
		outcome(http.StatusRequestEntityTooLarge, "audio_too_long", language, len(data), clip.DurationMs)
		s.respond(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "audio_too_long", "maxSeconds": o.MaxAudioSeconds})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), o.Timeout)
	defer cancel()
	res, err := o.Runner.Transcribe(ctx, data, language)
	if err != nil {
		switch {
		case errors.Is(err, asr.ErrBusy):
			outcome(http.StatusTooManyRequests, "busy", language, len(data), clip.DurationMs)
			s.respond(w, http.StatusTooManyRequests, map[string]any{"error": "busy", "retryable": true})
		case errors.Is(err, asr.ErrTooLong):
			outcome(http.StatusRequestEntityTooLarge, "audio_too_long", language, len(data), clip.DurationMs)
			s.respond(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "audio_too_long", "maxSeconds": o.MaxAudioSeconds})
		case errors.Is(err, asr.ErrUnsupportedAudio):
			outcome(http.StatusUnsupportedMediaType, "unsupported_audio_format", language, len(data), 0)
			s.respond(w, http.StatusUnsupportedMediaType, map[string]any{"error": "unsupported_audio_format"})
		default:
			// Timeout, cancelled or CLI failure: the app falls through to its
			// on-device recognizer. The reason stays in the log, not the body.
			s.log.Warn("transcription failed", "reqId", reqID, "err", err)
			outcome(http.StatusServiceUnavailable, "transcription_unavailable", language, len(data), clip.DurationMs)
			s.respond(w, http.StatusServiceUnavailable, map[string]any{"error": "transcription_unavailable", "retryable": true})
		}
		return
	}
	code := "transcribed"
	if res.NoSpeech {
		code = "no_speech"
	}
	outcome(http.StatusOK, code, language, len(data), res.DurationMs)
	s.respond(w, http.StatusOK, transcriptionResponse{
		Text: res.Text, Words: res.Words, Model: res.Model, DurationMs: res.DurationMs, Language: language,
	})
}

func newRequestID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "r-" + hex.EncodeToString([]byte(time.Now().Format("150405.000")))
	}
	return hex.EncodeToString(b[:])
}
