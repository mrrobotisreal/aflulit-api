# Deploying syncd to the home server

> **Who deploys:** Mitch. This is a `ready-for-human` step — agents never SSH,
> `scp` or touch the home server. Everything below is meant to be followed
> verbatim.

`syncd` listens on plain HTTP on the home server (`:8787`) and is reached
from the app **over HTTPS at `https://aflulit.creatv.dev`**, a Cloudflare
Tunnel that terminates TLS at Cloudflare and forwards to `localhost:8787` on
the server (Mitch, 2026-10-13; this replaces the Tailscale-only reachability
inherited from Sumrak — no Tailscale is involved anywhere). The bearer token
is the whole auth model: every route requires it, including unknown paths.
(The archives are additionally AES-256-GCM-encrypted end-to-end by the app
before they ever leave the phone — syncd stores opaque blobs and never holds
a key.)

## Prerequisites

- A Linux home server with systemd and a `cloudflared` tunnel whose public
  hostname `aflulit.creatv.dev` routes to `http://localhost:8787`.
- Go **not** required on the server if you cross-compile (step 1b).

## 1. Build

On the server (with Go ≥ 1.24):

```sh
git clone git@github.com:mrrobotisreal/aflulit-api.git && cd aflulit-api
go build -o syncd ./cmd/syncd
```

Or cross-compile from the Mac and copy the binary over (pick the arch that
matches the server — `uname -m` there; `x86_64` → `amd64`, `aarch64` → `arm64`):

```sh
make linux          # bin/syncd-linux-amd64   (or: make linux-arm64)
scp bin/syncd-linux-amd64 <server>:/tmp/syncd
```

## 2. Install

```sh
sudo useradd --system --home /var/lib/syncd --shell /usr/sbin/nologin syncd
sudo install -m 755 /tmp/syncd /usr/local/bin/syncd

sudo mkdir -p /etc/syncd
sudo cp deploy/syncd.env.example /etc/syncd/syncd.env
sudo chmod 600 /etc/syncd/syncd.env
# Edit /etc/syncd/syncd.env: set SYNCD_TOKEN to `openssl rand -hex 32` output.

sudo cp deploy/syncd.service /etc/systemd/system/syncd.service
sudo systemctl daemon-reload
sudo systemctl enable --now syncd
```

`enable --now` both starts it and registers it for boot — that is the
reboot-survival requirement; verify with a real reboot below.

## 3. Verify

```sh
systemctl status syncd            # active (running)
journalctl -u syncd -f            # logs (stdout goes to the journal)

# From anywhere (token from /etc/syncd/syncd.env; `read -rs TOKEN` first):
curl -H "Authorization: Bearer $TOKEN" https://aflulit.creatv.dev/backups
# → {"backups":[]}
curl https://aflulit.creatv.dev/backups
# → 401 {"error":"unauthorized"}

# Reboot survival:
sudo reboot
# ...after it's back:
systemctl status syncd            # active (running) again, no manual step
```

The app is built with this host and token baked in (`Aflulit/.env`
`SYNCD_TOKEN`, host `https://aflulit.creatv.dev`), so nothing is typed on the
phone; **Settings → Backup → Home server (syncd)** still shows the target and
lets you override it.

## 4. ASR — `POST /v1/transcriptions` (T12, ADR-0008 §2)

The transcription endpoint shells out to `whisper-ctranslate2` on the server's
GPU. It is **off by default** (`ASR_ENABLED=false` → the route is not
registered and a backup-only box needs nothing from this section).

### 4.1 Prerequisites on the server

- The existing `whisper-ctranslate2` venv (Ilan's lives at
  `/opt/creatv/whisper-ct2`; `WHISPER_CLI` points at its `bin/whisper-ctranslate2`).
- The model weights, either CLI-managed (first run downloads into
  `WHISPER_MODEL_CACHE`) or a local CTranslate2 directory passed as
  `WHISPER_MODEL_DIRECTORY` (must contain `model.bin`).
- Both **outside `/home`**, or allow-listed in the unit — `ProtectHome=true`
  hides `/home` from the service entirely.
