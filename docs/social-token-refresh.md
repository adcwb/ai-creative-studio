# TikTok just-in-time credential renewal

Automatic renewal runs only during an explicit authenticated **POST** creator query
or publish/status operation. It is not a timer, an OAuth reconnect, a retry of a
publication, or a reaction that silently resends a post after HTTP 401. Existing GET
creator queries remain read-only and cannot renew credentials. POST uses the existing
session/CSRF/tenant/RBAC checks and starts a redacted mutation audit before changing
credentials. Owner/admin account linking and revocation permissions are unchanged.

New OAuth responses require Bearer tokens, an explicit account identity, granted
publishing/upload scope, bounded access and refresh credentials, and useful lifetimes:
more than 90 seconds, at most 24 hours for access and 365 days for refresh. The server
records absolute access/refresh deadlines in the encrypted credential envelope once.
Reading or restoring a record never extends its deadline. A 90-second margin admits
renewal before access expiry.

Renewal uses the official form-encoded `POST /v2/oauth/token/` with
`grant_type=refresh_token`. A successful response must match the account, use Bearer,
and contain only a subset of the previously granted scopes. A reduced scope is saved
and enforced; it does not retain permissions that the provider removed. Returned
refresh credentials replace the old value atomically, including when rotated.

Before any request, the existing media transaction durably claims the account as
`refreshing`, increments its version and records an encrypted random attempt ID.
Other processes cannot refresh the same account. The request is bounded to 20 seconds;
a 30-second public lease describes a potentially interrupted attempt. A stale lease
is displayed as `refresh_unknown`, but never authorizes another refresh request.
Current actor/session and the exact active connection claim are checked immediately
before sending. Completion checks account version, status and attempt. Revoke or
reconnect during a request prevents a late response from overwriting the new state.

Timeout, malformed success, mismatched account, expanded scopes, uncertain persistence
and a crash after claiming all quarantine the credentials. No automatic retry uses
the potentially rotated old refresh token, including after restart or on another API
instance. Explicit reauthorization is the recovery path. `invalid_grant` or a known
expired refresh credential marks the connection `expired` and removes its encrypted
credential. Local revocation always removes credentials before its upstream request;
a late revoke response cannot overwrite a newer reconnect.

Photo media grants are permanently tombstoned on account revocation, explicit OAuth
reconnection or invalid/expired refresh authorization. Their original job state and
unknown-result deduplication remain intact. Routine successful access-token renewal
does not revoke an already consented photo grant.

## Migration and UI

No new database table is needed: encrypted private media receipts and connection
metadata gain additive fields. Old access credentials remain usable through their
existing recorded access expiry. Old records without an absolute, trustworthy refresh
deadline require explicit reauthorization when renewal becomes necessary; the system
does not assume a one-year lifetime or reconstruct it from file timestamps.

Connection responses expose `connected`, `refreshing`, `refresh_unknown`, `expired`
and existing revoked states, plus optional `refresh_lease_until`. They never include
access or refresh credentials. Creator responses include the current media `version`
after renewal, so callers can refresh their state before a later CAS-protected save.
Backups must still include the entire matching metadata/media set and the deployment
must independently retain its encryption key. Restored uncertain attempts stay blocked.

## Verification boundary

Synthetic tests cover rotated credentials and absolute expiry across restarts,
read-only GET behavior, account/scope/type/lifetime rejection, invalid grants,
request uncertainty, lost persistence, stale attempt fencing, current actor checks,
revoke/reconnect races and photo-grant tombstones. Actual PostgreSQL tests coordinate
twelve callers across two API instances and verify exactly one refresh request,
shared rotated state, cross-tenant denial and unknown-state recovery by a fresh API.
All upstream traffic uses fake transports and synthetic credentials. No real OAuth,
platform account, credential setup, upload, publication or paid request was performed.

[Official TikTok token lifecycle](https://developers.tiktok.com/docs/en/oauth-user-access-token-management)
