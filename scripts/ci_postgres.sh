#!/usr/bin/env bash
# One disposable database cluster per isolated Actions runner, never production.
set -euo pipefail
pg_bin="${STUDIO_CI_PG_BIN:-/usr/lib/postgresql/16/bin}"
pg_data="${RUNNER_TEMP:-/tmp}/studio-ci-pg-data"
pg_socket=/tmp/studio-ci-pg-socket
pg_port=55439
case "${1:-}" in
  start)
    : "${GITHUB_ENV:?Expected a CI environment output file}"
    mkdir -m 700 "$pg_socket"
    "$pg_bin/initdb" -D "$pg_data" --encoding=UTF8 --auth-local=trust --auth-host=reject
    "$pg_bin/pg_ctl" -D "$pg_data" -l "${RUNNER_TEMP:-/tmp}/studio-ci-pg.log" -o "-k $pg_socket -p $pg_port -h 127.0.0.1" start
    for database in studio_ci studio_backup_source studio_backup_target; do
      "$pg_bin/createdb" -h "$pg_socket" -p "$pg_port" "$database"
    done
    dsn="host=$pg_socket port=$pg_port user=$(id -un) dbname=studio_ci sslmode=disable passfile=/dev/null"
    {
      for name in STUDIO_POSTGRES_TEST_DSN JOBQUEUE_TEST_DSN STUDIO_BLOB_TEST_DSN STUDIO_WORKER_TEST_DSN STUDIO_AUDIT_TEST_DSN; do
        printf '%s=%s\n' "$name" "$dsn"
      done
      printf 'STUDIO_POSTGRES_BIN_DIR=%s\n' "$pg_bin"
      printf 'STUDIO_BACKUP_TEST_DSN=postgresql://%s@/studio_backup_source?host=%s&port=%s&sslmode=disable\n' "$(id -un)" "$pg_socket" "$pg_port"
      printf 'STUDIO_BACKUP_TARGET_DSN=postgresql://%s@/studio_backup_target?host=%s&port=%s&sslmode=disable\n' "$(id -un)" "$pg_socket" "$pg_port"
      printf 'STUDIO_TEST_PG_DUMP=%s/pg_dump\nSTUDIO_TEST_PG_RESTORE=%s/pg_restore\n' "$pg_bin" "$pg_bin"
    } >> "${GITHUB_ENV:?Expected a CI environment output file}"
    ;;
  stop)
    if [[ -f "$pg_data/postmaster.pid" ]]; then "$pg_bin/pg_ctl" -D "$pg_data" -m fast stop; fi
    ;;
  *) echo 'Use start or stop for the disposable CI database' >&2; exit 2 ;;
esac
