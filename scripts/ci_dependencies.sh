#!/usr/bin/env bash
# Official ubuntu-24.04 runner only. Do not change the host's apt sources.
set -euo pipefail

if [[ "$(lsb_release -is)" != Ubuntu || "$(lsb_release -rs)" != 24.04 ]]; then
  echo 'CI dependencies require the pinned official Ubuntu 24.04 runner' >&2
  exit 1
fi
packages=(ffmpeg fonts-noto-cjk)
case "${STUDIO_CI_POSTGRES:-false}" in
  true) packages+=(postgresql-16 postgresql-client-16) ;;
  false) ;;
  *) echo 'Invalid PostgreSQL dependency selection' >&2; exit 1 ;;
esac
missing=()
for package in "${packages[@]}"; do
  if [[ "$(dpkg-query -W -f='${db:Status-Status}' "$package" 2>/dev/null || true)" != installed ]]; then
    missing+=("$package")
  fi
done
if (( ${#missing[@]} )); then
  work="${RUNNER_TEMP:?}/studio-apt"
  mkdir -p "$work/archives/partial"
  # Isolate Ubuntu sources from unrelated third-party repositories. Both mirrors
  # are official HTTPS endpoints; apt still verifies Ubuntu's signed indexes and
  # package hashes, including restored cache entries. No trusted=yes/unsigned mode.
  cat > "$work/ubuntu.sources" <<'SOURCES'
Types: deb
URIs: https://archive.ubuntu.com/ubuntu/
Suites: noble noble-updates noble-backports
Components: main restricted universe multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg

Types: deb
URIs: https://security.ubuntu.com/ubuntu/
Suites: noble-security
Components: main restricted universe multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg
SOURCES
  apt=(sudo apt-get
    -o "Dir::Etc::sourcelist=$work/ubuntu.sources"
    -o 'Dir::Etc::sourceparts=-'
    -o "Dir::Cache::archives=$work/archives"
    -o 'APT::Keep-Downloaded-Packages=true'
    -o 'APT::Update::Error-Mode=any'
    -o 'Acquire::Retries=2'
    -o 'Acquire::http::Timeout=30'
    -o 'Acquire::https::Timeout=30')
  "${apt[@]}" update
  "${apt[@]}" install -y --no-install-recommends "${missing[@]}"
fi
# Fail explicitly if a runner image loses a required tool/font. Do not silently
# skip media tests or download another unverified binary distribution.
ffmpeg -version | sed -n '1p'
ffprobe -version | sed -n '1p'
family="$(fc-match --format='%{family}' 'Noto Sans CJK SC')"
[[ "$family" == *'Noto Sans CJK'* ]] || { echo 'Required Chinese font unavailable' >&2; exit 1; }
command -v google-chrome >/dev/null || { echo 'Official runner Chrome unavailable' >&2; exit 1; }
