# Сумрак (Sumrak) API — `syncd`

The Go backend for [Sumrak](../Sumrak) (Mitch's personal Russian-learning
app). v1 scope is exactly **`syncd`**, the backup-sync service from design §9
(ADR-0015): it runs on the home server, is reachable only over Tailscale, and
stores the app's **encrypted** backup snapshots as opaque blobs. It never
decrypts anything — the passphrase and derived key exist only on the phone.

## Endpoints (design §9 contract)

| Route | Purpose |
|---|---|
| `PUT /backup?name=<file>` | Store one archive (create-only; 409 on duplicate). Runs retention afterwards. |
| `GET /backups` | List stored archives `{name, size, timestampMs}`, newest first. |
| `GET /backup/{name}` | Return one archive verbatim. |

Every route (including unknown paths) requires `Authorization: Bearer
<token>`; there is deliberately no unauthenticated endpoint, not even a
health check — Tailscale reachability plus an authed `GET /backups` is the
health check.

Archive names are the T20 contract shared with the app and the GitHub
target: `sumrak-backup-YYYYMMDD-HHMMSSZ.json` (UTC). The name is the id.
Retention is the T20 ring buffer: keep every backup from the 30 newest
distinct UTC dates, plus the latest per older calendar month.

## Layout

```
cmd/syncd/          entrypoint
internal/config/    env-var configuration (SYNCD_TOKEN, SYNCD_ADDR, SYNCD_DATA_DIR, …)
internal/auth/      bearer-token middleware (constant-time)
internal/naming/    backup file-name parsing (shared contract with the app)
internal/retention/ T20 ring-buffer retention, ported
internal/storage/   disk blob store (atomic create-only writes)
internal/server/    routing + handlers (stdlib net/http)
deploy/             systemd unit, env template, deploy walkthrough
```

Router: stdlib `net/http` — Go 1.22+ method/path patterns cover all three
routes; zero dependencies for a three-endpoint service.

## Develop

```sh
make check       # gofmt + go vet + go test
make build       # → bin/syncd

SYNCD_TOKEN=$(openssl rand -hex 32) SYNCD_DATA_DIR=./data go run ./cmd/syncd
```

Configuration is env-vars only (see `deploy/syncd.env.example`); no secret is
ever committed or logged.

## Deploy

See [`deploy/README.md`](deploy/README.md) — build, systemd install,
Tailscale notes, reboot-survival verification.
