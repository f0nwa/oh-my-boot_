#!/bin/sh
# Regenerates the golden files of internal/unattend with the original C#
# generator. Needs the .NET 8 SDK; the Oh My Boot build does not.
# Usage: scripts/unattend-golden.sh [file.json …]   (default: all)
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
tool="$root/tools/unattend-golden"
[ -d "$tool/upstream/.git" ] || "$tool/fetch.sh"
commit=$(git -C "$tool/upstream" rev-parse HEAD)
dotnet build "$tool/UnattendGolden.csproj" -c Release -o "$tool/bin/out" -p:UpstreamCommit="$commit" -nologo -v quiet >/dev/null
if [ "$#" -eq 0 ]; then
  set -- "$root"/internal/unattend/testdata/golden/*.json
fi
dotnet "$tool/bin/out/UnattendGenerator.dll" "$@"
