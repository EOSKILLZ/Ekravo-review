# Ekravo Review

Self-hosted AI reviewer for GitHub pull requests. One Go binary, one SQLite
file, MIT. Powered by [Ekravo](https://ekravo.com) by default.

It reads the diff, pulls the surrounding code from the base commit, and posts
findings on the exact lines that changed. The verdict is computed in code from
the validated findings, so the model cannot announce an approval on its own.

## Run

You need Go 1.26.10+, a GitHub App (or a personal token) and an [Ekravo](https://ekravo.com)
API key.

    cp .env.example .env
    # set GITHUB_APP_ID, GITHUB_APP_PRIVATE_KEY_PATH, LLM_API_KEY
    go run ./cmd/ekravo-review -check
    go run ./cmd/ekravo-review

Dashboard: http://localhost:8090

First time? [Create and install the GitHub App](docs/GITHUB_APP.md) — it lists the
exact permissions and webhook events. TLS, backups, upgrades, metrics auth and
the data flow are covered in [docs/OPERATIONS.md](docs/OPERATIONS.md).

The Repositories page lists every repository your GitHub App installations can
reach, reviewed or not. Only watched repositories get reviewed: leave *Watch all
repositories* on, or switch it off and pick repositories one by one. The search
box and the All / Watched / Paused / No reviews filters only narrow the view.

## Commands

Comment on a pull request with your bot handle:

    @yourbot review         full review
    @yourbot explain        what this PR does, in plain language
    @yourbot summarize      five bullets
    @yourbot learn <rule>   remember a repo-scoped rule
    @yourbot learnings      list rules
    @yourbot forget <id>    remove one

## The model

Ekravo is the AI this reviewer is built for and the one we recommend. The
default endpoint and model chain point at [ekravo.com](https://ekravo.com), so a
single key is all it takes:

    LLM_BASE_URL=https://ekravo.com/v1
    LLM_API_KEY=ek_...                # create one at https://ekravo.com
    LLM_MODELS=deepseek-api/deepseek-flash,deepseek/deepseek-v4-flash,openrouter/free

The chain is tried in order, so the cheap fast model handles most reviews and
the stronger ones take over when a response comes back empty, malformed or is
rate-limited. Reasoning is off by default: JSON reviews truncate when reasoning
tokens eat the budget.

Any OpenAI-compatible endpoint still works if you self-host — LM Studio, Ollama,
llama.cpp, vLLM — by overriding `LLM_BASE_URL` and `LLM_MODELS`. Nothing else in
the app assumes Ekravo, but every default is tuned for it.

## Settings worth knowing

    LLM_BASE_URL, LLM_API_KEY, LLM_MODELS   Ekravo endpoint and failover chain
    CONTEXT_MODE=off|shadow|on               repository context
    AUTO_APPROVE=false                       post COMMENT instead of APPROVE
    AUTO_MERGE=false                         merge clean approvals
    WEB_RESEARCH=true                        verify version and CVE claims
    DASHBOARD_TOKEN=                         set a value to require a login

Everything is documented in `.env.example`. Per-repo overrides go in a
`.ekravo.yml` file at the repository root.

## Docker

    docker compose up -d --build

## Honest limits

No linters or SAST. GitHub only. One process per database. Context retrieval is
name-based and bounded: it reduces missed callers, it does not prove absence.
Web evidence can be wrong. Auto-merge does not wait for pending checks.

## Data and privacy

A review sends the pull request diff, selected surrounding code, and repository
metadata to the LLM endpoint configured by `LLM_BASE_URL` (Ekravo by default).
When `WEB_RESEARCH=true`, externally checkable claims (versions, CVEs, API
existence) may be sent to the same provider's web search. Local logic findings
are never sent for verification. Review history, learnings, and settings live in
one SQLite file under `DB_PATH`. Set `DASHBOARD_TOKEN` before exposing the
dashboard, and see [docs/OPERATIONS.md](docs/OPERATIONS.md) for the backup and
exposure checklist.

## License

MIT.