- The `syncd` user in the GPU groups (`SupplementaryGroups=video render`) and
  able to read the venv.

### 4.2 Binary, env + unit

0. **Reinstall the binary first.** `make linux` only writes
   `bin/syncd-linux-amd64` into the checkout; `/usr/local/bin/syncd` keeps
   whatever was installed in §2 until you copy over it again. A binary
   without ASR ignores every `ASR_*` variable silently — its `syncd
   listening` log line has **no `asr=` field**, and
   `strings /usr/local/bin/syncd | grep -c v1/transcriptions` prints `0`.

   ```sh
   make linux    # or: make linux-arm64 when `uname -m` on the server is aarch64
   sudo install -m 755 bin/syncd-linux-amd64 /usr/local/bin/syncd
   strings /usr/local/bin/syncd | grep -c v1/transcriptions   # ≥ 1
   ```

1. Append the ASR block from `deploy/syncd.env.example` to
   `/etc/syncd/syncd.env`; set `ASR_ENABLED=true`, `WHISPER_CLI`, and pick
   `ASR_MODEL`:
   - `large-v3` (default, multilingual), or
   - **the ivrit-ai turbo fine-tune (recommended, Apache-2.0)** — the CLI's
     `--model` only accepts its own size names, so clone the CTranslate2
     build once and point the directory variable at it:
     ```sh
     sudo mkdir -p /opt/creatv/models && cd /opt/creatv/models
     sudo git lfs install && sudo git clone https://huggingface.co/ivrit-ai/whisper-large-v3-turbo-ct2
     sudo chown -R syncd:syncd whisper-large-v3-turbo-ct2
     ```
     then `ASR_MODEL=large-v3-turbo` and
     `WHISPER_MODEL_DIRECTORY=/opt/creatv/models/whisper-large-v3-turbo-ct2`
     (add `/opt/creatv` to `ReadOnlyPaths=` if the unit lists any). Responses
     then report `"model":"whisper-large-v3-turbo-ct2"`.
   - `ASR_COMPUTE_TYPE=int8` — `float16` fails on the home server's GPU.
2. Uncomment the "ASR additions" in `deploy/syncd.service` (groups, read-only
   venv + cache paths, `TMPDIR` on a disk with room, `MemoryMax`), then
   `sudo mkdir -p /var/tmp/syncd-asr && sudo chown syncd:syncd /var/tmp/syncd-asr`.
3. Validate without a request — this runs `--help` on the CLI as the service
   user and names any missing flag, but runs **no inference**:

   ```sh
   sudo -u syncd env $(sudo cat /etc/syncd/syncd.env | grep -v '^#' | xargs) /usr/local/bin/syncd --check
   # → ok: configuration valid; asr enabled=true
   ```

4. `sudo systemctl daemon-reload && sudo systemctl restart syncd`, then
   `journalctl -u syncd -n 20` should show `asr ready … model=…`.

### 4.3 Smoke after deploying

A `503 transcription_unavailable` means the CLI process failed; the reason is
in the journal, not the body:

```sh
journalctl -u syncd -n 20 --no-pager | grep 'transcription failed'
# → err="whisper CLI failed (exit status 1): … <last 2 KiB of the CLI's stderr> …"
```

To see the whole thing, run the CLI once by hand **as the service user with
the service's environment and sandbox** (any 16 kHz mono WAV):

```sh
sudo systemd-run --pty --wait --collect --unit=syncd-smoke \
  -p User=syncd -p ProtectSystem=strict -p ProtectHome=true -p PrivateTmp=true \
  -p ReadWritePaths=/var/lib/syncd -p SupplementaryGroups="video render" \
  --setenv=HOME=/var/lib/syncd \
  /opt/creatv/whisper-ct2/bin/whisper-ctranslate2 /var/lib/syncd/smoke.wav \
    --model ivrit-ai/whisper-large-v3-turbo-ct2 --device cuda --compute_type float16 \
    --language he --task transcribe --output_dir /var/lib/syncd --output_format json \
    --verbose False --vad_filter True --condition_on_previous_text False \
    --beam_size 5 --temperature 0 --temperature_increment_on_fallback None
```

