#!/usr/bin/env bash
# Records the dependency gate case studies and use examples on
# remixware.com: `remixware deps` run on real dependency changes from the
# git history of public projects, offline.
#
#   scripts/deps_case_studies.sh REMIXWARE CACHE [OUT]
#
# REMIXWARE is the binary to run (the released one, from releases/latest),
# CACHE a folder outside the repository for the clones, OUT the results
# folder (default test/results/case-studies/deps). Each project is fetched
# shallowly at two commits, the commit that changed its dependencies and
# its parent, and the gate compares the two with --base-ref. Needs git and
# network access for the clones, jq for one recipe; the gate itself runs
# offline. Nothing here is the projects' code: only what Remixware printed.
#
#   typescript  microsoft/TypeScript 4139807e7, "Add build via esbuild"
#   hugo        gohugoio/hugo c7165589, "Add a "deploy" command"
#   serve       pytorch/serve b260776b5, "Enable opt-6.7b benchmark on inf2"
#   recipes     the use examples, run on a small git repository made from
#               the TypeScript lockfiles and a monorepo made from all three
set -uo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
bin="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
cache="$(mkdir -p "$2" && cd "$2" && pwd)/deps-case-studies"
out="${3:-$root/test/results/case-studies/deps}"
mkdir -p "$out" && out="$(cd "$out" && pwd)"
inputs="$root/test/results/case-studies/deps/inputs"   # policies and the workflow, kept by hand
export GIT_AUTHOR_NAME=case GIT_AUTHOR_EMAIL=case@example.com GIT_COMMITTER_NAME=case GIT_COMMITTER_EMAIL=case@example.com
export GIT_AUTHOR_DATE=2026-10-31T00:00:00Z GIT_COMMITTER_DATE=2026-10-31T00:00:00Z
export XDG_CACHE_HOME="$cache/xdg"   # --online is never used; keep any cache out of $HOME anyway

# fetch NAME URL HEAD: a shallow clone holding HEAD and its parent (depth
# 2), checked out at HEAD; prints the parent's hash.
fetch() {
  local dir="$cache/$1"
  if [ ! -d "$dir/.git" ]; then
    git init -q "$dir" && git -C "$dir" remote add origin "$2"
  fi
  git -C "$dir" fetch -q --depth 2 origin "$3" || { echo "fetch $1 failed" >&2; exit 2; }
  git -C "$dir" checkout -q --force "$3" && git -C "$dir" clean -qfdx
  git -C "$dir" rev-parse "$3^"
}

# record FILE DIR ARGS...: runs the gate in DIR, writes the command, the
# output and the exit code to FILE (paths in FILE are relative to DIR).
record() {
  local file="$1" dir="$2"; shift 2
  {
    echo "\$ remixware $*"
    (cd "$dir" && "$bin" "$@" 2>&1)
    echo "exit code: $?"
  } > "$out/$file"
  echo "$file: $(tail -1 "$out/$file"), $(grep -E '^(PASS|FAIL)' "$out/$file" | head -1)"
}

# summary NAME JSON: the counts the site quotes, from the gate's JSON.
summary() {
  jq -r '"lockfiles: " + ([.lockfiles[] | "\(.path) (\(.base_packages) -> \(.head_packages) packages)"] | join(", ")),
         "changes: \(.changes | length)",
         "counts: " + ([.counts | to_entries[] | "\(.key) \(.value)"] | join(", ")),
         "added: \([.changes[] | select(.change == "added")] | length) (direct \([.changes[] | select(.change == "added" and .direct)] | length))",
         "install scripts: " + ([.changes[] | select(.status == "install-script" or any(.issues[]?; .status == "install-script")) | .package] | join(", ")),
         "checks: " + ([.checks[] | "\(.name) \(.status)"] | join("; "))' "$1"
}

{
  echo "recorded: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "machine: $(uname -srm)"
  echo "remixware: $("$bin" version)"
  echo "git: $(git --version)"
  echo "all runs offline (no --online)"
} > "$out/environment.txt"

