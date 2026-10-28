# Approved Douyin H5 handoff

This implementation is an optional **App handoff**, not a server publish API.
It uses the official H5 share scheme and signature contract documented at
https://developer.open-douyin.com/docs/resource/zh-CN/dop/develop/sdk/web-app/h5/share-to-h5 .
The current version requires `h5.share`, `aweme.share` and ticket capability
`open.get.ticket`. One owned PNG/JPEG or MP4 (including completed exports) is
supported. Multi-image H5 capabilities are not implemented here.

`prepared` means an encrypted media grant and an App link were prepared locally.
`handed_off` records the user's explicit request to launch the App; it is **not
proof the App opened, accepted the source, or published a post**. The user selects
and confirms the actual account/publication in Douyin. Result-query integration is
unavailable because the current official share-result page conflicts on HTTP
method and has not been verified. No fallback fabricates `published`.

## Deployment prerequisites (not configured by development)

Secure authentication mode is mandatory. These operator-injected settings are
read only after `SOCIAL_DOUYIN_H5_APPROVED=true`:

- `SOCIAL_DOUYIN_SCOPES`: space-separated `h5.share aweme.share open.get.ticket`.
- `SOCIAL_DOUYIN_CLIENT_KEY`: the approved application's client key.
- `SOCIAL_DOUYIN_TICKET`: an actual **ephemeral** official platform ticket.
- `SOCIAL_DOUYIN_TICKET_EXPIRES_AT`: its real RFC3339 expiration, with at least a
  minute remaining. No guessed or perpetual ticket is accepted.
- `SOCIAL_DOUYIN_PUBLIC_ORIGIN`: approved, publicly reachable HTTPS origin without
  a path/query/credentials; hostname, not a literal IP or local host. This API's
  `/api/v1/social-media/douyin` must be routed there. An arbitrary local URL is not
  remotely accessible merely because the operator writes this setting.
- `SOCIAL_DOUYIN_MEDIA_KEY_BASE64`: deployment-injected independent 32-byte AES
  key, base64 encoded, for encrypted grants and private replay receipts. No default
  key or development credential is provided.

Automatic retrieval is optional: set `SOCIAL_DOUYIN_TICKET_FETCH_ENABLED=true`
and inject `SOCIAL_DOUYIN_CLIENT_SECRET` for the approved client key. Only an
explicit, authorized prepare request can call the fixed official
`POST https://open.douyin.com/oauth/client_token/` and
`GET https://open.douyin.com/open/getticket/` endpoints. Reading capabilities or
fetching a media grant never acquires a ticket. The manual ephemeral-ticket mode
remains the default; automatic mode does not silently fall back to injected
credentials when its own configuration is invalid.

The application-wide token/ticket cache is encrypted in private media receipts,
shared across API instances by the existing PostgreSQL transaction mechanism. All instances using one client key must share this cache/database; independent deployments using the same application credentials could invalidate each other’s tokens and are not supported.
Freshness margins and the official TTL bound are enforced. A durable dispatch
claim prevents simultaneous processes from creating competing client tokens.
An unknown client-token POST outcome blocks automatic recreation pending operator
reconciliation, including after restart; ticket GET failure uses bounded cooldown
and retains the valid token. Requests do not automatically retry or follow
redirects. Active session and editing membership are rechecked around these
requests. See the dedicated ticket-provider tests for synthetic protocol and
cache/recovery evidence. No real client secret was configured during development.

In manual mode, replace the actual short-lived ticket through the normal
secret-injection/restart workflow before expiration. Never paste tickets or keys
into the browser, repository, logs or reports. Replacing the media key invalidates
existing grants and prevents reading old encrypted cache/replay results; preserve
or explicitly reconcile matching encrypted state through the reviewed migration
workflow. Do not delete uncertain token dispatch records to force a retry.

No real application approval, public origin, ticket, device launch, account
selection, platform fetch or publication has been validated by local fixtures.
Enabling this in a real deployment remains an explicit external configuration and
platform acceptance task, not an action performed by this development work.

## Boundaries and recovery

An editor/admin/owner must choose a project-owned source and explicitly confirm
both sharing rights and a temporary **public read link**. Anyone holding the
unexpired link can read that one source. The grant lasts at most five minutes and
ends before the platform ticket's last minute. Image size is limited to20 MiB and
video to128 MiB. Files are served from the existing verified BlobStore path, not a
browser-supplied URL. The server does not fetch arbitrary external media URLs.

The fixed public GET/HEAD route uses an encrypted `cap` query parameter; no tenant,
actor, file path or credential appears in its pathname. It validates the purpose,
expiry, project ownership, job state and the original creator's **current** tenant
role on every request. Removing editing membership or revoking the job blocks
future full/range/HEAD reads. Revocation cannot recall bytes already delivered or
prevent a download already in flight from finishing. A later role re-grant does
not make an explicitly revoked job valid again. Logout alone does not revoke an
explicitly issued sessionless grant; use revoke or wait for expiry.

Responses use `Cache-Control: private, no-store`, no-referrer, no redirects and
standard bounded byte-range handling. Configure reverse-proxy/access logging to
omit query strings, including `$request` and `$request_uri`; log only `$uri` for
this route. Error logs must also omit capability URLs; the supplied Nginx uses a dedicated exact location with error logging disabled for this bearer-capability route. Apply an equivalent policy to other proxies. Do not attach third-party analytics to App links. The frontend keeps
links only in component memory; persistent media records expose metadata, while
replay links are encrypted in private receipts and never returned by job listing.

Preparation/ack/revoke require existing authenticated CSRF, project authorization,
CAS and idempotency controls. An unknown prepare result is recovered with the
same request key/body; it never triggers a platform request. Expired or revoked
preparations cannot be revived by replay. A user may deliberately prepare a fresh
grant after reviewing the current record. Ticket expiration must not block local
revocation. Cancellation and acknowledgements never manufacture a published state.

API contracts are in `api/openapi.json`; `social_douyin_runtime_test.go` exercises
synthetic grants, Range/HEAD, expiry, tenant/asset isolation, role revocation,
consent, replay/conflicts, encrypted receipts and narrow authentication bypass.
These tests do not establish real Douyin API/device compatibility.
