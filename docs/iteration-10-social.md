# Social publishing: implementation and approval boundaries

The center supports authenticated export preparation for TikTok, Douyin and
Xiaohongshu. Preparing or downloading an owned image/video is not publishing.
Xiaohongshu export rejects prefilled social title/body; official access is paused.
No private server publishing endpoint is invented.

## TikTok video vertical slice

An operator-approved **and Direct Post audited** app can enable web account linking,
creator information, explicit privacy/interaction/AIGC/commercial consent, video
Direct Post or inbox draft upload, and explicit status reconciliation. This version
requires audited approval even for drafts; it does not enable unaudited sandbox
publication. Photos, scheduling, webhook delivery and automated token refresh are
not implemented. Expired accounts require explicit reconnect. Account reconnection
retains stable tenant/project/account identity so it cannot bypass an unresolved
publication intent. Tokens are never returned through media/social APIs.

The shipped path uses `FILE_UPLOAD`, one MP4 chunk up to 64 MiB, avoiding public
media URLs. It validates actual local media, fetches current creator permissions
and duration limits, and defaults comments/duet/stitch off. Privacy has no default.
Only the fixed API host and exact official upload host are accepted. Redirects and
HTTP proxies are disabled. Caller endpoints and upload URLs are never accepted.
The direct-post caption is the caption field; the local title is organizational
metadata and is not concatenated into an unexpected upstream caption.

OAuth uses the documented **web** authorization-code flow: random state is
one-use, five-minute, encrypted and bound to user, session hash, project and tenant.
PKCE is not claimed for this web protocol; the official token documentation lists
it for mobile/desktop. Token exchange occurs only after explicit browser approval.
Client secret stays server-side; AES-256-GCM encrypts tokens in private media
receipts, with tenant/project/app/redirect associated data. Pending state is consumed
before exchange; uncertain exchanges are not retried. A changed or missing key/app
configuration fails closed. Keep the social encryption key independently with
backups; existing model-KEK rotation does **not** rotate this separate social key.

Connection management requires owner/admin. Existing session, CSRF, tenant ownership
and mutation-audit middleware cover routes. Current session/membership and connection
revocation are checked again at external request boundaries and before accepting
an OAuth result. Revocation durably removes local credentials first; remote failure
persists `revoked_remote_unconfirmed`. Already admitted upstream calls cannot be
recalled; the result is reconciled rather than blindly sent again.

Jobs preserve source/account/intent and version, use existing transactional media
storage and request-key receipts, and claim an attempt before creating a post.
Each project retains at most 100 social jobs; this version has no automatic job-history
pruning. Completed or uncertain records are not silently deleted to bypass deduplication.
A three-minute durable lease plus a 150-second execution deadline prevents recovery
polls from racing an active upload; attempt identities fence late completions.
A lost create result remains `unknown`, blocking both retries and a new-key duplicate
for that account/source. Known publish IDs may be explicitly polled after the lease.
Token expiry during recovery preserves unknown status. Only an official terminal
failure unlocks a new attempt after a dispatched result. No automatic retries or
rate-limit loops are performed. Unknown results (including 429) impose a durable
60-second minimum per-job polling cooldown (429 Retry-After is honored up to one
hour); successful still-processing polls impose ten
seconds. A 429 or network uncertainty stops the attempt;
operators must inspect the stored state. `SEND_TO_USER_INBOX` is a draft, not a post;
only `PUBLISH_COMPLETE` for a direct job becomes `published`.

## Operator setup (not performed in this task)

After platform review and registered HTTPS web redirect, inject these values into
API processes using the deployment's secret mechanism, not browser storage or Git:

- `SOCIAL_TIKTOK_APP_APPROVED=true`
- `SOCIAL_TIKTOK_DIRECT_AUDITED=true`
- `SOCIAL_TIKTOK_CLIENT_KEY`, `SOCIAL_TIKTOK_CLIENT_SECRET`
- `SOCIAL_TIKTOK_REDIRECT_URI=https://<approved-origin>/social-callback`
- `SOCIAL_KEK_BASE64`: a separately generated and securely retained 32-byte key,
  encoded as base64; no default exists.

Every API instance needs the same approved configuration and encryption key. Secure
application mode is required; demo mode cannot enable remote social execution.
Defaults make no network requests. Existing real model budgets authorize no social
spending. This implementation task configured no genuine app, account or credential
and uploaded nothing to an external platform.

## Douyin and Xiaohongshu

Douyin's ordinary creative-SaaS route is approved H5 sharing, not arbitrary server
posting. The signing adapter follows official versioned scope and ticket rules;
a handed-off app link is never represented as published. Server video publishing
requires `video.create.bind` and enterprise/bound-account eligibility and is not
implemented here. The conflicting GET/POST share-result documentation is not guessed.
The controlled handoff runtime, when configured, supplies short-lived owned-media
capabilities; see its configuration documentation and tests. App/device behavior
and platform approval remain unverified.