(Copy `smoke.wav` into `/var/lib/syncd` first; add the unit's other
`ReadOnlyPaths=`/`Environment=TMPDIR=` lines if §4.2 enabled them.) The usual
first failures: the venv under `/home` hidden by `ProtectHome=true`; the
Hugging Face cache not readable (or not writable on first download) by the
`syncd` user; `/dev/nvidia*` blocked (`DevicePolicy`/`PrivateDevices`); CUDA
libraries not on the service's path.

Record a 16 kHz mono 16-bit WAV on the phone (the app's recorder format) or
with `sox … -r 16000 -c 1 -b 16`. Never paste a real token into a shell
history: `read -rs TOKEN` first, and `HOST=https://aflulit.creatv.dev`.

```sh
# Hebrew (default language) — expect 200 with Hebrew `text`, your ASR_MODEL in `model`
curl --fail -sS -H "Authorization: Bearer $TOKEN" -F audio=@hebrew-clip.wav "$HOST/v1/transcriptions"

# English re-decode — expect 200, "language":"en" and English text on an English clip
curl --fail -sS -H "Authorization: Bearer $TOKEN" -F audio=@english-clip.wav -F language=en "$HOST/v1/transcriptions"

# Unsupported code — expect 400 {"error":"unsupported_language"}
curl -sS -H "Authorization: Bearer $TOKEN" -F audio=@hebrew-clip.wav -F language=fr "$HOST/v1/transcriptions"

# Wrong token — expect 401 {"error":"unauthorized"}
curl -sS -H "Authorization: Bearer not-the-token" -F audio=@hebrew-clip.wav "$HOST/v1/transcriptions"

# With ASR_TOKEN set: the SYNCD_TOKEN must be refused here (401) and the
# ASR_TOKEN refused on /backups (401).
```

Other codes you may see: `415 unsupported_audio_format` (not a 16 kHz mono
16-bit WAV), `413 audio_too_long` / `invalid_audio_size`, `429 busy` (one
GPU job at a time), `503 transcription_unavailable` (CLI failure or timeout —
check `journalctl -u syncd`; the log carries the request id, never the
transcript or the audio).

### 4.4 Verified on the server (2026-10-13, first real inference)

1. **Turbo model id — a Hugging Face id is NOT accepted.** The installed
   `whisper-ctranslate2` prints `--model` as a fixed choice list (`tiny … large-v3,
   large-v3-turbo, turbo, distil-*`) and exits 2 on anything else. Custom
   weights go through `WHISPER_MODEL_DIRECTORY` (§4.2). `syncd --check` now
   refuses an `ASR_MODEL` outside that list with the directory hint.
2. **`words` shape — yes.** With `ASR_WORD_TIMESTAMPS=true` the CLI's JSON
   carries `segments[].words[{word,start,end}]`; responses carry
   `words:[{word,startMs,endMs}]` (19 words on a 9 s clip, monotonic).
3. Measured: a 9 s Hebrew clip → correct text in **~8 s wall** (the CLI
   reloads the model per request — Ilan's note; a resident worker is a later
   optimisation), silence → 0.5 s, second concurrent request → `429 busy`
   in 0.9 s. `language=en` on the same Hebrew clip returned the Hebrew text
   (the fine-tune leans Hebrew); on an English clip, `en` and `he` both
   returned the English text.

## Operational notes

- **Storage**: archives land in `/var/lib/syncd` as
  `aflulit-backup-YYYYMMDD-HHMMSSZ.json`, already encrypted by the app.
  Retention (30 newest daily dates + one per older month — the ring-buffer rule
  inherited from Sumrak T20)
  runs automatically after every upload; nothing else to cron.
- **Token rotation**: edit `/etc/syncd/syncd.env`, `sudo systemctl restart
  syncd`, update the token in the app.
- **Logs** never contain the token, archive contents, audio or transcripts.
  Transcription log lines carry: request id, status, outcome, language,
  bytes, durationMs, wall ms.
- **Backup of the backups**: `/var/lib/syncd` is a copy of data that also
  lives on the phone and in the content repo's `backups/` — losing it only
  costs redundancy.
