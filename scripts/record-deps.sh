#!/usr/bin/env bash
# Records the dependency gate's results in test/results/deps: its tests,
# the counts of the generated promise test, and the gate run on the
# fixture in testdata/deps (base is main, head is a change). It needs no
# network. record-results.sh runs it too.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
out="$root/test/results/deps"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
rm -rf "$out"
mkdir -p "$out"
cd "$root"

{
  echo "recorded: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "machine: $(uname -srm)"
  echo "go: $(go version)"
  echo "remixware: $(cat VERSION)"
} > "$out/environment.txt"

echo "== dependency gate tests"
go test -count=1 -race -v -run 'Deps|Parse(GoMod|NPMLock|Requirements|PoetryLock)' ./... > "$out/tests.txt" 2>&1 || true
if grep -qE "^(--- FAIL|FAIL)" "$out/tests.txt"; then echo "::error::dependency gate tests failed"; exit 1; fi
echo "passed: $(grep -c -- '--- PASS' "$out/tests.txt") tests"
grep -o 'promise: .*' "$out/tests.txt" | sed 's/^promise: //' > "$out/promise.txt"
cat "$out/promise.txt"

echo "== the gate on testdata/deps"
bin="$work/remixware"
go build -o "$bin" ./cmd/remixware
set +e
"$bin" deps testdata/deps/head --base testdata/deps/base --json "$out/fixture.json" > "$out/fixture.txt"
echo "exit code: $?" >> "$out/fixture.txt"
"$bin" deps testdata/deps/head --base testdata/deps/base --quiet > "$out/fixture-quiet.txt"
echo "exit code: $?" >> "$out/fixture-quiet.txt"
"$bin" deps testdata/deps/base --base testdata/deps/base > "$out/unchanged.txt"
echo "exit code: $?" >> "$out/unchanged.txt"
cp -R testdata/deps/head "$work/broken"
printf '{"lockfileVersion": 3, "packages": {' > "$work/broken/web/package-lock.json"
"$bin" deps "$work/broken" --base testdata/deps/base > "$out/broken.txt" 2>&1
echo "exit code: $?" >> "$out/broken.txt"
set -e
sed -i.bak "s#$work/##g" "$out/broken.txt" && rm -f "$out/broken.txt.bak"
grep -q '^exit code: 1$' "$out/fixture.txt" || { echo "::error::the gate didn't fail the fixture change"; exit 1; }
grep -q '^exit code: 0$' "$out/unchanged.txt" || { echo "::error::the gate failed an unchanged set"; exit 1; }
grep -q '^exit code: 2$' "$out/broken.txt" || { echo "::error::the gate didn't stop on a broken lockfile"; exit 1; }
tail -n 3 "$out/fixture.txt"
echo "recorded in $out"
