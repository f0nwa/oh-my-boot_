#!/bin/sh
# Fetches the upstream unattend-generator at the commit the Go port follows,
# for the golden-file tool. Usage: fetch.sh [commit]
set -eu
commit=${1:-27237f82e9a8498d9c5bab7d398dc34b7682a1e8}
here=$(cd "$(dirname "$0")" && pwd)
dest="$here/upstream"
if [ ! -d "$dest/.git" ]; then
  git clone --quiet https://github.com/cschneegans/unattend-generator "$dest"
fi
git -C "$dest" fetch --quiet origin
git -C "$dest" checkout --quiet "$commit"
echo "upstream at $(git -C "$dest" rev-parse HEAD)"
