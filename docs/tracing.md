# Privacy-safe distributed correlation and tracing

Tracing is optional and off by default (`TRACE_MODE=off`). Authentication, tenant
ownership, RBAC, quotes and execution permits never read trace context. Auditing
continues independently even when the client supplies an unsampled trace.

HTTP accepts exactly one W3C version-00 `traceparent`, lowercase, nonzero IDs and
flags 00/01. Malformed/unsupported values start a fresh trace. Baggage and
tracestate are not propagated. Caller correlation is untrusted, not proof of who
issued a request. The server captures its HTTP span parent in task records and
`execution_jobs.trace_parent`; request JSON cannot set this field. Queue retries
and explicit recovery retain the original generation trace. Legacy records with
no parent start fresh. Queue fencing/idempotency/tenant checks remain unchanged.

API → durable PostgreSQL task/queue → independent worker → governance → provider
transport spans share an ID. File workers restore correlation for governance and
provider spans; only the PostgreSQL runner emits `worker.execute` spans.
Governance preserves `unknown` when authoritative billing has not resolved a
reservation; provider transport failures, partial response read failures and
worker errors are conservatively unknown. A successful HTTP response alone does
not prove settled cost. Successful provider request spans describe transport,
not billing, model quality or moderation success.

Only fixed operation/service names, trace/span IDs, timestamps, duration, outcome,
HTTP method/status and allowlisted capability/provider codes are exported. No
prompt, output, URL/path/query, header, raw exception, model ID, actor/tenant ID,
task ID or credential is exported. Remote provider requests are not given a
traceparent header (avoids leaking internal correlation to third parties).

## Explicit local export

Set `TRACE_MODE=json` and `TRACE_JSON_PATH` to a new absolute file on a trusted
local filesystem. API and worker need distinct paths, and each restart needs a
new path. Files are created exclusively with mode 0600 and symlinks are refused;
existing files are never appended or overwritten. No network collector or new
external service is configured. `internal/platform/tracing.Sink` is the replaceable
exporter interface; an OTLP exporter is not bundled or live-validated.

The default queue holds 256 spans (hard configurable constructor cap 4096), sends
batches of at most 32 and drops without blocking request execution. Export calls
have a two-second context deadline; shutdown waits at most five seconds. JSON
output stops at 64 MiB and errors/drops increment metrics. Rotation, retention and
collection are operator-owned. Ordinary file Write/Sync cannot be interrupted by
context: use a reliable local filesystem; shutdown timeout does not prove stuck
kernel filesystem I/O has exited. Custom sinks must honor context cancellation.

## Upgrade and rollback

Before running binaries against an existing PostgreSQL schema, run the explicit
schema initialization migration (`store-migrate` init workflow) that applies
`ALTER TABLE execution_jobs ADD COLUMN IF NOT EXISTS trace_parent text NOT NULL
DEFAULT ''`. Startup schema checks reject an unupgraded database. Initialization
is idempotent and legacy rows retain an empty parent. Rollback binaries may ignore
the additive column and optional domain JSON metadata; no destructive drop is
needed. Backups preserve the column with queue data.

## Metrics and validation

Operator-only `/metrics` now reports aggregate queue states, database configured
and available status, database health query failures, audit failures, and trace
exported/dropped/failure counters. Queue metrics use a one-second bounded database
query; unavailable metrics must not be interpreted as a cleared unknown backlog.
There are no tenant, task or provider labels on these metrics. `deploy/alerts.yaml`
adds unknown-execution, database, audit and trace-loss alerts; it is configuration,
not a deployed Prometheus/Alertmanager or exercised paging destination.

Tests use a fake in-memory collector and synthetic provider transport, plus real
local PostgreSQL for HTTP → queue → worker propagation, tenant lookup denial and
unknown-state metrics. Parser rejection, privacy allowlists, unsampled/no-export,
queue saturation, flush timeout, concurrent close and file safety/capacity are
covered. These tests do not call a real model or external trace collector.
