package asr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// CLIConfig is everything CLIRunner needs; config.FromEnv fills it.
type CLIConfig struct {
	// Binary is the whisper-ctranslate2 executable (WHISPER_CLI).
	Binary string
	// Model is passed as --model: a Whisper size, a Hugging Face id or a
	// CTranslate2 model path (ASR_MODEL). Echoed in Result.Model.
	Model string
	// Device, ComputeType, BeamSize and VADFilter are Ilan's values by default
	// (cuda, float16, 5, true).
	Device      string
	ComputeType string
	BeamSize    int
	VADFilter   bool
	// WordTimestamps adds --word_timestamps True and fills Result.Words when
	// the CLI emits segments[].words (verify-on-server).
	WordTimestamps bool
	// ModelDirectory, ModelCache and LocalFilesOnly pass through as Ilan's
	// --model_directory / --model_dir / --local_files_only.
	ModelDirectory string
	ModelCache     string
	LocalFilesOnly bool
	// MaxParallel is the number of concurrent CLI processes (one GPU job by
	// default). Extra requests do not queue: the handler answers 429 busy.
	MaxParallel int
	// MaxAudioSeconds bounds the clip the runner will stage.
	MaxAudioSeconds int
	// SecretEnvKeys are environment variables stripped from the child's
	// environment (the bearer tokens).
	SecretEnvKeys []string
}

// CLIRunner shells out to whisper-ctranslate2. Temp WAV/JSON files live in a
// private per-request directory removed on every exit path; the child never
// sees the API tokens; its output is size-capped.
type CLIRunner struct {
	cfg  CLIConfig
	gate chan struct{}
}

// ErrBusy means every parallel slot is taken; the handler maps it to 429.
var ErrBusy = errors.New("all transcription slots are busy")

// ErrTooLong means the clip exceeds MaxAudioSeconds (413 audio_too_long).
var ErrTooLong = errors.New("audio duration exceeds limit")

// Bounds for the per-request CLI output and temp dir.
const (
	maxResultBytes = 1 << 20
	minSpeechBytes = 3200 // 100 ms — below this the model never runs
	silenceRMS     = 0.001
)

// NewCLIRunner returns a runner with MaxParallel slots (minimum 1).
func NewCLIRunner(cfg CLIConfig) *CLIRunner {
	if cfg.MaxParallel < 1 {
		cfg.MaxParallel = 1
	}
	return &CLIRunner{cfg: cfg, gate: make(chan struct{}, cfg.MaxParallel)}
}

// Config returns the runner's configuration (read-only use).
func (r *CLIRunner) Config() CLIConfig { return r.cfg }

// Ready checks that the executable and, when configured, the model
// directory are reachable by this user. No inference.
func (r *CLIRunner) Ready(context.Context) error {
	if _, err := exec.LookPath(r.cfg.Binary); err != nil {
		return fmt.Errorf("whisper CLI not executable at %s", r.cfg.Binary)
	}
	if r.cfg.ModelDirectory != "" {
		info, err := os.Stat(filepath.Join(r.cfg.ModelDirectory, "model.bin"))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("model.bin not readable under WHISPER_MODEL_DIRECTORY %s", r.cfg.ModelDirectory)
		}
	}
	return nil
}

// requiredFlags are the options the argument vector uses; Check requires
// each one to appear in `--help` so a wrong CLI version fails at startup
// with the flag named instead of at the first request.
var requiredFlags = []string{
	"--model", "--device", "--compute_type", "--language", "--task",
	"--output_dir", "--output_format", "--verbose", "--vad_filter",
	"--condition_on_previous_text", "--beam_size", "--temperature",
	"--temperature_increment_on_fallback",
}

// Check runs `<binary> --help` once and verifies every flag the runner
// passes is supported. Deploy-time guard; also behind `syncd --check`.
func (r *CLIRunner) Check(ctx context.Context) error {
	if err := r.Ready(ctx); err != nil {
		return err
	}
	var help cappedBuffer
	help.max = maxResultBytes
	cmd := command(ctx, r.cfg.Binary, "--help")
	cmd.Env = r.childEnv()
	cmd.Stdout = &help
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s --help failed: %w (run it as the service user to inspect the venv)", r.cfg.Binary, err)
	}
	text := help.String()
	flags := append([]string{}, requiredFlags...)
	if r.cfg.WordTimestamps {
		flags = append(flags, "--word_timestamps")
	}
	if r.cfg.ModelDirectory != "" {
		flags = append(flags, "--model_directory")
	}
	if r.cfg.ModelCache != "" {
		flags = append(flags, "--model_dir")
	}
	if r.cfg.LocalFilesOnly {
		flags = append(flags, "--local_files_only")
	}
	for _, flag := range flags {
		if !strings.Contains(text, flag) {
			return fmt.Errorf("installed whisper-ctranslate2 lacks required option %s", flag)
		}
	}
	return nil
}

