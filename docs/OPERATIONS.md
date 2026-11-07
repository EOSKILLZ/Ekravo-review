# Running in production

The binary serves plain HTTP on `ADDR` (default `:8090`). Put TLS in front of
it, keep the SQLite file on persistent storage, and set long random secrets.

## Exposure and TLS

Only publish the reverse proxy; leave `8090` on localhost or a private network.
The webhook URL is `https://YOUR_HOST/webhook`.

Caddy:

    review.example.com {
        reverse_proxy 127.0.0.1:8090
    }

nginx:

    server {
        listen 443 ssl;
        server_name review.example.com;
        # ssl_certificate ...;

        location / {
            proxy_pass http://127.0.0.1:8090;
            proxy_set_header Host $host;
            proxy_set_header X-Forwarded-Proto $scheme;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        }
    }

`X-Forwarded-Proto` also makes the dashboard session cookie `Secure` when TLS
terminates at the proxy.

Tailscale works too: `tailscale serve --bg 8090`, or `tailscale funnel 8090`
for a public HTTPS hostname.

## Checklist before exposing it

- `DASHBOARD_TOKEN` is a long random value (`openssl rand -hex 32`).
- `WEBHOOK_SECRET` is a different long random value.
- `AUTO_APPROVE=false` and `AUTO_MERGE=false` until you trust the output.
- The private key is mounted read-only and is not in git.
- `DB_PATH` is on a persistent volume, not inside the container layer.
- The reverse proxy forwards `X-Forwarded-Proto: https`.

## Backups

Review history and learnings live in one SQLite file (WAL mode). Prefer an
online backup:

    sqlite3 data/ekravo-review.db ".backup 'backup.db'"

Or stop the container and copy the whole `data/` directory. Restore by putting
the file back while the app is stopped, then start it. Test a restore before you
need one.

## Upgrades

    git fetch --tags
    git checkout v0.2.0
    VERSION=$(git describe --tags) COMMIT=$(git rev-parse --short HEAD) \
      BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) docker compose up -d --build

Schema migrations run at startup and are additive, so downgrades keep the new
columns. Run exactly one process per database: this is a single-writer design,
not a horizontally scaled service.

## Health and metrics

- `/healthz` — liveness, no dependencies.
- `/readyz` — returns 503 if SQLite is unavailable.
- `/api/health` — JSON with the build version.
- `/metrics` — Prometheus counters. When `DASHBOARD_TOKEN` is set it requires
  `Authorization: Bearer <token>`, so configure the scraper accordingly.

## Dashboard

The Reviews view keeps live work in **Now** and **Next**, and lists merged or
closed pull requests under **Done**. Newest-first and repository order are both
server-side, so pagination keeps the order you selected. Pull request state is
recorded from `pull_request` webhooks; after an upgrade, older reviews are
reconciled against GitHub on startup.

## Failure behaviour

- A full worker queue returns HTTP `503` with `Retry-After`, so GitHub
  redelivers instead of losing the review.
- If every model section fails, the bot posts a short failure comment on the
  pull request rather than staying silent.
- GitHub `429`, `5xx`, and secondary rate limits are retried with backoff.
  LLM calls fall back through `LLM_MODELS`.
- Duplicate deliveries are deduplicated by repository, pull request, and head
  SHA, and the same PR is never processed twice concurrently.
- Comments authored by bots are ignored for `@handle` commands, which prevents
  loops.

## Data flow

A review sends the diff, selected surrounding code, and pull request metadata
to the LLM endpoint in `LLM_BASE_URL` (Ekravo by default). With
`WEB_RESEARCH=true`, externally checkable claims (versions, CVEs, API
existence) may be sent to the provider's web search; local logic findings are
never sent. Review history, learnings, and the watch list are stored in
`DB_PATH`. The GitHub App private key and webhook secret are only read from the
environment/mounted files and are never written to the database or logs.

## Releases

Tags build the binary and publish a container image to
`ghcr.io/ekravo/ekravo-review`. The image is built from the same Dockerfile, so
`docker compose up -d --build` and the published image behave identically. New
GHCR packages are private by default; if anonymous pulls are required, set the
package visibility to public in its settings, or authenticate with a token that
has `read:packages`.
