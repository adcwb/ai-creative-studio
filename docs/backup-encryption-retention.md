# Authenticated backup encryption and reviewed retention

The backup CLI supports local `seal`, `open`, and `retention`. These operations do
not contact providers, provision cloud resources, start workers, install timers,
or create real credentials. PostgreSQL backup/restore retain the explicit offline
contract in [postgres-backup.md](postgres-backup.md).

## Keys and usage

Inject `BACKUP_ENCRYPTION_KEY_BASE64` through the operator's protected secret
mechanism: exactly32 random bytes, base64 encoded. No default key, password KDF,
key generation, `.env` loading or credential discovery exists. Use a backup-specific
key distinct from `MODEL_CONFIG_KEK_BASE64`. Neither key enters archives/reports.
Archive directories must exist, be non-symlink mode0700, and have trusted ancestors.
New mode0600 outputs use `studio-backup-<random-id>.sbk`; existing files are never
overwritten. No real user key was created or configured during implementation.

```sh
# The key is securely injected, never passed as an argument or committed file.
/app/backup -mode=seal -source=/private/plain-bundle -destination=/private/encrypted
/app/backup -mode=open -source=/private/encrypted/studio-backup-REPLACE_ID.sbk -destination=/private/new-plain-bundle
```

Seal verifies the PostgreSQL/object bundle, captures initial hashes/sizes, and
checks every byte streamed into the tar against that inventory. Source mutation,
added/missing files, symlinks and hash mismatch fail. Keep the source private and
immutable throughout sealing. Seal does not delete the plaintext source. Open
uses private staging and verifies authentication, safe tar paths and the entire
bundle before publishing a NEW directory. Open does not restore a database;
review the bundle and separately restore to an empty, stopped database.

## Format and bounds

Version1 magic `STBKP001` includes creation time and random256-bit salt. A fixed-domain
HMAC-SHA256 derives a per-envelope AES-256-GCM key. Chunks are at most1MiB, with
counter nonces unique under that key. Each chunk authenticates header, counter and
length. A separate authenticated zero-length final frame is mandatory; trailing
bytes are rejected. Header/body mutation, wrong keys, reorder and truncation fail.
Go standard cryptographic primitives are used; the custom format has local tests,
not an external cryptographic audit or interoperability certification.

The envelope allows at most104GiB plaintext and100002 regular files; existing
bundle limits still apply (database1GiB, objects100GiB total/128MiB each). Streaming
avoids loading whole archives into memory. Extraction rejects links, devices,
absolute/traversal/duplicate paths and oversized entries. Creation time and total
ciphertext size remain visible. A key holder can forge an authenticated archive:
trust provenance before executing a native PostgreSQL restore. No password
stretching, key escrow or memory-erasure guarantee is implied.

Context cancellation is checked between chunks, not during stuck kernel I/O.
Process kill or filesystem failure may leave private `.encrypting-*`/`.decrypting-*`
staging; inspect it explicitly. No automatic retries or cloud transfer occur.

## Retention requires a reviewed plan

```sh
/app/backup -mode=retention -source=/private/encrypted -keep-last=7 -min-age-hours=168
# Inspect candidate names/hashes and the returned plan_sha256 before applying.
/app/backup -mode=retention -source=/private/encrypted -keep-last=7 -min-age-hours=168 \
  -apply-retention -expected-plan-sha256=REVIEWED_PLAN_DIGEST
```

Default is dry-run. At least one newest archive is retained, and minimum age is
24hours. All matching tool-format archives must fully authenticate with the supplied
key BEFORE any deletion. Unknown-key/malformed/matching-symlink entries abort the
whole plan. Foreign names, plaintext bundles and directories are never selected.
A stale digest cannot apply a changed plan. Use a separate directory per backup-key
generation; the tool does not guess keys or delete unreadable old backups.

The directory lock serializes cooperating seal/retention processes, not a separate
same-user script ignoring the lock or a hostile filesystem administrator. Deletion
is per-file, not an atomic multi-file transaction: an I/O failure can leave a partial
reviewed deletion. Inspect/replan instead of blind retry. There is no automatic
periodic deletion, bucket-version purge or offsite replication.

## Encrypted scheduling

`scripts/scheduled_backup.py --encrypted-root=/private/encrypted` changes the existing
sequence to stop → backup → seal → resume. Only a verified encrypted publication
allows deletion of this invocation's newly created plaintext bundle. Failure
preserves plaintext for diagnosis and runs the post-hook after successful pre-hook.
Other backups are untouched. Both roots must be private; schedulers controlling
the same services must share the scheduler lock root. Allow disk space for both
plaintext and encrypted copy during sealing. No timer was installed or production
service stopped in local testing.

## Validation

Crypto tests cover roundtrip, wrong keys, mutation, reorder, truncation, append,
frame bounds and cancellation. Bundle tests cover byte checks against the initial
manifest, safe extraction, no overwrite/partial publication, dry-run retention,
stale plans, unknown keys, newest retention and foreign-file protection. The real
PostgreSQL test now runs snapshot → bundle → seal → open → empty-database pg_restore
and preserves an immutable asset plus unknown queue/fee states. Scheduler tests
cover success cleanup and failed encryption preserving plaintext/resuming services.

These do not establish external S3 interoperability, production RTO/RPO, managed
key escrow, deployed scheduling or offsite recovery. Model KEK rotation is separate:
[model-key-rotation.md](model-key-rotation.md).

Local evidence for this milestone (2026-10-26): crypto/bundle race checks passed;
real PostgreSQL17 encrypted recovery log `/tmp/studio-backup9-pg-encrypted.log`;
real CLI encrypted scheduler/open/retention dry-run evidence
`/tmp/studio-encrypted-scheduler-l97u_0fw/result.json`; seven scheduler tests passed.
Only synthetic keys and a disposable locally created database were used. These
are targeted checks; the parent task runs the final whole-application regression.
