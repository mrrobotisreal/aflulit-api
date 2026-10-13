package config

import (
	"strings"
	"testing"
)

const goodToken = "0123456789abcdef0123456789abcdef"

func clearASR(t *testing.T) {
	for _, k := range []string{"ASR_ENABLED", "ASR_MODEL", "WHISPER_CLI", "WHISPER_MODEL_DIRECTORY", "WHISPER_MODEL_CACHE",
		"WHISPER_LOCAL_FILES_ONLY", "ASR_DEVICE", "ASR_COMPUTE_TYPE", "ASR_BEAM_SIZE", "ASR_VAD_FILTER",
		"ASR_MAX_AUDIO_SECONDS", "ASR_MAX_UPLOAD_MB", "ASR_TIMEOUT_SECONDS", "ASR_MAX_PARALLEL", "ASR_WORD_TIMESTAMPS", "ASR_TOKEN",
		"SYNCD_ADDR", "SYNCD_DATA_DIR", "SYNCD_RETENTION_DAYS", "SYNCD_MAX_BODY_MB"} {
		t.Setenv(k, "")
	}
}

func TestDefaultsAreBackupOnly(t *testing.T) {
	clearASR(t)
	t.Setenv("SYNCD_TOKEN", goodToken)
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.ASR
	if a.Enabled || a.Model != "large-v3" || a.WhisperCLI != DefaultWhisperCLI || a.Device != "cuda" ||
		a.ComputeType != "float16" || a.BeamSize != 5 || !a.VADFilter || a.MaxAudioSeconds != 60 ||
		a.MaxUploadBytes != 10<<20 || a.TimeoutSeconds != 120 || a.MaxParallel != 1 || a.WordTimestamps || a.Token != "" {
		t.Fatalf("unexpected ASR defaults: %+v", a)
	}
}

func TestModelIsConfigurableNotHardRejected(t *testing.T) {
	clearASR(t)
	t.Setenv("SYNCD_TOKEN", goodToken)
	t.Setenv("ASR_ENABLED", "true")
	t.Setenv("ASR_MODEL", "ivrit-ai/whisper-large-v3-turbo-ct2")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ASR.Enabled || cfg.ASR.Model != "ivrit-ai/whisper-large-v3-turbo-ct2" {
		t.Fatalf("model not honoured: %+v", cfg.ASR)
	}
}

func TestASRTokenSplitRules(t *testing.T) {
	clearASR(t)
	t.Setenv("SYNCD_TOKEN", goodToken)
	t.Setenv("ASR_TOKEN", goodToken)
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "differ") {
		t.Fatalf("same token accepted: %v", err)
	}
	t.Setenv("ASR_TOKEN", "short")
	if _, err := FromEnv(); err == nil {
		t.Fatal("short ASR_TOKEN accepted")
	}
	t.Setenv("ASR_TOKEN", "fedcba9876543210fedcba9876543210")
	cfg, err := FromEnv()
	if err != nil || cfg.ASR.Token != "fedcba9876543210fedcba9876543210" {
		t.Fatalf("distinct ASR_TOKEN refused: %v", err)
	}
}

func TestBoundsRejected(t *testing.T) {
	cases := map[string]string{
		"ASR_MAX_AUDIO_SECONDS": "0",
		"ASR_MAX_UPLOAD_MB":     "33",
		"ASR_TIMEOUT_SECONDS":   "4",
		"ASR_MAX_PARALLEL":      "3",
		"ASR_BEAM_SIZE":         "x",
		"ASR_ENABLED":           "yes-please",
		"ASR_DEVICE":            "tpu",
		"ASR_COMPUTE_TYPE":      "bf16",
		"ASR_MODEL":             " ",
	}
	for key, bad := range cases {
		clearASR(t)
		t.Setenv("SYNCD_TOKEN", goodToken)
		t.Setenv(key, bad)
		if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s=%q: expected an error naming the variable, got %v", key, bad, err)
		}
	}
}
