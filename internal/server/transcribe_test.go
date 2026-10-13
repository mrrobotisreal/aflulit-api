package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrrobotisreal/aflulit-api/internal/asr"
	"github.com/mrrobotisreal/aflulit-api/internal/storage"
)

const asrToken = "asr-token-fedcba9876543210fedcba"

// fakeRunner records what the handler asked for and answers from a script.
type fakeRunner struct {
	calls    atomic.Int32
	lastLang atomic.Value
	delay    time.Duration
	err      error
	result   asr.Result
	busyGate chan struct{}
}

func (f *fakeRunner) Ready(context.Context) error { return nil }
func (f *fakeRunner) Transcribe(ctx context.Context, wav []byte, language string) (asr.Result, error) {
	f.calls.Add(1)
	f.lastLang.Store(language)
	if f.busyGate != nil {
		select {
		case f.busyGate <- struct{}{}:
			defer func() { <-f.busyGate }()
		default:
			return asr.Result{}, asr.ErrBusy
		}
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return asr.Result{}, ctx.Err()
		}
	}
	if f.err != nil {
		return asr.Result{}, f.err
	}
	clip, err := asr.ParseWAV(wav)
	if err != nil {
		return asr.Result{}, err
	}
	res := f.result
	res.DurationMs = clip.DurationMs
	if res.Model == "" {
		res.Model = "large-v3"
	}
	return res, nil
}

func asrHandler(t *testing.T, r asr.Runner, split string) http.Handler {
	t.Helper()
	store, err := storage.New(t.TempDir(), 30)
	if err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(store, Options{Token: token, MaxBodyBytes: 1 << 20, Log: quiet, ASR: &ASROptions{
		Runner: r, Token: split, MaxUploadBytes: 128 << 10, MaxAudioSeconds: 2, Timeout: 300 * time.Millisecond,
	}})
}

func pcm(sec float64, amp int16) []byte {
	n := int(sec * asr.BytesPerSecond)
	n -= n % 2
	b := make([]byte, n)
	for i := 0; i < n; i += 2 {
		b[i] = byte(amp)
		b[i+1] = byte(amp >> 8)
	}
	return b
}

func voice() []byte { return asr.EncodeWAV(pcm(0.5, 800)) }

type part struct{ field, filename, value string }

func post(h http.Handler, bearer string, parts ...part) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, p := range parts {
		if p.filename != "" {
			w, _ := mw.CreateFormFile(p.field, p.filename)
			_, _ = w.Write([]byte(p.value))
		} else {
			_ = mw.WriteField(p.field, p.value)
		}
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, TranscriptionsPath, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func audio(b []byte) part { return part{"audio", "clip.wav", string(b)} }

func errorCode(rec *httptest.ResponseRecorder) string {
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	s, _ := m["error"].(string)
	return s
}

func TestRouteAbsentWhenASRDisabled(t *testing.T) {
	h := newHandler(t) // no ASR options
	rec := post(h, token, audio(voice()))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled route: expected 404, got %d", rec.Code)
	}
	if rec := post(h, "", audio(voice())); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled route still behind auth: got %d", rec.Code)
	}
}

func TestAuthOnTranscriptions(t *testing.T) {
	f := &fakeRunner{result: asr.Result{Text: "שלום"}}
	shared := asrHandler(t, f, "")
	if rec := post(shared, "", audio(voice())); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	if rec := post(shared, token, audio(voice())); rec.Code != http.StatusOK {
		t.Fatalf("shared token: %d %s", rec.Code, rec.Body)
	}
	split := asrHandler(t, f, asrToken)
	if rec := post(split, token, audio(voice())); rec.Code != http.StatusUnauthorized {
		t.Fatalf("split: SYNCD_TOKEN must not open ASR, got %d", rec.Code)
	}
	if rec := post(split, asrToken, audio(voice())); rec.Code != http.StatusOK {
		t.Fatalf("split: ASR token refused, got %d", rec.Code)
	}
	if rec := do(split, http.MethodGet, "/backups", "", false); rec.Code != http.StatusUnauthorized {
		t.Fatal("backups without token")
	}
	req := httptest.NewRequest(http.MethodGet, "/backups", nil)
	req.Header.Set("Authorization", "Bearer "+asrToken)
	rec := httptest.NewRecorder()
	split.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("split: ASR token must not open backups, got %d", rec.Code)
	}
}

func TestLanguageHandling(t *testing.T) {
	f := &fakeRunner{result: asr.Result{Text: "x"}}
	h := asrHandler(t, f, "")
	cases := []struct {
		lang, wantLang string
		code           int
	}{
		{"", "he", 200}, {"he", "he", 200}, {"en", "en", 200}, {" EN ", "en", 200}, {"He", "he", 200},
		{"fr", "", 400}, {"ru", "", 400}, {"hebrew", "", 400},
	}
	for _, c := range cases {
		parts := []part{audio(voice())}
		if c.lang != "" {
			parts = append(parts, part{"language", "", c.lang})
		}
		rec := post(h, token, parts...)
		if rec.Code != c.code {
			t.Errorf("language %q: expected %d, got %d (%s)", c.lang, c.code, rec.Code, rec.Body)
			continue
		}
		if c.code == 400 && errorCode(rec) != "unsupported_language" {
			t.Errorf("language %q: body %s", c.lang, rec.Body)
		}
		if c.code == 200 {
			if f.lastLang.Load() != c.wantLang {
				t.Errorf("language %q: runner saw %v, want %s", c.lang, f.lastLang.Load(), c.wantLang)
			}
			var resp transcriptionResponse
			_ = json.Unmarshal(rec.Body.Bytes(), &resp)
			if resp.Language != c.wantLang {
				t.Errorf("language %q: response echoed %q", c.lang, resp.Language)
			}
		}
	}
}