# --- TypeScript: one package.json line, esbuild and its platform packages
ts_head=4139807e75af55d05e132fac81dd551160d60940
ts_base="$(fetch typescript https://github.com/microsoft/TypeScript.git "$ts_head")" || exit 2
mkdir -p "$out/typescript"
{ echo "project: https://github.com/microsoft/TypeScript"; echo "base: $ts_base"; echo "head: $ts_head"
  echo "head subject: $(git -C "$cache/typescript" log -1 --format=%s "$ts_head")"
  echo "head author date: $(git -C "$cache/typescript" log -1 --format=%as "$ts_head")"
  echo "head commit date: $(git -C "$cache/typescript" log -1 --format=%cs "$ts_head")"
  echo; echo "package.json change:"; git -C "$cache/typescript" diff "$ts_base" "$ts_head" -- package.json
} > "$out/typescript/commits.txt"
record typescript/gate.txt "$cache/typescript" deps . --base-ref "$ts_base" --json "$out/typescript/gate.json"
record typescript/gate-quiet.txt "$cache/typescript" deps . --base-ref "$ts_base" --quiet
record typescript/gate-allowed.txt "$cache/typescript" deps . --base-ref "$ts_base" --policy "$inputs/esbuild.json"
summary "$out/typescript/gate.json" > "$out/typescript/summary.txt"

# --- Hugo: the deploy command brings in gocloud.dev and its cloud SDKs
hugo_head=c7165589b3be5923f1275f0024818e6ae889d881
hugo_base="$(fetch hugo https://github.com/gohugoio/hugo.git "$hugo_head")" || exit 2
mkdir -p "$out/hugo"
{ echo "project: https://github.com/gohugoio/hugo"; echo "base: $hugo_base"; echo "head: $hugo_head"
  echo "head subject: $(git -C "$cache/hugo" log -1 --format=%s "$hugo_head")"
  echo "head author date: $(git -C "$cache/hugo" log -1 --format=%as "$hugo_head")"
  echo "head commit date: $(git -C "$cache/hugo" log -1 --format=%cs "$hugo_head")"
  echo "go.sum lines: $(git -C "$cache/hugo" show "$hugo_base:go.sum" | wc -l) -> $(git -C "$cache/hugo" show "$hugo_head:go.sum" | wc -l)"
  echo; echo "go.mod change:"; git -C "$cache/hugo" diff "$hugo_base" "$hugo_head" -- go.mod
} > "$out/hugo/commits.txt"
record hugo/gate.txt "$cache/hugo" deps . --base-ref "$hugo_base" --json "$out/hugo/gate.json"
record hugo/gate-quiet.txt "$cache/hugo" deps . --base-ref "$hugo_base" --quiet
record hugo/gate-allowed.txt "$cache/hugo" deps . --base-ref "$hugo_base" --policy "$inputs/go-project-modules.json" --quiet
summary "$out/hugo/gate.json" > "$out/hugo/summary.txt"

# --- pytorch/serve: two new requirements files with extra package indexes
serve_head=b260776b55283ab6080f00fec62bfcddf766e97d
serve_base="$(fetch serve https://github.com/pytorch/serve.git "$serve_head")" || exit 2
mkdir -p "$out/serve"
{ echo "project: https://github.com/pytorch/serve"; echo "base: $serve_base"; echo "head: $serve_head"
  echo "head subject: $(git -C "$cache/serve" log -1 --format=%s "$serve_head")"
  echo "head author date: $(git -C "$cache/serve" log -1 --format=%as "$serve_head")"
  echo; echo "requirements change:"; git -C "$cache/serve" diff "$serve_base" "$serve_head" -- requirements/
} > "$out/serve/commits.txt"
record serve/gate.txt "$cache/serve" deps . --base-ref "$serve_base" --json "$out/serve/gate.json"
record serve/gate-quiet.txt "$cache/serve" deps . --base-ref "$serve_base" --quiet
summary "$out/serve/gate.json" > "$out/serve/summary.txt"

# --- Use examples. A small repository whose main holds the TypeScript
# lockfiles before esbuild and a policy, and a pull request branch with
# the lockfiles after it.
rec="$out/recipes"; mkdir -p "$rec"
demo="$cache/demo-origin"; rm -rf "$demo" "$cache/demo"
git init -q -b main "$demo"
git -C "$cache/typescript" show "$ts_base:package.json" > "$demo/package.json"
git -C "$cache/typescript" show "$ts_base:package-lock.json" > "$demo/package-lock.json"
cp "$inputs/demo-main.json" "$demo/.remixware.json"
git -C "$demo" add -A && git -C "$demo" commit -qm "main"
git -C "$demo" checkout -qb add-esbuild
git -C "$cache/typescript" show "$ts_head:package.json" > "$demo/package.json"
git -C "$cache/typescript" show "$ts_head:package-lock.json" > "$demo/package-lock.json"
git -C "$demo" commit -qam "Add esbuild"
git -C "$demo" checkout -qb allow-itself
cp "$inputs/demo-pr-allows-itself.json" "$demo/.remixware.json"
git -C "$demo" commit -qam "Add esbuild and allow it"
git clone -q "$demo" "$cache/demo"

