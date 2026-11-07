# Connect the GitHub App

Ekravo Review talks to GitHub through a GitHub App. One App can be installed on
several personal accounts and organizations; the dashboard unions every
installation, so you do not pin an installation ID.

## 1. Create the App

Open <https://github.com/settings/apps/new> (Settings → Developer settings →
GitHub Apps → New GitHub App).

- **Homepage URL**: your repository or dashboard URL.
- **Webhook URL**: `https://YOUR_HOST/webhook` — public HTTPS. TLS is normally
  terminated by a reverse proxy; see [OPERATIONS.md](OPERATIONS.md).
- **Webhook secret**: `openssl rand -hex 32`. Save it as `WEBHOOK_SECRET`.
- **Repository permissions**:

  | Permission | Access | Why |
  |:--|:--|:--|
  | Contents | Read-only | diffs, file contents, tarball and tree reads |
  | Pull requests | Read and write | post reviews and inline comments |
  | Issues | Read and write | PR conversation comments and `@handle` commands |
  | Metadata | Read-only | mandatory for every GitHub App |

- **Subscribe to events**: `Pull request` and `Issue comment`.
- **Where can this App be installed?**: any account.

Create the App and note the **App ID**.

## 2. Generate a private key

App settings → **Private keys** → **Generate a private key**. The `.pem`
download is shown once.

With Docker Compose, put it in `./secrets/app.pem` (mounted read-only) and set:

    GITHUB_APP_PRIVATE_KEY_PATH=/data/secrets/app.pem

Alternatively put the key inline in `GITHUB_APP_PRIVATE_KEY`, using `\n` for
newlines. Never commit it.

## 3. Install the App

App settings → **Install App** → choose the account or organization → select
**All repositories** or specific repositories. Repeat for every account or
organization you want reviewed.

The bot resolves the installation that owns each repository, so a single App
serves all of them. `GITHUB_APP_INSTALLATION_ID` is optional and only used for
GitHub calls that are not tied to a repository.

## 4. Configure and start

    GITHUB_APP_ID=123456
    GITHUB_APP_PRIVATE_KEY_PATH=/data/secrets/app.pem
    WEBHOOK_SECRET=<the same secret>
    BOT_HANDLE=your-app-slug

`BOT_HANDLE` is the App slug, which is what people type after `@`. Keep
`AUTO_APPROVE=false` and `AUTO_MERGE=false` until you trust the reviews.

    go run ./cmd/ekravo-review -check
    docker compose up -d --build

## 5. Pick repositories

Open the dashboard → **Repositories**. Every repository the App can reach is
listed. Leave *Watch all repositories* on, or switch it off and select the ones
to review. Nothing is reviewed until it is watched.

If a repository is missing, confirm the App installation covers it and reload
the page. If reviews still do not run, check the App's **Advanced → Recent
Deliveries**: a successful delivery returns `202`; `401` means the webhook
secret differs, and `503` means the queue was full (GitHub retries).

## 6. Test it

Open a non-draft pull request. The bot reviews `opened`, `synchronize`,
`ready_for_review`, and `reopened` events. Comment `@your-app-slug review` to
force a review, `explain` for a plain-language description, or `summarize` for
five bullets.
