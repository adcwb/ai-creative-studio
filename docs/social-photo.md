# TikTok PHOTO: explicit conversion, consent and bounded media access

This integration implements the official
[PHOTO API](https://developers.tiktok.com/docs/en/content-posting-api-reference-photo-post)
with `PHOTO` media, direct posting or inbox upload. AI disclosure is a top-level
field; title and description are separate. Official transfer requirements are in
the [media guide](https://developers.tiktok.com/docs/en/content-posting-api-media-transfer-guide).
Actual application approval, URL ownership verification, OAuth and remote media
fetching have not been exercised. Tests use synthetic accounts and fake upstreams.

## Local preparation

`POST /api/v1/projects/{id}/social/photos/normalize` accepts `expected_version`,
`asset_id`, `confirmed: true`, and an idempotency key. It is local computation and
works without a configured social application. PNG (including exported posters)
and JPEG sources must belong to this project, be at most 20,000,000 bytes and
20,000,000 decoded pixels. The explicit operation creates a new JPEG asset:
white transparency matte, bilinear downsampling to fit 1080×1080 without
upscaling, JPEG quality 90. Originals remain unchanged. The response embeds the
media state and includes `normalized_asset_id` from the operation receipt; do not
infer the new asset by its position in a concurrently updated list. Preview the
new JPEG before confirming publication. Normalization is lossy, not merely a
filename/MIME change. Existing project asset and storage quotas still apply.

The outbound implementation deliberately supports JPEG only, with each dimension
at most 1080 pixels and at most 20,000,000 bytes per image. These conservative local
limits fit the official image restrictions; WebP is not implemented by this
application. JPEG headers and complete decoded pixels are checked, rather than
trusting the upload MIME label. A PNG cannot be sent without explicit conversion.

## Intent and execution

Existing `/social/jobs` accepts `media_type: "PHOTO"`, 1–35 unique ordered
`photo_asset_ids`, a zero-based `photo_cover_index`, and explicit
`public_media_confirmed: true`. `asset_id` and `export_id` must be empty.
Title is at most 90 UTF-16 units and caption/description at most 4000. Direct
posting requires explicit creator-supported privacy, rights and relevant comment,
AI and commercial disclosures. `auto_add_music` is an explicit direct-only option.
Photo drafts carry title, description and AI disclosure; unsupported direct-only
settings, duet and stitch flags are rejected. Empty media type preserves existing
video behavior; explicit arbitrary media types are rejected.

A persisted intent does not expose an image publicly. Explicit dispatch obtains
current account authorization and creator options, validates owned files, then
mints URLs and submits exactly once. Accepting an init response means processing,
not published. Only remote status evidence can mark a direct post published;
inbox upload remains a draft. Unknown creation outcomes never automatically retry.
Status recovery with a known publish ID does not create a new grant or resend.

Deduplication binds account and source set. Ordered IDs remain intact for delivery
and cover selection, while duplicate admission compares an order-independent
source-ID set and sorted SHA-256 JPEG byte digests. Reordering, changing captions
or explicitly re-normalizing identical bytes cannot bypass an unresolved intent.
This is byte identity, not a claim of perceptual image similarity detection.

## Operator configuration and public-image boundary

The existing approved TikTok app configuration and encrypted credential key are
required, plus:

- `SOCIAL_TIKTOK_PHOTO_URL_VERIFIED=true`: operator attestation that this app has
  verified the domain or matching URL prefix with TikTok.
- `SOCIAL_TIKTOK_PHOTO_PUBLIC_ORIGIN=https://your-approved-domain`: no path, query,
  credentials or IP literal. The broker path is fixed to
  `/api/v1/social-media/tiktok-photo` beneath `/api/v1/social-media/`.

These settings do not perform verification or create an external resource. No
browser request can provide an arbitrary source URL. The server creates encrypted
capabilities bound to the configured origin/client, tenant, project, actor,
connection, job, immutable grant identity and owned asset. URLs permit GET/HEAD,
including bounded HTTP Range, without browser cookies. They are bearer grants:
anyone holding one can download the selected image while it remains authorized.

TTL is **65 minutes**, allowing the official one-hour asynchronous download window
plus bounded dispatch overhead. This duration must be disclosed before dispatch.
Grants remain available during processing/unknown results; polling does not renew
them. Current membership and account status are checked on each request. Completed,
failed, expired or explicitly revoked grants are denied. Routine token refresh
preserves a grant; account revoke/reconnect invalidates previous grants permanently.
Logging out alone does not revoke this already-consented asynchronous transfer.

`POST /social/jobs/{job_id}/revoke-photo-access` requires current project write
permission, `expected_version`, `confirmed: true` and an idempotency key. It stops
future image requests without erasing the job's unknown/published state or enabling
a resend. It does not retract a remote post or recall already downloaded bytes.

Public lists never expose capability URLs; encrypted credentials and capabilities
are excluded from report/log output. The exact reverse-proxy broker location must
suppress query-bearing error logs as well as use query-free access logging. Test
an unavailable backend with synthetic capabilities when changing proxy templates.

## Validation boundaries

Tests cover real PNG/JPEG decoding and white-matte pixels, limits, ownership,
CAS/idempotency, correct direct/draft payloads and source order, explicit public
GET/HEAD through secure middleware, Range, grant expiry/config changes/revocation,
unknown-result deduplication, query recovery and account-reconnect non-revival.
PostgreSQL and browser checks are recorded in the current acceptance report.
No test result establishes real TikTok account permission or successful publishing.