# 1. The GitHub Actions step, its run block executed as written, with
# base_ref = main (the install line downloads releases/latest).
cp "$inputs/deps.yml" "$rec/deps.yml"
python3 -c 'import sys, yaml; yaml.safe_load(open(sys.argv[1])); print("deps.yml: valid YAML")' "$rec/deps.yml" > "$rec/actions-yaml.txt"
git -C "$cache/demo" checkout -q add-esbuild
{
  echo "\$ (run block of deps.yml, base_ref=main)"
  (cd "$cache/demo" && export HOME="$cache/demo-home" && rm -rf "$HOME" && mkdir -p "$HOME" &&
    mkdir -p "$HOME/bin" &&
    curl -sSL https://github.com/softdevstudio/remixware/releases/latest/download/remixware_linux_amd64.tar.gz \
      | tar -xz -C "$HOME/bin" ./remixware &&
    "$HOME/bin/remixware" version &&
    "$HOME/bin/remixware" deps . --base-ref "origin/main" --base-policy --json deps.json --quiet 2>&1)
  echo "exit code: $?"
} > "$rec/actions-run.txt"
echo "recipes/actions-run.txt: $(tail -1 "$rec/actions-run.txt")"

# 2. An allow list with reasons, in the change's own policy file.
cp "$inputs/esbuild.json" "$rec/allow-policy.json"
record recipes/allow.txt "$cache/demo" deps . --base-ref origin/main --policy "$rec/allow-policy.json"

# 3. A pull request that allows its own packages, with and without
# --base-policy.
git -C "$cache/demo" checkout -q allow-itself
record recipes/self-allow.txt "$cache/demo" deps . --base-ref origin/main --quiet
record recipes/self-allow-base-policy.txt "$cache/demo" deps . --base-ref origin/main --base-policy --quiet

# 4. --json for other tools: the failing packages, one per line.
git -C "$cache/demo" checkout -q add-esbuild
(cd "$cache/demo" && "$bin" deps . --base-ref origin/main --json deps.json > /dev/null)
{
  echo "\$ jq -r '.changes[] | select(.status != \"allowed\" and .status != \"updated\" and .status != \"removed\") | \"\\(.status)\\t\\(.package)\\t\\(.to // [] | join(\",\"))\"' deps.json | head -4"
  jq -r '.changes[] | select(.status != "allowed" and .status != "updated" and .status != "removed") | "\(.status)\t\(.package)\t\(.to // [] | join(","))"' "$cache/demo/deps.json" | head -4
  echo "\$ jq '.counts' deps.json"
  jq '.counts' "$cache/demo/deps.json"
  echo "\$ jq -r '.checks[] | \"\\(.name): \\(.status)\"' deps.json"
  jq -r '.checks[] | "\(.name): \(.status)"' "$cache/demo/deps.json"
} > "$rec/json.txt"
echo "recipes/json.txt: $(wc -l < "$rec/json.txt") lines"

# 5. A monorepo with three lockfiles: web/ (npm, TypeScript's), api/ (Go,
# Hugo's) and ml/ (pip, a requirements file of pytorch/serve's).
mono="$cache/mono"; rm -rf "$mono" "$mono-base"
for side in base head; do
  d="$mono"; [ "$side" = base ] && d="$mono-base"
  t=$ts_head; h=$hugo_head; [ "$side" = base ] && t=$ts_base && h=$hugo_base
  mkdir -p "$d/web" "$d/api" "$d/ml"
  git -C "$cache/typescript" show "$t:package.json" > "$d/web/package.json"
  git -C "$cache/typescript" show "$t:package-lock.json" > "$d/web/package-lock.json"
  git -C "$cache/hugo" show "$h:go.mod" > "$d/api/go.mod"
  git -C "$cache/hugo" show "$h:go.sum" > "$d/api/go.sum"
  git -C "$cache/serve" show "$serve_base:requirements/common.txt" > "$d/ml/requirements.txt"
done
git -C "$cache/serve" show "$serve_head:requirements/neuronx.txt" > "$mono/ml/requirements-neuron.txt"
record recipes/monorepo.txt "$mono" deps . --base "$mono-base" --quiet
record recipes/monorepo-allowed.txt "$mono" deps . --base "$mono-base" --policy "$inputs/monorepo.json" --quiet
record recipes/monorepo-lockfiles.txt "$mono" deps . --base "$mono-base" --policy "$inputs/monorepo-lockfiles.json" --quiet

# Paths of the cache don't belong in the record.
grep -rl "$cache" "$out" | while read -r f; do sed -i "s#$cache/#CACHE/#g; s#$root/#./#g" "$f"; done
grep -rl "$root" "$out" | while read -r f; do sed -i "s#$root/#./#g" "$f"; done
echo "done: $out"