Xiaohongshu remains material export, with an explicit local-only caption-copy helper.
The bounded draft stays in component memory scoped to project and tenant; it is not
persisted or sent to export/SDK/platform APIs. Copy requires a user click. Clipboard
failures show a localized manual-selection fallback; reload clears the draft. Its approved SDK would only hand off to the
app and must not auto-fill title, caption or topics. The paused onboarding and
absence of authorized configuration are not replaced with fake success.

## Evidence and limits

`social_test.go` uses synthetic credentials and a fake HTTP transport, plus an actual
one-second FFmpeg MP4. It covers encrypted recovery, state binding/replay, tenant
source isolation, CAS/idempotency, local export/cancellation, direct/draft byte-upload
contracts, status mapping, uncertain create/no resend, SSRF upload rejection,
expired-token recovery, lease fencing and revoked actor admission. These establish
local protocol/state behavior only, not account access, app-review acceptance,
platform/device interoperability or real publication quality. Final suite/browser
results belong in the iteration delivery report.

Official references (checked 2026-10-28):

- [TikTok web Login Kit](https://developers.tiktok.com/docs/en/login-kit-web)
- [Token exchange and revocation](https://developers.tiktok.com/docs/en/oauth-user-access-token-management)
- [Direct video post](https://developers.tiktok.com/docs/en/content-posting-api-reference-direct-post)
- [Status](https://developers.tiktok.com/docs/en/content-posting-api-reference-get-video-status)
- [Content-sharing requirements](https://developers.tiktok.com/docs/en/content-sharing-guidelines)
- [Douyin H5](https://developer.open-douyin.com/docs/resource/zh-CN/dop/develop/sdk/web-app/h5/share-to-h5)
- [Xiaohongshu access](https://agora.xiaohongshu.com/doc) and [SDK](https://agora.xiaohongshu.com/doc/js)

## Automatic Douyin application-ticket cache

In addition to explicit short-lived ticket injection, approved H5 deployments can
set `SOCIAL_DOUYIN_TICKET_FETCH_ENABLED=true` and inject
`SOCIAL_DOUYIN_CLIENT_SECRET`. The existing client key and media encryption key
remain required. Fetch mode does not silently fall back to an injected ticket when
its configuration is invalid. Readiness requests never fetch credentials; an
explicit, authorized preparation requests a ticket only after source/consent/CAS
preflight. Session and actor permissions are checked before and after each request.

The official sequence is JSON `POST https://open.douyin.com/oauth/client_token/`
with `grant_type=client_credential`, followed by
`GET https://open.douyin.com/open/getticket/` using the `access-token` header.
Only explicit `data.error_code=0`, nonempty bounded credentials, and expiry greater
than 120 and at most 7200 seconds are accepted. Responses are capped at 32 KiB,
requests have ten-second timeouts, and the whole fetch has a 25-second deadline.
URLs are fixed, proxies and redirects disabled. There are no automatic retries.

Issuing a new application token invalidates the previous token. Consequently this
implementation caches **per application, not per project, tenant or process**.
AES-GCM ciphertext lives in reserved private media receipts under the full client-key
hash. Those receipts are never emitted by media/social APIs. Existing PostgreSQL
transactions coordinate all API instances; file mode retains its existing
single-process store lock. Claims are durable before sending the token POST.
Positive cache entries survive restarts and participate in complete metadata backups.
A 90-second freshness margin refreshes tickets using the still-valid token first.
Failed ticket GETs keep that token and impose a 60-second minimum explicit-retry
cooldown; a 429 Retry-After seconds/date is honored up to one hour.
Concurrent callers observe a claimed operation instead of creating a second token.

A timed-out token POST, crash during issuance, or uncertain cache commit remains
blocked across restarts. Operators must first reconcile the application credential
with the platform and stop all users of that app before a reviewed cache recovery;
blind deletion/retry is unsafe and no UI reset performs it. The same caution applies
to restoring older backups and changing the client secret or encryption key: a
mismatched secret/key fails closed, rather than issuing another token. Do not run
production and test deployments with the same client credentials and independent
caches. Multi-instance deployments must share the same PostgreSQL state and keys.
No such external recovery or genuine credential configuration was performed here.

Tests include actual PostgreSQL coordination of twelve concurrent requests across
two API instances (one token issuance), restart/encrypted cache recovery, ticket
refresh without token regeneration, expiry refresh, invalid upstream TTL/error
schemas, unknown POST blocking, actor revocation and GET cooldown. Every upstream
response is synthetic; platform interoperability remains unverified.

- [Official client-token lifecycle and JSON contract](https://developer.open-douyin.com/docs/resource/zh-CN/dop/develop/openapi/account-permission/client-token)
- [Official open-ticket contract](https://developer.open-douyin.com/docs/resource/zh-CN/dop/develop/openapi/tools-ability/jsb-management/open-ticket)
