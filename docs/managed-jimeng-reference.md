# Managed Jimeng reference transfer

The managed runtime now attaches the existing signed S3 reference bridge. Before
this correction the legacy image adapter could attach a bridge, but the managed
factory never did and managed acceptance rejected Jimeng references. That was a
local wiring gap, not merely missing cloud credentials.

The optional `EXECUTION_POLICY_FILE.reference_bridge_entitlements` array is an
operator-controlled transfer/storage authorization. Each entry binds:

```json
{
  "tenant_id": "REPLACE_TENANT",
  "model_config_id": "REPLACE_CONFIG",
  "model_config_version": 1,
  "model": "jimeng_seedream46_cvtob",
  "endpoint": "https://REPLACE_OPERATOR_S3_ENDPOINT",
  "bucket": "replace-private-bucket",
  "region": "replace-region",
  "source": "REPLACE_REVIEWED_TRANSFER_STORAGE_AUTHORIZATION",
  "expires_at": "REPLACE_FUTURE_RFC3339_TIME"
}
```

Illustrative placeholders are intentionally not valid configuration. No grant is
inferred from tenant-admin privileges, a model credential, a budget or an enabled
bridge. Changing a tenant model configuration version invalidates the grant. The
source records the operator's approval of reference data transfer and associated
storage costs; it is not a live cost measurement or authority to buy storage.

Only when this array is nonempty does managed execution configuration require
`REFERENCE_BRIDGE_ENABLED=true` and read `REFERENCE_S3_ENDPOINT`,
`REFERENCE_S3_BUCKET`, `REFERENCE_S3_REGION`, `REFERENCE_S3_ACCESS_KEY`,
`REFERENCE_S3_SECRET_KEY`, and optional `REFERENCE_S3_SESSION_TOKEN`. The destination
must exactly match every grant. Keys remain server-side; no tenant API accepts,
returns or exports these storage keys. Inject secrets through the deployment's
normal secure mechanism; no credential has been configured by this change. Empty
grants leave the managed bridge unavailable even if legacy bridge variables exist.
API and worker need the same policy and explicitly authorized storage configuration.
Policy changes require restarting all processes; there is no file hot reload.

Acceptance checks the matching grant, and execution rechecks model authorization,
configuration version, the task's cost/moderation permit, grant expiry and task
lifetime immediately before upload and again after upload. Revocation after upload
prevents the supplier create request. Recovery permits cannot upload. A retained
adapter cannot upload after its invocation ends. Once storage is attempted, failure
is conservatively treated as attempted/unknown, not a reason to release funds or
retry automatically; storage and model billing evidence still need reconciliation.

Existing safety remains: normalized bounded image, public HTTPS destination with
DNS/IP pinning, no redirects, random object key, 60-second signed PUT and 300-second
signed GET. No public bucket ACL is set, and the signed URL is not put in public job
responses. Object expiry/cleanup must be configured by the operator; URL expiry is
not object deletion. No bucket, cleanup rule or cloud resource is created here.

`reference_entitlement_test.go` uses synthetic keys, a fake object bridge and a
fake supplier transport to verify binding, default refusal, destination mismatch,
expiry, cancellation, post-upload revocation and unknown-state preservation.
Existing object-store tests exercise signing, image validation and SSRF guards.
These tests prove local protocol/control flow, not actual public S3 accessibility,
Jimeng's ability to fetch a URL, account entitlement, image quality or real billing.
Case 09 therefore still needs explicit external reference-bridge validation and
secure authentication approval before any real call.
