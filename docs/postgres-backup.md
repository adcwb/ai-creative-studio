# PostgreSQL + immutable objects: offline backup and recovery

`backend/cmd/backup` implements a local backup bundle, independent verification,
and restore to an **already-created empty database and new directory**. It never
creates/drops databases, stops services, starts workers, installs timers, retries
upstream work, releases reservations or changes unknown outcomes.

## Maintenance contract

Stop **every API and worker instance** first, including autoscaling/restart
controllers. Confirm no process can claim jobs or modify data. Wait for leases to
expire, then supply `-confirm-stopped`. The database advisory migration barrier
blocks cooperating application writes; it does **not** stop queue Claim or
heartbeat. The command additionally refuses unexpired leased/dispatch_started
jobs. This is an offline procedure, not an online backup guarantee.

Within one repeatable-read transaction it exports a PostgreSQL snapshot, inventories
blob keys/hash/size and queue/cost states, and runs `pg_dump --snapshot` against that
snapshot. Every referenced immutable object is copied and SHA256 verified. Only a
fully written bundle is published under a new name. Orphan objects not referenced
by the manifest are excluded. Bounds: database dump 1 GiB, 100,000 objects, 100 GiB
total objects, existing 128 MiB per-object bound. Larger deployments need a revised
streaming/partitioned backup design before exceeding these limits.

## Commands and credentials

Build `go build -o backup ./cmd/backup` from backend. Docker builds the same binary
at `/app/backup`; **matching PostgreSQL client tools must be supplied separately**
(the default runtime image does not install them). Use a dedicated operator image
or host with compatible `pg_dump`/`pg_restore`. Do not install the scheduler inside
the API image.

Inject `DATABASE_URL` as an explicit PostgreSQL URI with host, user, database and
`sslmode`. Database names supported by this tool contain ASCII letters, numbers,
underscore or hyphen (maximum 63 bytes). Credentials belong in deployment secret
injection, not command arguments or repository files. No `.env`, shared credential
profile, default `.pgpass` or implicit client key is loaded. Optional explicit TLS
certificate paths and the existing S3 environment configuration are supported.
`BACKUP_PG_LIBRARY_PATH` exists for isolated packaged PostgreSQL clients; leave it
unset for normally installed tools.

```sh
# DATABASE_URL and existing BlobStore configuration are deployment-injected.
# BLOB_STORE=shared-file with BLOB_FILE_ROOT, or BLOB_STORE=s3 with BLOB_S3_*.
./backup -mode=backup -confirm-stopped -destination=/private/backups/new-bundle
./backup -mode=verify -source=/private/backups/new-bundle
# DATABASE_URL now identifies a NEW EMPTY target database; all targets stopped.
./backup -mode=restore -confirm-stopped -source=/private/backups/new-bundle -destination=/private/new-restored-data
```

Restore validates all hashes and refuses symlinks/unexpected files before mutation.
It runs transactional `pg_restore`, checks manifest and queue/cost state counts,
and publishes `<destination>/objects` for `BLOB_STORE=shared-file`, with a separate
`<destination>/cache` for DATA_DIR. Reconfigure and validate target applications
before operator-controlled startup. S3-source backups restore first to shared-file;
a separate reviewed object migration is needed to switch the restored system to S3.

A DB commit and filesystem rename cannot be one atomic transaction. On uncertain
restore outcome or publication failure, the tool retains private
`.postgres-restore-*` staging and never drops/retries the target DB. Inspect both
before choosing another empty target. Database ownership/ACLs are intentionally
not restored; configure deployment roles separately. Queue unknown/expired leases
and unknown monetary reservations remain exactly as recorded, requiring normal
recovery policy. No automatic refund/replay is implied.

Local authenticated `seal`/`open` and reviewed `retention` modes are documented in
[backup-encryption-retention.md](backup-encryption-retention.md). They do not install
cloud encryption, key escrow or lifecycle policies.

Bundles contain sensitive application data (identity hashes, sessions, encrypted
provider secrets). Keep the local destination private, apply deployment encryption
at rest and retention policy, and protect the separately injected encryption key;
this tool does not export that key. SHA256 detects accidental corruption, **not a
malicious party rewriting both archive and manifest**: restore only trusted bundles.
Archives can contain database code; never restore an untrusted bundle.

## Optional scheduler (not installed)

`deploy/backup/studio-backup.service` and `.timer` are opt-in templates. Before
installing, provision a private destination, matching PG tools, protected environment
file, and absolute executable **pre/post hooks**. The pre-hook must stop and verify
all local/remote API+worker instances (and prevent supervisor restarts); post-hook
resumes exactly those operator-owned services. Hooks receive only a clean PATH,
not DB/provider secrets. No built-in hook stops a real service. The 0700 destination root must be shared by every scheduler controlling these services: a no-symlink nonblocking file lock covers the entire pre/backup/post sequence, rejecting concurrent runners. Different destination roots cannot coordinate each other. Hooks have a 300-second default timeout and backup a 3600-second timeout (configurable); timeout is failure, never an automatic retry. Failed pre-hook
means no backup and no post-hook: operators must make partial-stop failures safe.
Once pre-hook succeeds, post-hook runs even when backup fails. Hook exit status is
mandatory; output is suppressed to avoid secret leaks. The service is not enabled,
and the timer never catches up missed windows automatically.

`python3 scripts/test_scheduled_backup.py` checks hook ordering/failure behavior.
Real synthetic integration is gated by `STUDIO_BACKUP_TEST_DSN` and
`STUDIO_BACKUP_TARGET_DSN` (fresh empty DBs), plus optional
`STUDIO_TEST_PG_DUMP`, `STUDIO_TEST_PG_RESTORE`, `STUDIO_TEST_PG_LIBRARY_PATH`:
`go test ./internal/application -run '^TestPostgresBackup' -race -count=1 -v`.
The test stores synthetic shared-file data, runs real pg_dump/pg_restore, verifies
unknown queue/cost preservation, and rejects active leases, nonempty targets and
object corruption. Real external S3 backup, offsite transport, managed encryption/retention
services and production recovery RTO/RPO are not validated by this local rehearsal.

## Local evidence (2026-10-23)

Real PostgreSQL 17 synthetic source/empty target and shared-file test passed with
`-race`; actual snapshot/custom dump/transactional restore completed, including an
immutable asset and unknown queue/cost records. A separate real CLI scheduler drill
created and verified a 63,276-byte PG bundle with empty object inventory and observed
pre → backup → post ordering. Five scheduler tests passed (success, backup failure,
failed stop, concurrent invocation, symlink lock). No production service was stopped,
no timer installed, and no cloud transfer occurred.
