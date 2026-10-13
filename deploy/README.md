# Deploying syncd to the home server

> **Who deploys:** Mitch. This is a `ready-for-human` step — agents never SSH,
> `scp` or touch the home server. Everything below is meant to be followed
> verbatim.

`syncd` runs on the home server and is reached **only over Tailscale** — it is
never exposed to the public internet. Tailscale's WireGuard tunnel provides
transport encryption and network-level authentication, which is why syncd
itself speaks plain HTTP with bearer auth on top: TLS termination inside the
tailnet would add certificate management for no additional threat-model win.
(The archives are additionally AES-256-GCM-encrypted end-to-end by the app
before they ever leave the phone — syncd stores opaque blobs and never holds
a key.)

## Prerequisites

- A Linux home server with systemd, joined to the tailnet (`tailscale status`
  shows it), with the phone's Tailscale client able to reach it.
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

# From any tailnet machine (token from /etc/syncd/syncd.env):
curl -H "Authorization: Bearer $TOKEN" http://<tailnet-hostname>:8787/backups
# → {"backups":[]}
curl http://<tailnet-hostname>:8787/backups
# → 401 {"error":"unauthorized"}

# Reboot survival:
sudo reboot
# ...after it's back:
systemctl status syncd            # active (running) again, no manual step
```

The tailnet hostname is whatever `tailscale status` calls the server (e.g.
`homeserver` → `http://homeserver:8787`); MagicDNS names and the `100.x.y.z`
address both work. Use that same base URL + token in the app:
**Settings → Backup → Home server (syncd)**.

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

### 4.2 Env + unit

1. Append the ASR block from `deploy/syncd.env.example` to
   `/etc/syncd/syncd.env`; set `ASR_ENABLED=true`, `WHISPER_CLI`, and pick
   `ASR_MODEL`:
   - `large-v3` (default, multilingual), or
   - `ivrit-ai/whisper-large-v3-turbo-ct2` — the recommended Hebrew fine-tune
     (Apache-2.0; see "Verify on the server" below).
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

Record a 16 kHz mono 16-bit WAV on the phone (the app's recorder format) or
with `sox … -r 16000 -c 1 -b 16`. Never paste a real token into a shell
history: `read -rs TOKEN` first, and `HOST=http://<tailnet-hostname>:8787`.

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

### 4.4 Verify on the server (open questions this ticket could not settle on a Mac)

1. **Turbo model id.** Whether the installed `whisper-ctranslate2` accepts
   `ASR_MODEL=ivrit-ai/whisper-large-v3-turbo-ct2` as a Hugging Face id for
   `--model`, or needs the repo downloaded and passed as a local path via
   `WHISPER_MODEL_DIRECTORY`. Try the id first; `--check` passes either way
   (it only probes `--help`), so the first smoke request is the real test.
2. **`words` shape.** With `ASR_WORD_TIMESTAMPS=true`, whether the CLI's JSON
   carries `segments[].words[{word,start,end}]`. If it does, responses gain
   `words:[{word,startMs,endMs}]`; if not, `words` is simply absent and
   nothing in the app depends on it (T14/T16 do not).

Record both answers in the T12 Session log.

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
