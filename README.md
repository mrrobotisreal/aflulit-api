# AflulitAPI — `syncd` + Hebrew transcription

The Go backend for [Aflulit](https://github.com/mrrobotisreal/aflulit)
(«אַפְלוּלִית», Mitch's personal Hebrew-learning app). It is a
**history-preserving fork of `SumrakAPI@0142268`** (ADR-0001, ADR-0008):
the `syncd` backup service renamed for Aflulit, with the Hebrew speech-to-text
endpoint added on top.

`syncd` runs on the home server, is reachable only over Tailscale, and stores
the app's **encrypted** backup snapshots and scenario media bundles as opaque
blobs. It never decrypts anything — the passphrase and derived key exist only
on the phone.

## Endpoints

| Route | Purpose |
|---|---|
| `PUT /backup?name=<file>` | Store one archive (create-only; 409 on duplicate). Runs retention afterwards. |
| `GET /backups` | List stored archives `{name, size, timestampMs}`, newest first (media bundles last). |
| `GET /backup/{name}` | Return one archive verbatim. |
| `POST /v1/transcriptions` | Whisper transcription (T12, ADR-0008 §2). Multipart `audio` = 16 kHz mono 16-bit WAV, optional `language` = `he` (default) or `en`. Returns `{text, words?, model, durationMs, language}`. Registered only with `ASR_ENABLED=true`. |

Every route (including unknown paths) requires `Authorization: Bearer
<token>`; there is deliberately no unauthenticated endpoint, not even a
health check — Tailscale reachability plus an authed `GET /backups` is the
health check.

Transcription errors: `400 audio_file_required` / `unsupported_language`,
`415 unsupported_audio_format`, `413 audio_too_long` / `invalid_audio_size`,
`429 busy` (retryable), `503 transcription_unavailable` (retryable). Silence
is `200` with `text:""`. ASR returns text only — never a grade (ADR-0007 §4);
Hebrew normalisation and matching happen in the app. `language=en` exists
for the scenario «איך אומרים …» re-decode of the same clip.

Archive names are the contract shared with the app and the GitHub target
(ADR-0002): `aflulit-backup-YYYYMMDD-HHMMSSZ.json` (UTC) for snapshots and
`aflulit-media-<runId>.json` for media bundles. The name is the id.
Retention is the ring buffer inherited from Sumrak T20: keep every backup
from the 30 newest distinct UTC dates, plus the latest per older calendar
month; media bundles are never pruned.

## Layout

```
cmd/syncd/          entrypoint
internal/config/    env-var configuration (SYNCD_TOKEN, SYNCD_ADDR, SYNCD_DATA_DIR, …)
internal/auth/      bearer-token middleware (constant-time)
internal/naming/    backup file-name parsing (shared contract with the app)
internal/retention/ ring-buffer retention, ported from the app
internal/storage/   disk blob store (atomic create-only writes)
internal/server/    routing + handlers (stdlib net/http); transcribe.go = the ASR route
internal/asr/       Whisper runner: whisper-ctranslate2 CLI, pure-Go WAV validation
deploy/             systemd unit, env template, deploy walkthrough
```

Router: stdlib `net/http` — Go 1.22+ method/path patterns; zero third-party
dependencies (`go.mod` has no `require` block, and stays that way).

## Develop

```sh
make check       # gofmt (fails on unformatted files) + go vet + go test
make build       # → bin/syncd
make linux       # → bin/syncd-linux-amd64   (make linux-arm64 for ARM boxes)

SYNCD_TOKEN=$(openssl rand -hex 32) SYNCD_DATA_DIR=./data go run ./cmd/syncd
```

Configuration is env-vars only (see `deploy/syncd.env.example`); no secret is
ever committed or logged. `syncd --check` validates the configuration and,
with ASR enabled, probes the CLI's `--help` for every flag the runner uses —
no inference. Tests use a shell-script fake CLI: no weights are ever
downloaded or run on a development machine.

### Environment

| Variable | Default | Notes |
|---|---|---|
| `SYNCD_TOKEN` | — (required) | bearer, ≥ 16 chars |
| `SYNCD_ADDR` | `:8787` | |
| `SYNCD_DATA_DIR` | `./data` | `/var/lib/syncd` under systemd |
| `SYNCD_RETENTION_DAYS` | `30` | ring-buffer daily window |
| `SYNCD_MAX_BODY_MB` | `64` | `PUT /backup` cap |
| `ASR_ENABLED` | `false` | `false` leaves `/v1/transcriptions` unregistered |
| `ASR_MODEL` | `large-v3` | any size, HF id or model path; echoed as `model` |
| `WHISPER_CLI` | `/opt/creatv/whisper-ct2/bin/whisper-ctranslate2` | |
| `WHISPER_MODEL_DIRECTORY` / `WHISPER_MODEL_CACHE` / `WHISPER_LOCAL_FILES_ONLY` | unset / unset / `false` | pass-through |
| `ASR_DEVICE` / `ASR_COMPUTE_TYPE` / `ASR_BEAM_SIZE` / `ASR_VAD_FILTER` | `cuda` / `float16` / `5` / `true` | bounded |
| `ASR_MAX_AUDIO_SECONDS` | `60` | 1–120 |
| `ASR_MAX_UPLOAD_MB` | `10` | 1–32 |
| `ASR_TIMEOUT_SECONDS` | `120` | 5–150 |
| `ASR_MAX_PARALLEL` | `1` | 1–2; extra requests get `429 busy` |
| `ASR_WORD_TIMESTAMPS` | `false` | adds `--word_timestamps True` |
| `ASR_TOKEN` | unset | optional second bearer for the ASR route only (≠ `SYNCD_TOKEN`) | `.env` and `*firebase-adminsdk*.json` are gitignored
and unused (ADR-0008 §3).

## Deploy

See [`deploy/README.md`](deploy/README.md) — build, systemd install,
Tailscale notes, reboot-survival verification. Deploying is Mitch's step;
agents never touch the home server.

## Fork point

`SumrakAPI@0142268` (its `syncd`, Sumrak T21/T20/T63) merged with
`--allow-unrelated-histories` on 2026-10-13 (T11). Later SumrakAPI fixes are
cherry-picked, never re-implemented (ADR-0001 §3).