// args builds the CLI argument vector. --initial_prompt, --prefix and
// --hotwords are never passed: nothing may bias the decode toward an
// expected answer (ASR is not a grade).
func (r *CLIRunner) args(input, outDir, language string) []string {
	vad := "False"
	if r.cfg.VADFilter {
		vad = "True"
	}
	a := []string{
		input,
		"--model", r.cfg.Model,
		"--device", r.cfg.Device,
		"--compute_type", r.cfg.ComputeType,
		"--language", language,
		"--task", "transcribe",
		"--output_dir", outDir,
		"--output_format", "json",
		"--verbose", "False",
		"--vad_filter", vad,
		"--condition_on_previous_text", "False",
		"--beam_size", strconv.Itoa(r.cfg.BeamSize),
		"--temperature", "0",
		"--temperature_increment_on_fallback", "None",
	}
	if r.cfg.WordTimestamps {
		a = append(a, "--word_timestamps", "True")
	}
	if r.cfg.ModelDirectory != "" {
		a = append(a, "--model_directory", r.cfg.ModelDirectory)
	}
	if r.cfg.ModelCache != "" {
		a = append(a, "--model_dir", r.cfg.ModelCache)
	}
	if r.cfg.LocalFilesOnly {
		a = append(a, "--local_files_only", "True")
	}
	return a
}

// Transcribe validates wav, short-circuits silence, and otherwise runs one
// CLI process in a private temp dir. It never blocks on a busy gate: with
// no free slot it returns ErrBusy at once (the app's cellular timeout is
// short; queueing would only waste it).
func (r *CLIRunner) Transcribe(ctx context.Context, wav []byte, language string) (Result, error) {
	res := Result{Model: r.cfg.Model}
	if !Languages[language] {
		return res, ErrUnsupportedLanguage
	}
	clip, err := ParseWAV(wav)
	if err != nil {
		return res, err
	}
	res.DurationMs = clip.DurationMs
	if r.cfg.MaxAudioSeconds > 0 && len(clip.PCM) > r.cfg.MaxAudioSeconds*BytesPerSecond {
		return res, ErrTooLong
	}
	if len(clip.PCM) < minSpeechBytes || RMS(clip.PCM) < silenceRMS {
		res.NoSpeech = true
		return res, nil
	}

	select {
	case r.gate <- struct{}{}:
		defer func() { <-r.gate }()
	default:
		return res, ErrBusy
	}
	if ctx.Err() != nil {
		return res, ctx.Err()
	}

	dir, err := os.MkdirTemp("", "aflulit-asr-")
	if err != nil {
		return res, errors.New("private transcription workspace unavailable")
	}
	defer os.RemoveAll(dir)

	input := filepath.Join(dir, "utterance.wav")
	// Re-frame the PCM so the CLI sees a canonical header regardless of what
	// extra chunks the upload carried.
	if err := os.WriteFile(input, EncodeWAV(clip.PCM), 0o600); err != nil {
		return res, errors.New("cannot stage transcription audio")
	}
	cmd := command(ctx, r.cfg.Binary, r.args(input, dir, language)...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Env = r.childEnv()
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		return res, errors.New("whisper CLI failed; run the deploy smoke as the service user to inspect CUDA/model setup")
	}
	f, err := os.Open(filepath.Join(dir, "utterance.json"))
	if err != nil {
		return res, errors.New("whisper CLI did not create its JSON result")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxResultBytes+1))
	if err != nil || len(data) > maxResultBytes {
		return res, errors.New("invalid or oversized CLI result")
	}
	return parseResult(data, language, res)
}

// parseResult reads the CLI's JSON and requires its reported language to
// equal the requested one (a decode constraint the CLI must honour). Words
// are optional: a result without segments[].words simply has none.
func parseResult(data []byte, language string, res Result) (Result, error) {
	var raw struct {
		Text     *string `json:"text"`
		Language string  `json:"language"`
		Segments []struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
			Words []struct {
				Word  string  `json:"word"`
				Start float64 `json:"start"`
				End   float64 `json:"end"`
			} `json:"words"`
		} `json:"segments"`
	}
	if json.Unmarshal(data, &raw) != nil || raw.Text == nil || raw.Language != language {
		return res, errors.New("CLI result missing or in the wrong language")
	}
	text := strings.TrimSpace(*raw.Text)
	if !utf8.ValidString(text) {
		return res, errors.New("CLI result is not valid UTF-8")
	}
	res.Text = text
	limit := float64(res.DurationMs)/1000 + 1
	for _, s := range raw.Segments {
		if s.Start < 0 || s.End < s.Start || s.End > limit {
			return res, errors.New("invalid CLI segment bounds")
		}
		for _, w := range s.Words {
			word := strings.TrimSpace(w.Word)
			if word == "" || w.Start < 0 || w.End < w.Start {
				continue
			}
			res.Words = append(res.Words, Word{Word: word, StartMs: int64(w.Start * 1000), EndMs: int64(w.End * 1000)})
		}
	}
	return res, nil
}

// childEnv is the process environment minus the bearer tokens: the CLI
// needs CUDA and the venv, never a secret.
func (r *CLIRunner) childEnv() []string {
	out := []string{}
	for _, kv := range os.Environ() {
		skip := false
		for _, key := range r.cfg.SecretEnvKeys {
			if strings.HasPrefix(kv, key+"=") {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, kv)
		}
	}
	return out
}

type cappedBuffer struct {
	bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		return 0, errors.New("process output exceeds limit")
	}
	return b.Buffer.Write(p)
}
