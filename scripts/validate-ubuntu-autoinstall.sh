#!/bin/bash
# Validate cloud-config files offline with the pinned Subiquity schema.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
paths=()
for path in "$@"; do
  if [[ "$path" != /* ]]; then path="$PWD/$path"; fi
  paths+=("$path")
done
cd "$root"
GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly go test ./internal/autoinstall -run '^TestGoldenUserData$' -count=1 -args "${paths[@]}"
