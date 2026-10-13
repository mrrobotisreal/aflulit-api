// syncd is Aflulit's home-server service (ADR-0008): it accepts encrypted
// backup archives from the app over Tailscale, lists them, serves them back
// for restore, and applies the Sumrak T20 ring-buffer retention rule. It never
// decrypts anything — archives are opaque blobs, and neither the passphrase
// nor any key ever reaches this process. With ASR_ENABLED=true it also
// serves POST /v1/transcriptions (Whisper he/en through whisper-ctranslate2).
//
// `syncd --check` validates the configuration and, when ASR is enabled, the
// installed CLI, then exits — a deploy-time probe that runs no inference.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mrrobotisreal/aflulit-api/internal/asr"
	"github.com/mrrobotisreal/aflulit-api/internal/config"
	"github.com/mrrobotisreal/aflulit-api/internal/server"
	"github.com/mrrobotisreal/aflulit-api/internal/storage"
)

func main() {
	checkOnly := flag.Bool("check", false, "validate configuration and the whisper CLI, then exit")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := config.FromEnv()
	if err != nil {
		log.Error("bad configuration", "err", err)
		os.Exit(1)
	}

	var asrOpts *server.ASROptions
	if cfg.ASR.Enabled {
		runner := asr.NewCLIRunner(asr.CLIConfig{
			Binary:          cfg.ASR.WhisperCLI,
			Model:           cfg.ASR.Model,
			Device:          cfg.ASR.Device,
			ComputeType:     cfg.ASR.ComputeType,
			BeamSize:        cfg.ASR.BeamSize,
			VADFilter:       cfg.ASR.VADFilter,
			WordTimestamps:  cfg.ASR.WordTimestamps,
			ModelDirectory:  cfg.ASR.ModelDirectory,
			ModelCache:      cfg.ASR.ModelCache,
			LocalFilesOnly:  cfg.ASR.LocalFilesOnly,
			MaxParallel:     cfg.ASR.MaxParallel,
			MaxAudioSeconds: cfg.ASR.MaxAudioSeconds,
			SecretEnvKeys:   config.SecretEnvKeys,
		})
		// Fail fast with the missing flag named rather than at the first request.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := runner.Check(ctx)
		cancel()
		if err != nil {
			log.Error("whisper CLI check failed", "err", err, "cli", cfg.ASR.WhisperCLI, "model", cfg.ASR.Model)
			os.Exit(1)
		}
		log.Info("asr ready", "cli", cfg.ASR.WhisperCLI, "model", cfg.ASR.Model, "device", cfg.ASR.Device,
			"maxParallel", cfg.ASR.MaxParallel, "maxAudioSeconds", cfg.ASR.MaxAudioSeconds, "splitToken", cfg.ASR.Token != "")
		asrOpts = &server.ASROptions{
			Runner:          runner,
			Token:           cfg.ASR.Token,
			MaxUploadBytes:  cfg.ASR.MaxUploadBytes,
			MaxAudioSeconds: cfg.ASR.MaxAudioSeconds,
			Timeout:         time.Duration(cfg.ASR.TimeoutSeconds) * time.Second,
		}
	}

	if *checkOnly {
		fmt.Printf("ok: configuration valid; asr enabled=%v\n", cfg.ASR.Enabled)
		return
	}

	store, err := storage.New(cfg.DataDir, cfg.RetentionDailyDays)
	if err != nil {
		log.Error("storage init failed", "err", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.New(store, server.Options{Token: cfg.Token, MaxBodyBytes: cfg.MaxBodyBytes, Log: log, ASR: asrOpts}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("syncd listening",
			"addr", cfg.Addr,
			"dataDir", cfg.DataDir,
			"retentionDays", cfg.RetentionDailyDays,
			"maxBodyBytes", cfg.MaxBodyBytes,
			"asr", cfg.ASR.Enabled,
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Warn("shutdown incomplete", "err", err)
	}
}
