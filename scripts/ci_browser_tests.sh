#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../frontend"
case "${STUDIO_CI_BROWSER_SUITE:?Select a browser suite}" in
  e2e) config=playwright.config.ts ;;
  routing) config=playwright.routing.config.ts ;;
  auth) config=playwright.auth.config.ts ;;
  *) echo 'Unknown browser suite' >&2; exit 2 ;;
esac
case "${STUDIO_CI_BROWSER_DEVICE:?Select a browser device}" in
  desktop|mobile) ;;
  *) echo 'Unknown browser device' >&2; exit 2 ;;
esac
# Each matrix cell has its own runner, backend and temporary data. One worker
# retains fixture ordering and actual registration rate limits within that cell.
node node_modules/@playwright/test/cli.js test --config "$config" --project "$STUDIO_CI_BROWSER_DEVICE" --workers=1
