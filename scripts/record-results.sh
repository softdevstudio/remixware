#!/usr/bin/env bash
# Runs every test and the benchmark on real open source code, and records
# the output in test/results. It also writes the sample report gallery to
# test/results/gallery. Every number on remixware.com comes from these files.
#
# The benchmark clones the projects listed in scripts/bench/corpus.json at
# pinned versions into a cache folder outside this repository
# (REMIXWARE_BENCH_CACHE, default ~/.cache/remixware-bench), so the first run
# needs git and network access.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
res="$root/test/results"
cache="${REMIXWARE_BENCH_CACHE:-$HOME/.cache/remixware-bench}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
rm -rf "$res"
mkdir -p "$res/benchmark" "$res/gallery"
cd "$root"

{
  echo "recorded: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "machine: $(uname -srm)"
  echo "cpus: $(getconf _NPROCESSORS_ONLN 2>/dev/null || echo ?)"
  echo "go: $(go version)"
  echo "remixware: $(cat VERSION)"
} > "$res/environment.txt"
cat "$res/environment.txt"

echo "== all tests"
go test -count=1 -race -v ./... > "$res/tests.txt" 2>&1 || true
grep -E "^(--- |ok|FAIL)" "$res/tests.txt" | grep -v "^--- PASS" || true
if grep -qE "^(--- FAIL|FAIL)" "$res/tests.txt"; then echo "::error::tests failed"; exit 1; fi
echo "passed: $(grep -c -- '--- PASS' "$res/tests.txt") tests and subtests"

echo "== benchmark"
go run ./scripts/bench -cache "$cache" -out "$res/benchmark" | tee "$res/benchmark/log.txt"

echo "== sample report gallery"
bin="$work/remixware"
go build -o "$bin" ./cmd/remixware
ix=upstream.rmx
trap 'rm -rf "$work" "$root/$ix"' EXIT
g="$res/gallery"
"$bin" index testdata/upstream --out "$ix" > "$g/index.txt" 2>/dev/null
for p in shopfront firmware clean; do
  "$bin" scan "testdata/projects/$p" --index "$ix" --quiet \
    --html "$g/$p.html" --json "$g/$p.json" --spdx "$g/$p.spdx.json" > "$g/$p.txt"
done
"$bin" scan testdata/projects/shopfront --index "$ix" --from testdata/scanoss/shopfront.json \
  --quiet --html "$g/shopfront-with-scanoss.html" --json "$g/shopfront-with-scanoss.json" > "$g/shopfront-with-scanoss.txt"
"$bin" check testdata/projects/shopfront --index "$ix" > "$g/shopfront-check.txt" 2>/dev/null \
  && { echo "::error::check passed on shopfront, which has conflicts"; exit 1; }
"$bin" notice testdata/projects/shopfront --index "$ix" --quiet > "$g/shopfront-notices.md" 2>/dev/null
# Paths in the reports are shown from the repository root, not this machine.
for f in "$g"/*.json "$g"/*.html "$g"/*.txt; do
  sed -i.bak "s#$root/##g" "$f" && rm -f "$f.bak"
done
python3 - "$g" > "$res/gallery.txt" <<'PY'
import json, os, sys
d = sys.argv[1]
for name in sorted(os.listdir(d)):
    if not name.endswith(".json") or name.endswith(".spdx.json"):
        continue
    j = json.load(open(os.path.join(d, name)))
    c = j.get("counts", {})
    print(f"{name[:-5]}: licence={j['project_license']} files={j['files_scanned']} lines={j['lines_scanned']} "
          f"findings={len(j['findings'])} " + " ".join(f"{k}={c[k]}" for k in sorted(c)))
PY
cat "$res/gallery.txt"
echo "recorded in $res"
