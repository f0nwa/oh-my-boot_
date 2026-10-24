#!/bin/sh
# Moves the Go port of unattend-generator to another upstream commit:
# fetches it, copies resource/, writes the commit everywhere the port names
# it, regenerates the golden files with the original and runs the tests.
# What is left by hand: porting the C# changes listed at the end until
# `go test ./internal/unattend/` passes. Needs the .NET 8 SDK and git.
# Usage: scripts/unattend-update.sh <commit>
set -eu
[ "$#" -eq 1 ] || { echo "usage: $0 <commit>" >&2; exit 2; }
root=$(cd "$(dirname "$0")/.." && pwd)
tool="$root/tools/unattend-golden"
pkg="$root/internal/unattend"

old=$(sed -n 's/^const UpstreamCommit = "\([0-9a-f]*\)"$/\1/p' "$pkg/generator.go")
[ -n "$old" ] || { echo "UpstreamCommit not found in $pkg/generator.go" >&2; exit 1; }
"$tool/fetch.sh" "$1"
new=$(git -C "$tool/upstream" rev-parse HEAD)
echo "port: $old -> $new"

# resource/ is copied without changes.
rm -f "$pkg"/resource/*
cp "$tool"/upstream/resource/* "$pkg/resource/"

if [ "$old" != "$new" ]; then
  for f in "$pkg/generator.go" "$pkg/UPSTREAM.md" "$tool/fetch.sh"; do
    sed -i "s/$old/$new/g" "$f"
  done
fi

"$root/scripts/unattend-golden.sh"

echo
echo "C# files changed upstream since the ported commit:"
git -C "$tool/upstream" diff --stat "$old" "$new" -- '*.cs' resource/ || true
echo
if (cd "$root" && go test ./internal/unattend/); then
  echo "The port matches the original. Update the date in $pkg/UPSTREAM.md."
else
  echo "Port the changes above (git -C $tool/upstream diff $old $new -- modifier/ Main.cs) until the test passes."
fi
