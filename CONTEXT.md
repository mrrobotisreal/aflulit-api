# AflulitAPI — Repo Glossary

Repo-local terms for the Go backend of Aflulit («אַפְלוּלִית»), Mitch's Hebrew-
learning app. The service is a history-preserving fork of `SumrakAPI@0142268`
(ADR-0001, ADR-0008): v1 scope = `syncd` (backups) plus, from T12, the Hebrew
transcription endpoint. Cross-repo language lives in `docs-aflulit/`.

## Language

**Archive / blob**:
What syncd stores — one backup snapshot file, already gzip+AES-256-GCM
encrypted by the app. Opaque to the server: syncd never decrypts, never
holds a key or passphrase (server compromise costs availability, not data).
_Avoid_: backup contents, payload (that's the app-side decrypted form)

**Backup name**:
The file-name contract `aflulit-backup-YYYYMMDD-HHMMSSZ.json` (UTC), ADR-0002.
The name is the archive's id in every endpoint, embeds the snapshot moment
(retention reasons from names alone, clock-independent), and doubles as the
path-traversal guard — anything not matching is rejected everywhere.

**Media bundle**:
`aflulit-media-<runId>.json` — one immutable bundle per finished scenario
run (or exam attempt, stem `exam-<attemptid>`). Stored, listed and served
like a snapshot, but never pruned.

**Ring buffer (retention)**:
The rule inherited from Sumrak T20, ported to Go: keep every archive from
the 30 newest distinct UTC dates present, plus the latest per older calendar
month; never touch unparseable names. Runs server-side after every
successful `PUT /backup` — the app never prunes this target.

**Bearer token**:
The single shared secret (`SYNCD_TOKEN` env, ≥16 chars) the app presents on
every request. Constant-time compared; every route — unknown paths included
— 401s without it. There is deliberately no unauthenticated endpoint, not
even a health check (an authed `GET /backups` is the health check).

**Public host**:
How the app reaches the service: `https://aflulit.creatv.dev`, a Cloudflare
Tunnel that terminates TLS and forwards to `localhost:8787` on the home
server (2026-10-13; replaced the Tailscale-only reachability inherited from
Sumrak). syncd itself speaks plain HTTP on loopback; the bearer token is the
whole auth model.

**Transcription (T12)**:
`POST /v1/transcriptions` — Whisper `he`/`en` speech-to-text through
`whisper-ctranslate2` on the home server's GPU. Returns text only, never a
grade (ADR-0007 §4). Optional `ASR_TOKEN` splits its bearer from `SYNCD_TOKEN`.
