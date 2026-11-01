#!/usr/bin/env bash
# Reads every licence text of the SPDX License List with Remixware's
# licence detection and records the result in
# test/results/licences/spdx-corpus.txt (TestSPDXCorpus).
#
#   scripts/spdx-corpus.sh [CACHE]
#
# CACHE (default: a folder in the user cache) holds a sparse clone of
# github.com/spdx/license-list-data at the commit below. Needs git and
# network access the first time.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
commit=3184648
cache="${1:-${XDG_CACHE_HOME:-$HOME/.cache}/remixware}/spdx-license-list-data"
if [ ! -d "$cache/.git" ]; then
  git clone -q --filter=blob:none --no-checkout https://github.com/spdx/license-list-data "$cache"
fi
git -C "$cache" sparse-checkout set text >/dev/null 2>&1 || true
git -C "$cache" fetch -q --filter=blob:none origin || true
git -C "$cache" checkout -q "$commit"
mkdir -p "$root/test/results/licences"
cd "$root"
SPDX_TEXTS="$cache/text" SPDX_OUT="$root/test/results/licences/spdx-corpus.txt" \
  go test -count=1 -run TestSPDXCorpus -v . | grep -E "^(--- |ok|FAIL)|texts:"
echo "license-list-data $(git -C "$cache" log -1 --format='%h %cs')" >> "$root/test/results/licences/spdx-corpus.txt"
head -1 "$root/test/results/licences/spdx-corpus.txt"
