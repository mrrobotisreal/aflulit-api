# Deploying syncd to the home server

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

## Operational notes

- **Storage**: archives land in `/var/lib/syncd` as
  `aflulit-backup-YYYYMMDD-HHMMSSZ.json`, already encrypted by the app.
  Retention (30 newest daily dates + one per older month — the ring-buffer rule
  inherited from Sumrak T20)
  runs automatically after every upload; nothing else to cron.
- **Token rotation**: edit `/etc/syncd/syncd.env`, `sudo systemctl restart
  syncd`, update the token in the app.
- **Logs** never contain the token or archive contents.
- **Backup of the backups**: `/var/lib/syncd` is a copy of data that also
  lives on the phone and in the content repo's `backups/` — losing it only
  costs redundancy.