func TestInputErrors(t *testing.T) {
	f := &fakeRunner{result: asr.Result{Text: "x"}}
	h := asrHandler(t, f, "")
	check := func(label string, rec *httptest.ResponseRecorder, code int, errCode string) {
		t.Helper()
		if rec.Code != code || errorCode(rec) != errCode {
			t.Errorf("%s: got %d %s, want %d %s", label, rec.Code, rec.Body, code, errCode)
		}
	}
	check("no audio part", post(h, token, part{"language", "", "he"}), 400, "audio_file_required")
	check("empty audio", post(h, token, audio(nil)), 413, "invalid_audio_size")
	check("not WAV", post(h, token, audio([]byte("OggS"+strings.Repeat("x", 100)))), 415, "unsupported_audio_format")
	check("stereo", post(h, token, audio(stereo())), 415, "unsupported_audio_format")
	check("too long", post(h, token, audio(asr.EncodeWAV(pcm(2.1, 800)))), 413, "audio_too_long")
	req := httptest.NewRequest(http.MethodPost, TranscriptionsPath, strings.NewReader("not multipart"))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	check("not multipart", rec, 400, "audio_file_required")
	if f.calls.Load() != 0 {
		t.Fatalf("runner must not run on any input error, ran %d times", f.calls.Load())
	}
}

func stereo() []byte {
	b := asr.EncodeWAV(pcm(0.5, 800))
	b[22] = 2
	return b
}

func TestOversizeUploadIs413(t *testing.T) {
	f := &fakeRunner{}
	h := asrHandler(t, f, "")
	big := asr.EncodeWAV(pcm(4.5, 800)) // 144,044 B > the 128 KiB test cap (size is checked before duration)
	rec := post(h, token, audio(big))
	if rec.Code != 413 || errorCode(rec) != "invalid_audio_size" {
		t.Fatalf("oversize: %d %s", rec.Code, rec.Body)
	}
	if f.calls.Load() != 0 {
		t.Fatal("runner ran on oversize input")
	}
}

func TestSuccessShapeAndSilence(t *testing.T) {
	f := &fakeRunner{result: asr.Result{Text: "שלום עולם", Model: "ivrit-ai/whisper-large-v3-turbo-ct2",
		Words: []asr.Word{{Word: "שלום", StartMs: 0, EndMs: 250}}}}
	rec := post(asrHandler(t, f, ""), token, audio(voice()))
	want := `{"text":"שלום עולם","words":[{"word":"שלום","startMs":0,"endMs":250}],"model":"ivrit-ai/whisper-large-v3-turbo-ct2","durationMs":500,"language":"he"}`
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("response golden:\n got %s\nwant %s", rec.Body, want)
	}
	silent := &fakeRunner{result: asr.Result{NoSpeech: true}}
	rec = post(asrHandler(t, silent, ""), token, audio(voice()))
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"text":"","model":"large-v3","durationMs":500,"language":"he"}` {
		t.Fatalf("silence: %d %s", rec.Code, rec.Body)
	}
}

func TestBusyTimeoutAndFailure(t *testing.T) {
	busy := &fakeRunner{err: asr.ErrBusy}
	rec := post(asrHandler(t, busy, ""), token, audio(voice()))
	if rec.Code != 429 || !strings.Contains(rec.Body.String(), `"error":"busy"`) || !strings.Contains(rec.Body.String(), `"retryable":true`) {
		t.Fatalf("busy: %d %s", rec.Code, rec.Body)
	}
	slow := &fakeRunner{delay: 2 * time.Second, result: asr.Result{Text: "late"}}
	start := time.Now()
	rec = post(asrHandler(t, slow, ""), token, audio(voice()))
	if rec.Code != 503 || errorCode(rec) != "transcription_unavailable" || time.Since(start) > time.Second {
		t.Fatalf("timeout: %d %s after %v", rec.Code, rec.Body, time.Since(start))
	}
	failing := &fakeRunner{err: errors.New("cuda exploded")}
	rec = post(asrHandler(t, failing, ""), token, audio(voice()))
	if rec.Code != 503 || strings.Contains(rec.Body.String(), "cuda") {
		t.Fatalf("failure must be 503 without internals: %d %s", rec.Code, rec.Body)
	}
}

func TestBusyUnderOneParallelSlot(t *testing.T) {
	f := &fakeRunner{delay: 200 * time.Millisecond, result: asr.Result{Text: "x"}, busyGate: make(chan struct{}, 1)}
	h := asrHandler(t, f, "")
	first := make(chan int)
	go func() { first <- post(h, token, audio(voice())).Code }()
	time.Sleep(50 * time.Millisecond)
	second := post(h, token, audio(voice()))
	if second.Code != 429 {
		t.Fatalf("second concurrent request: expected 429, got %d", second.Code)
	}
	if code := <-first; code != 200 {
		t.Fatalf("first request: expected 200, got %d", code)
	}
}
