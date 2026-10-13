# SumrakAPI — Repo Glossary

Repo-local terms for the Go backend (v1 scope = exactly `syncd`, design §9 /
ADR-0015). Cross-repo language lives in the workspace root `CONTEXT.md`
(Backup snapshot, syncd, Sync state, …).

## Language

**Archive / blob**:
What syncd stores — one backup snapshot file, already gzip+AES-256-GCM
encrypted by the app. Opaque to the server: syncd never decrypts, never
holds a key or passphrase (server compromise costs availability, not data).
_Avoid_: backup contents, payload (that's the app-side decrypted form)

**Backup name**:
The T20 file-name contract `sumrak-backup-YYYYMMDD-HHMMSSZ.json` (UTC).
The name is the archive's id in every endpoint, embeds the snapshot moment
(retention reasons from names alone, clock-independent), and doubles as the
path-traversal guard — anything not matching is rejected everywhere.

**Ring buffer (retention)**:
The T20 rule ported to Go: keep every archive from the 30 newest distinct
UTC dates present, plus the latest per older calendar month; never touch
unparseable names. Runs server-side after every successful `PUT /backup` —
the app never prunes this target.

**Bearer token**:
The single shared secret (`SYNCD_TOKEN` env, ≥16 chars) the app presents on
every request. Constant-time compared; every route — unknown paths included
— 401s without it. There is deliberately no unauthenticated endpoint, not
even a health check (an authed `GET /backups` is the health check).

**Tailnet host**:
How the app reaches syncd: `http://<tailscale-name>:8787` inside Mitch's
tailnet only. Plain HTTP by design — Tailscale provides transport
encryption + network auth; no public exposure, no TLS termination.
