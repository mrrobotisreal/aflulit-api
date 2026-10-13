// Package config loads the service's configuration from environment
// variables — never from source or flags, so the bearer tokens can live in a
// root-only env file (deploy/README.md) and nothing secret is ever committed.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is everything the service needs to run.
type Config struct {
	// Addr is the listen address (SYNCD_ADDR, default ":8787"). On the home
	// server this can stay a wildcard bind — Tailscale is the only network
	// path in (deploy notes) — or be pinned to the tailnet IP.
	Addr string
	// Token is the required bearer token (SYNCD_TOKEN).
	Token string
	// DataDir is where archives are stored (SYNCD_DATA_DIR, default "./data").
	DataDir string
	// RetentionDailyDays is the ring-buffer daily window
	// (SYNCD_RETENTION_DAYS, default 30 — the Sumrak T20 rule; change only if the
	// app-side rule changes too).
	RetentionDailyDays int
	// MaxBodyBytes caps PUT /backup bodies (SYNCD_MAX_BODY_MB, default 64).
	MaxBodyBytes int64

	// ASR is the transcription endpoint's configuration (T12, ADR-0008 §2).
	ASR ASRConfig
}

// ASRConfig configures POST /v1/transcriptions. With Enabled false the route
// is not registered (404 behind auth) and nothing else here is validated
// beyond parsing, so a backup-only deploy needs none of these variables.
type ASRConfig struct {
	Enabled bool
	// Model is passed to the CLI as --model and echoed in every response
	// (ASR_MODEL, default "large-v3"; a size, Hugging Face id or model path).
	Model string
	// WhisperCLI is the whisper-ctranslate2 executable (WHISPER_CLI).
	WhisperCLI string
	// ModelDirectory / ModelCache / LocalFilesOnly pass through as the CLI's
	// --model_directory / --model_dir / --local_files_only.
	ModelDirectory string
	ModelCache     string
	LocalFilesOnly bool
	// Device / ComputeType / BeamSize / VADFilter: Ilan's values by default.
	Device      string
	ComputeType string
	BeamSize    int
	VADFilter   bool
	// Limits.
	MaxAudioSeconds int
	MaxUploadBytes  int64
	TimeoutSeconds  int
	MaxParallel     int
	WordTimestamps  bool
	// Token, when set, is the only bearer /v1/transcriptions accepts (and the
	// backup routes keep accepting only SYNCD_TOKEN). Empty = one shared token.
	Token string
}

// MinTokenLen guards against an accidentally empty or trivial token; the
// deploy notes generate a 64-hex-char one.
const MinTokenLen = 16

// Defaults for the ASR half (deploy/syncd.env.example documents them).
const (
	DefaultASRModel   = "large-v3"
	DefaultWhisperCLI = "/opt/creatv/whisper-ct2/bin/whisper-ctranslate2"
)

// SecretEnvKeys are the variables that must never reach a child process.
var SecretEnvKeys = []string{"SYNCD_TOKEN", "ASR_TOKEN"}

// FromEnv reads and validates configuration from the environment.
func FromEnv() (Config, error) {
	cfg := Config{
		Addr:               envOr("SYNCD_ADDR", ":8787"),
		Token:              os.Getenv("SYNCD_TOKEN"),
		DataDir:            envOr("SYNCD_DATA_DIR", "./data"),
		RetentionDailyDays: 30,
		MaxBodyBytes:       64 << 20,
	}
	if len(cfg.Token) < MinTokenLen {
		return Config{}, fmt.Errorf("SYNCD_TOKEN must be set (>= %d chars)", MinTokenLen)
	}
	if v := os.Getenv("SYNCD_RETENTION_DAYS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return Config{}, fmt.Errorf("SYNCD_RETENTION_DAYS must be a positive integer, got %q", v)
		}
		cfg.RetentionDailyDays = n
	}
	if v := os.Getenv("SYNCD_MAX_BODY_MB"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return Config{}, fmt.Errorf("SYNCD_MAX_BODY_MB must be a positive integer, got %q", v)
		}
		cfg.MaxBodyBytes = int64(n) << 20
	}

	asr, err := asrFromEnv(cfg.Token)
	if err != nil {
		return Config{}, err
	}
	cfg.ASR = asr
	return cfg, nil
}

func asrFromEnv(syncdToken string) (ASRConfig, error) {
	var err error
	a := ASRConfig{
		Model:          envOr("ASR_MODEL", DefaultASRModel),
		WhisperCLI:     envOr("WHISPER_CLI", DefaultWhisperCLI),
		ModelDirectory: os.Getenv("WHISPER_MODEL_DIRECTORY"),
		ModelCache:     os.Getenv("WHISPER_MODEL_CACHE"),
		Device:         envOr("ASR_DEVICE", "cuda"),
		ComputeType:    envOr("ASR_COMPUTE_TYPE", "float16"),
		Token:          os.Getenv("ASR_TOKEN"),
	}
	if a.Enabled, err = boolEnv("ASR_ENABLED", false); err != nil {
		return a, err
	}
	if a.LocalFilesOnly, err = boolEnv("WHISPER_LOCAL_FILES_ONLY", false); err != nil {
		return a, err
	}
	if a.VADFilter, err = boolEnv("ASR_VAD_FILTER", true); err != nil {
		return a, err
	}
	if a.WordTimestamps, err = boolEnv("ASR_WORD_TIMESTAMPS", false); err != nil {
		return a, err
	}
	if a.BeamSize, err = intEnv("ASR_BEAM_SIZE", 5, 1, 10); err != nil {
		return a, err
	}
	if a.MaxAudioSeconds, err = intEnv("ASR_MAX_AUDIO_SECONDS", 60, 1, 120); err != nil {
		return a, err
	}
	uploadMB, err := intEnv("ASR_MAX_UPLOAD_MB", 10, 1, 32)
	if err != nil {
		return a, err
	}
	a.MaxUploadBytes = int64(uploadMB) << 20
	if a.TimeoutSeconds, err = intEnv("ASR_TIMEOUT_SECONDS", 120, 5, 150); err != nil {
		return a, err
	}
	if a.MaxParallel, err = intEnv("ASR_MAX_PARALLEL", 1, 1, 2); err != nil {
		return a, err
	}
	if strings.TrimSpace(a.Model) == "" {
		return a, errors.New("ASR_MODEL must not be blank")
	}
	if strings.TrimSpace(a.WhisperCLI) == "" {
		return a, errors.New("WHISPER_CLI must not be blank")
	}
	switch a.Device {
	case "cuda", "cpu", "auto":
	default:
		return a, fmt.Errorf("ASR_DEVICE must be cuda, cpu or auto, got %q", a.Device)
	}
	switch a.ComputeType {
	case "float16", "int8_float16", "int8", "float32", "default":
	default:
		return a, fmt.Errorf("ASR_COMPUTE_TYPE must be one of float16, int8_float16, int8, float32, default; got %q", a.ComputeType)
	}
	if a.Token != "" {
		if len(a.Token) < MinTokenLen {
			return a, fmt.Errorf("ASR_TOKEN, when set, must be >= %d chars", MinTokenLen)
		}
		if a.Token == syncdToken {
			return a, errors.New("ASR_TOKEN must differ from SYNCD_TOKEN (leave it unset to share one token)")
		}
	}
	return a, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func boolEnv(key string, fallback bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false, got %q", key, v)
	}
	return b, nil
}

func intEnv(key string, fallback, min, max int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s must be an integer in [%d, %d], got %q", key, min, max, v)
	}
	return n, nil
}
