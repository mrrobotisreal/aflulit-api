// Package config loads syncd's configuration from environment variables —
// never from source or flags, so the bearer token can live in a root-only
// env file (deploy/README.md) and nothing secret is ever committed.
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config is everything syncd needs to run.
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
	// (SYNCD_RETENTION_DAYS, default 30 — the T20 rule; change only if the
	// app-side rule changes too).
	RetentionDailyDays int
	// MaxBodyBytes caps PUT /backup bodies (SYNCD_MAX_BODY_MB, default 64).
	MaxBodyBytes int64
}

// MinTokenLen guards against an accidentally empty or trivial token; the
// deploy notes generate a 64-hex-char one.
const MinTokenLen = 16

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
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
