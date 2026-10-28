# Yingchuang · AI Creative Studio

[简体中文](README.md) · **English**

A creative workspace from briefs, scripts and storyboards to images, voice, subtitles, posters and short films. The React / TypeScript frontend runs separately from the Go / Eino backend. Local files are supported alongside PostgreSQL, independent workers and object storage adapters.

The UI supports **Simplified Chinese and English**, remembers language selection and falls back to the browser language on first use. Currency is independent of language: USD is the default, with integer minor-unit budgets and ledgers separated by currency. There is no silent conversion. The translation structure is extensible; other languages are not yet translated.

## What you can do

| Workflow | Features |
| --- | --- |
| Planning | Project briefs, scripts, shots, character references, version history and assets |
| Images and posters | Image-provider adapters, references and mask editing; editable text and Logo layers saved and exported as PNG |
| Short-film editing | Trim, reorder, split and duplicate shots; positioned audio tracks, image/video overlays and position/opacity keyframes |
| Preview and output | Browser composition preview and scrubbing; composable shot transitions; H.264/AAC MP4, landscape/portrait/square, 360p through bounded 4K |
| Sound and subtitles | Explicit source sound mixing, music, manual subtitles, ASR candidates, burned subtitles and SRT/VTT; text-only transcription remains distinct |
| Publishing preparation | Xiaohongshu, Douyin and TikTok center with separate export, App handoff, draft and direct-post capabilities |
| Team operations | Authentication, organizations, server-side roles, tenant isolation, model configuration, budget reservations/settlement, moderation and redacted audits |

See the [current delivery matrix](docs/iteration-10-delivery.md) for implementation, verification and remaining gaps. Synthetic media, fake upstreams and protocol tests do not establish real model quality or social-platform approval.

## Editing boundaries

This is an evolving short-film editor, not a complete professional NLE. Output is 24fps: at most 180 seconds at 360p, 120 at 720p, 60 at 1080p and 15 at 2160p (4K). Limits are 30 shots, eight positioned audio tracks, four visual overlays and 16 position/opacity keyframes per overlay. Size remains fixed and interpolation is linear. Output and intermediate files are capped at 128MiB. Long-form 4K, HDR, lossless mastering and real-time full-resolution playback are not promised.

The browser offers a low-resolution silent composition preview. Final subtitles, audio and encoding must be reviewed in the actual export. Transitions use allowlisted built-in effects, never arbitrary FFmpeg expressions. Dissolves, wipes, slides and circle reveals overlap neighboring shots; subtitles and tracks use the shortened movie clock. See [media specifications](docs/iteration-10-media.md).

Posters preserve editable layers and export a new PNG without overwriting source images. Character references and feedback cannot guarantee identity consistency. [Offline alignment, lip-sync and sound engines](docs/local-model-engines.md) have not been quality-tested with real weights; some upstream licenses restrict commercial use.

## Social publishing requires approval

Downloading files, handing off to an App, uploading a draft or submitting for review is never labeled published. TikTok video and photo direct-post/draft protocols require application approval and user authorization; photos also require a platform-verified public media domain. Ordinary Douyin creative tools use approved H5 sharing, with official ticket retrieval, caching and expiry refresh. Enterprise posting has additional account and permission restrictions. Xiaohongshu access is restricted and no general server-side note-publishing API has been verified.

An unconfigured installation provides real media export. Xiaohongshu also offers explicit copying of a local caption, with no automatic platform prefill. PNG posters can be explicitly converted into separate JPEGs, previewed, ordered and assigned a cover without altering originals. Photo transmission requires consent to scoped public access lasting up to 65 minutes. TikTok credentials renew just in time during explicit operations; rotated tokens are encrypted, and uncertain renewal requires reauthorization without resending posts. [Photo contract](docs/social-photo.md) · [Token renewal](docs/social-token-refresh.md)

Account connections, transmission and status queries require platform-specific server-side capability checks. An uncertain create result retains its receipt and is not blindly resent. This iteration uses synthetic accounts and fake upstreams only: **no real OAuth, uploads or external posts have been performed**. [Protocol and state boundaries](docs/iteration-10-social.md)

## Model ecosystems

Replaceable adapters cover OpenAI, Anthropic / Claude, Google / Gemini / Nano Banana, ByteDance Ark / Doubao, MiniMax and Jimeng's public visual API. Text, images, video, speech and transcription differ by provider and model. A configured model ID does not imply protocol compatibility or account access.

OpenAI Videos / Sora is retired and rejected locally. No currently usable official Jimeng video API has been verified. See [candidate contracts and lifecycle notes](docs/iteration-9-followup.md). Real permissions, quality, latency and charges remain unverified. [Live evaluation tooling](docs/live-evaluation.md) defaults to dry-run and refuses paid requests without an exact quote, cap, moderation and authorization.

## Run locally

Requirements: Go 1.25.3, Node.js 24 / npm 11, FFmpeg / FFprobe and Chinese fonts. The backend reads process environment variables; it does not automatically load `.env`. This example is for loopback HTTP with new synthetic accounts:

```bash
# Terminal 1
cd backend
AUTH_ALLOW_REGISTRATION=true AUTH_COOKIE_SECURE=false go run ./cmd/server
```

```bash
# Terminal 2
cd frontend
npm ci
npm run dev
```

Open `http://localhost:5173`; the backend listens on `http://localhost:8080`. Registration is disabled and Secure cookies enabled by default. Public deployment requires TLS; do not copy the local cookie setting. Explicit `AUTH_MODE=demo` exposes demo spaces, not secure tenants. Never use it for real customer data. [Authentication](docs/authentication.md)

## Docker and persistence

```bash
docker compose build
docker compose up -d
```

Nginx provides a same-origin proxy; the frontend binds loopback by default. Configure registration, TLS, volumes and limits using the [Docker guide](docs/docker.md). Published images are not a deployed application. This development iteration does not automatically push, deploy or create cloud resources.

Back up the entire `DATA_DIR` in file mode. PostgreSQL uses transactional storage and a database queue with separate workers and shared blobs. Stop writes and use an empty migration target; changing environment variables is not a migration. [Workers](docs/postgres-workers.md) · [Object storage](docs/iteration-8-object-storage.md) · [Migration and backup](docs/postgres-backup.md)

Deployment keys must be injected by operators and have no defaults. Lists and audits do not expose provider credentials. Encrypted backups exclude the KEK, which must be retained separately. Old binaries cannot safely preserve new layer and publishing fields: rollback requires a matching complete backup. [Encrypted backups](docs/backup-encryption-retention.md) · [Key rotation](docs/model-key-rotation.md)

## Development checks

```bash
cd backend
go vet ./...
go test -race ./...
go build ./cmd/server
cd ../frontend
npm ci
npm run lint
npm run typecheck
npm test
npm run build
PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/usr/bin/chromium npm run test:e2e
```

Full acceptance also covers OpenAPI, tenant security, recovery, real media, desktop/mobile layouts and multiple services. See [current verification](docs/iteration-10-verification.md) and `.github/workflows/ci.yml`. Only synthetic media and temporary accounts belong in tests; do not commit credentials, private inputs or generated media.

[Handoff](docs/next-iteration.md) · [Roadmap](docs/roadmap.md) · [Contributing](CONTRIBUTING.md) · [Internationalization](docs/internationalization.md)

[Chinese/English desktop and mobile screenshots](docs/screenshots/iteration-10/README.md) use synthetic test data and do not demonstrate real platform publishing.
