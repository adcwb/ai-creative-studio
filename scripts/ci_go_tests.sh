#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../backend"
case "${STUDIO_CI_TEST_GROUP:?Select a test group}" in
  a-m) args=(-run '^Test[A-M]') ;;
  other) args=(-skip '^(TestNative|Test[A-M])') ;;
  native) args=(-run '^TestNative') ;;
  *) echo 'Unknown Go test group' >&2; exit 2 ;;
esac
# Keep the original complementary selections, including examples and fuzz seeds.
# Never reuse result-cache entries for a new database/worker acceptance run.
go test -count=1 -race ./... "${args[@]}"
