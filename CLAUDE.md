# Remixware: where the next session starts

Remixware finds code a project copied from open source, names the licence
it came with, and says what that licence asks (conflict, no licence,
review, attribution missing, attributed). Go, standard library only, one
binary. It connects to the network only when asked to, with `--scanoss`.

- `tokenize.go` + `lang.go` read source as tokens (comments and layout
  dropped, names kept with a hash). `fingerprint.go` numbers names inside
  each 17-token k-gram and winnows (window 12); it drops low-variety
  k-grams, k-grams repeated in a file, and the generated Go idioms in
  `idioms.go` (sort.Interface).
- `component.go` reads manifests and licence files; `license.go` holds the
  licence list, SPDX expressions, licence detection and the compatibility
  table (`evaluate`). `index.go` builds, saves and loads `.rmx` indexes
  (gzip, versioned header, refuses damaged files).
- `scan.go` chains shared fingerprints into regions, decides on evidence
  (fingerprints plus identical continuation), extends by shape and trims
  the edges back to whole copied lines (`trimEdges`). `findings.go` merges
  and joins findings. `scanoss.go` imports SCANOSS and Remixware JSON.
- `wfp.go` makes SCANOSS's own fingerprints (WFP: CRC32C winnowing of
  letters and digits, gram 30, window 64), ported from scanoss.py's MIT
  `winnowing.py` and checked byte for byte against it
  (`testdata/scanoss/wfp/expected.wfp`, made by `scripts/wfp-golden.py`).
  One known difference: scanoss.py's binaryornot takes some text files for
  binary by their first bytes ("BM", "MZ", "RIFF", "PACK"...) and sends no
  hashes for them; Remixware hashes every file it sends (it doesn't send
  files with NUL bytes near the start). `scanossapi.go` sends them to the service
  (`--scanoss`) in batches of 32 files or 32 KB, file names replaced by
  numbers, and reads the answers with the code that reads results files.
  The answers are untrusted input: only names that were sent count, every
  name sent must come back (the API server drops the files of a failed
  engine run and still answers 200), a response is read up to 64 MB, a file
  entry may hold at most 100 matches, redirects aren't followed (they'd
  carry the key), and reports and errors show the URL without user name,
  password or query.
- `clean.go` keeps text from elsewhere (SCANOSS answers, results and
  findings files, indexes made elsewhere) from acting on terminals:
  `oneLine` replaces only what a terminal acts on (C0 controls but tab,
  DEL, C1, U+2028/2029, bidi marks, invalid UTF-8) and keeps everything
  else, spacing and joiners included, because copyright lines are matched
  exactly (keepsNotice, credit), and those compare cleaned text with
  cleaned text (`textBlock` on the file, `oneLine` on notices), since an
  index's lines are cleaned on load. Don't widen it: the first version
  also collapsed spaces and dropped format characters, which made checks
  fail on credited code and dropped files with everyday Unicode names.
  `TerminalText` filters everything the CLI prints, `mdLine` escapes a
  copyright line that would start a Markdown block in notices, `importPath`
  drops untrusted paths a terminal could act on (counted as `dropped`,
  and `check` exits 2), while `projectPath` stays a shape check for the
  reports, which read Remixware's own paths. Limits: 100 matches per
  project file in SCANOSS results (counted per cleaned path, case-folded
  on macOS and Windows), 100 licences/copyrights/purls per match, 20,000
  findings per file in a findings file (an amalgamation of 1,100 indexed
  files gives 1,100), 100 copyright lines per imported finding.
- `nearby.go` finds credit kept with the code (0.2.1, from the case
  studies): licence and notice files in the copy's folder and the folders
  above it, then anywhere in the project outside the folders a scan skips
  (a licence in vendor/ doesn't credit code copied out of it; the shopfront
  fixture tests that), matched with the same needles as notices (full
  copyright line, name with version, URL, PURL). A source whose licence
  names no copyright holder (bare Apache or MPL text) is credited only by
  its own licence text kept whole in the nearest folder below the root that
  has a licence file, plus its NOTICE text when it ships one; a licence
  text at the root could be any other library's (TestLicenceTextIsNotCredit,
  from the 0.1.1 review). At the root, nearby reads licence files only: the
  root's notice files are readNotices' job, and a policy's notice_files
  replaces them. Source and build files named like notices (attribution.go,
  ThirdParty.cmake) aren't notices, here and in readNotices (noticeText). The walk leaves out --exclude and the policy's
  ignore (Results.Exclude carries --exclude). Symbolic links aren't
  followed; the project root is resolved first (NewResults uses realPath,
  like Scan). Reads stop at two separate 64 MB budgets, one for the folders
  above copies and one for the walk (20,000 files), so the walk can't use up
  what a copy's own folder needs; past them, files go unread, each counted
  once in Skipped["licence-files"], reset on every Evaluate.
  Credited findings carry `credited: true`, and `notice` skips them.
- `policy.go` evaluates statuses, notice files and allow rules;
  `notice.go` writes notices with the texts in `licensetexts/` (from the
  SPDX License List); `report.go` + `report.html.tmpl`, `spdx.go` write
  the reports. `cmd/remixware` is the CLI.
- Tests: `unit_test.go`, `fixtures_test.go` (testdata/upstream is small
  open source written for the tests; testdata/projects copy from it),
  `cmd/remixware/main_test.go` (exit codes, the notice loop).
- `scripts/bench` clones real projects (scripts/bench/corpus.json) into a
  cache outside the repo and measures recall, wrong sources, false alarms
  and speed.
- `scripts/case_studies.py` records the site's case studies in
  test/results/case-studies: typer 0.12.5 and 0.27.3 against Click, coyim
  against xmpp-client, and pip 26.2.2's `_vendor` folder against the 18
  releases its vendor.txt lists, downloaded from PyPI and checked against
  PyPI's SHA-256. record-results.sh runs it after the gallery.

- `depslock.go` reads lockfiles for `remixware deps` (0.3.0) from a folder
  (`DepsDir`) or a git revision (`DepsGit`: `git ls-tree`/`cat-file`, no
  fetch): go.mod + go.sum, package-lock.json v1-3, requirements files
  (`-r`, `# via`, index options recorded as sources), poetry.lock (strict:
  junk lines and unclosed arrays are errors). `deps.go` holds the `deps`
  policy keys, the diff (new = ecosystem + name absent from every base
  lockfile) and the checks; `registry.go` the opt-in `--online` lookups
  (npm packuments, PyPI JSON) with a JSON cache of immutable per-version
  facts. `cmd/remixware/deps.go` is the command. Every reader fails loudly;
  a gate that skipped a broken lockfile would pass what it hides.
  `scripts/record-deps.sh` records test/results/deps (no network).

Site repo: github.com/photozeitgeist/remixware.com (Hugo, Launch template,
content and config values only; never touch the theme).

## Workflow (low maintenance)

- Pull before working: ChatGPT/Codex also works on this repo (see AGENTS.md).
- The owner does not publish releases. Raise the version in `VERSION` and
  `version.go`, add a `CHANGELOG.md` section, push to main. When CI is
  green, `release.yml` builds Linux/macOS/Windows archives, smoke-tests
  them (index, check and scan of the fixtures), tags `vX.Y.Z` and
  publishes "Remixware vX.Y.Z" with SHA256SUMS. Then watch the run, check
  the release files and links, and run a downloaded binary.
- Every number on the site comes from `test/results/`, written by
  `scripts/record-results.sh` (needs git and network the first time, for
  the benchmark corpus; set REMIXWARE_BENCH_CACHE to reuse a cache).
  Re-record after any change that could move a number, and update the site
  from the new files. The gallery reports on remixware.com are copied from
  `test/results/gallery`.
- An independent review agent looks at code and site before a release is
  called done.
- Check the site after a deploy once, and stop: the Cloudflare Pages
  check on the site commit (`gh api
  repos/photozeitgeist/remixware.com/commits/SHA/check-runs`) plus one page
  not fetched before in the session, or a fetched one with a query added
  (`?v=0.2.1`). WebFetch keeps each address's copy for about 15 minutes,
  so fetching the same address again returns the old page. Never re-fetch
  it, wait out a cache or blame Cloudflare. If a page looks stale, say so
  in one line and move on. (2026-10-26: rechecking the homepage's stored
  copy wasted the owner's usage.)
- CI never touches the network. `.github/workflows/scanoss-live.yml` (run by
  hand: `gh api -X POST repos/softdevstudio/remixware/actions/workflows/scanoss-live.yml/dispatches -f ref=main`)
  asks the real SCANOSS service about copies of logrus and reports what it
  found as annotations. Run it after any change to `wfp.go` or
  `scanossapi.go`; these sessions can't reach the service themselves. The
  free service keeps a monthly limit and turned all of GitHub's runners
  away from 2026-10-26 to the end of that month (HTTP 503 with a wait until
  the 1st). When that happens, `scripts/scanoss_live.py request DIR`
  captures Remixware's request and writes a `submit.js` that posts it from
  a browser tab on https://api.osskb.org/ (the owner's built-in browser
  pane, with his OK, worked for 0.2), and `record DIR` replays the saved
  answer into `test/results/live`. Don't commit the service's answers: its
  terms allow them for software identification only.
- main is the default branch. No side branches.
- GitHub job logs can't be downloaded from these sessions, but annotations
  can. Workflows print `::error::` lines for that reason. Read the real
  error before any rerun; at most two fix-and-retry rounds on one failure,
  then stop and report what's known and the options. Time-box a stuck
  problem to 15 minutes and report early.
- Don't push to main while a release run is going (the release must tag
  main's head).
- In workflow steps, write a command's output to a file and grep the
  file. `cmd | grep -q` fails under pipefail when grep stops reading
  early (SIGPIPE); that broke the first 0.1.0 release run on arm64.
- The release runs after *any* CI run on main completes, and builds the
  commit that CI run tested. Raise VERSION only in a commit you want
  released.
- Statuses, JSON field names and the `.remixware.json` keys are a public
  interface: never rename one. Add new ones instead, and document them in
  README.md.
- In the sandbox, don't `rm -rf` with globs or variables (the safety check
  blocks it); delete named paths.
- Any change to tokenizing or fingerprinting must bump `normVersion` in
  fingerprint.go, so old indexes are refused instead of quietly matching
  less. `TestFingerprintsMatchNormVersion` fails until the digest for the
  new version is recorded in it.
- Indexes and results files from elsewhere are untrusted input: keep
  `Index.validate`, `Index.readable`, the checksum check in
  `sourceTokens` and `projectPath` in the way of every file read they could
  steer, keep `LoadIndex` (and `decodeHeader`) from allocating for what a
  file only claims, and never let an index make a scan skip a project file.
  A change to the index layout bumps `indexFormat`.

## State

0.1.0 (2026-10-26): index, scan, check, notice, licenses; SCANOSS and
Remixware JSON import with `--from`; policy file with allow rules; HTML,
JSON and SPDX 2.3 reports; 15 syntax families; CRLF-safe. Benchmark on 14
real projects plus 9 unrelated ones recorded in test/results (licences of
all 14 detected as expected; 0 wrong sources; the only matches in the
unrelated projects are typer's real copies of click). The first
independent review found 16 problems (file headers hiding copyleft, EUPL
read as GPL, crafted indexes reading local files, checks passing on empty
scans, loose credit matching, SPDX snippets without byte ranges, and
more). The second found 13 more, several from the first round's fixes:
an index holding the project hid its sub-folders, crafted indexes could
still show local files or crash a scan, review verdicts passed check
without a notice, "Alternatively" headers read as AND, identical large
files split into many findings, and others. A third, focused check of
those fixes found 10 more, among them a GPL notice's "either version 2, or
(at your option) any later version" read as a choice of licences, vendored
GPL code inside an indexed project counted as the project's own, and a
made-up index still able to make a scan skip a folder (scans no longer
skip anything an index names). All were fixed with tests before release;
index format 2 adds file checksums. Released 2026-10-26 as v0.1.0: five
archives plus SHA256SUMS, checked by downloading the Linux build,
verifying its checksum and running it on the fixtures.

0.1.1 (2026-10-26): the independent review of remixware.com found that
copyright lines were read from inside licence texts (item "(c) under
Patent Claims ..." of the MPL-2.0, "(c) You must retain ..." of a wrapped
Apache-2.0 text, the FSF's line at the top of the GPL). A copyright line
counts as credit, so another library's notice with the same licence text
credited the copy. `copyrightLines` now skips item (c) of a lettered list
(near item (b), or in lower case after a list, and without a company, an
address or "all rights reserved"), licence documents' own copyrights and
templates (placeholders in brackets, whole template words). A re-check
found the first version of this too eager ("Holder Technologies Inc.",
"<https://...>"); those cases are tests now. Checked against all 826 SPDX
licence texts and about 19,000 real licence files and source headers. The
fixtures link to example domains (one linked an unrelated npm package).
Released as v0.1.1 the same day; the downloaded Linux build was checked
(checksum, fixtures, the MPL credit case), and remixware.com went up with
its gallery recorded from it.

0.2.0 (2026-10-26): `--scanoss` asks the SCANOSS service directly.
`wfp.go` makes scanoss.py's fingerprints (byte for byte on its golden
files and on 3,399 real files); `scanossapi.go` sends them with numbered
names. The live check workflow got no answer: the free service turned
GitHub's runners away until 2026-11-01 (monthly limit), scanoss-py too.
With the owner's OK, the request captured from Remixware went out from
his Mac through the linked built-in browser pane, and the answer,
replayed to Remixware, traced both logrus copies (test/results/live).
Reviews: the first found 11 problems (an answer leaving files out was
accepted, so a check could pass; redirects carried the key; answer text
could drive the terminal; unbounded work; odd file names lost; URL
secrets in reports). A re-check found those fixes' cleaning too broad
(credited code failing, files with everyday Unicode names dropped so
`check --from` could pass, quadratic copyright work). A final check found
a tab-led Markdown hole, cleaned index lines no longer matching raw
headers, and a findings cap refusing amalgamations. All fixed with tests;
the fixtures and the benchmark corpus (234 outputs, both index versions)
give the same results as 0.1.1 apart from the notice hint. A site review
found 17 wording problems, fixed before publishing. Released 2026-10-26
as v0.2.0: five archives plus SHA256SUMS. The downloaded Linux build was
checked (checksum, fixtures, and the live answer replayed to it through
`--scanoss`), and remixware.com went up with its gallery recorded from it.

0.2.1 (2026-10-26): the owner asked for case studies on real projects
(three posts tagged "case studies", a Case Studies menu link). Running
them showed 0.2.0 reading credit only from notice files at a project's top:
typer 0.27.3 keeps Click's licence in typer/_click/LICENSE.txt and coyim
keeps xmpp-client's in LICENSE.xmpp-client, yet both came out "attribution
missing". The owner chose to fix first. nearby.go reads licence files kept
with the code; a licence file that points to others (packaging's LICENSE)
is read as that choice; archives named like licences are skipped; an
.egg-info package in a PyPI source release takes the release's licence
text, copyright lines and NOTICE; pyproject licence words ("ISC License")
map to SPDX ids. Results with 0.2.1: typer 0.12.5 has 5 copies of Click
without Click's notice; typer 0.27.3 has 61, all credited; coyim's 17
copies are credited; pip's vendored folder has 226 copies, all credited
except requests' 17 (pip keeps its LICENSE but not its NOTICE, which the
requests release ships). The independent review found 9 defects in the new
code, among them source files named like notices (attribution.go) crediting
copies, a stray "either" in a short licence file turning AND into OR, texts
inherited by npm packages and across _vendor, a symlinked root losing
credit with --from, order-dependent budgets, --exclude ignored by the
walk, and `notice` writing entries for credited MPL copies; plus 9 wording
problems in the posts. A focused re-check of those fixes found 8 more:
build files named like notices (ThirdParty.cmake) crediting copies,
readSmall allocating its whole limit per file (33 s for 20,000 small
licence files), NOTICE and licence texts cut differently by an index and a
findings file (so --index and --from disagreed), unread files counted
twice, "either way" read as a choice, and wording. All fixed with tests;
both reviewers' reproductions pass. Released 2026-10-26 as v0.2.1: five
archives plus SHA256SUMS. The downloaded Linux build was checked
(checksum, the fixtures' findings and check exit codes, typer 0.27.3
against Click 8.3.1 giving 61 attributed), and remixware.com went up with
the three case studies, the Case Studies menu link and its gallery recorded
from 0.2.1. The recorded speeds (index 1.4 s, unrelated scan 169,000
lines/s) came out lower than 0.2.0's; three alternating benchmark runs of
each version gave the same speeds, so it's run-to-run noise.
0.3.0 (2026-10-31): the dependency change gate, asked for by the owner.
`remixware deps [project] (--base DIR | --base-ref REV)` compares the
change's lockfiles with main's and fails on `not-allowed` (a new direct or
transitive package no `deps.allow` rule names), `install-script`,
`publisher-changed` (online only: the new version's npm `_npmUser`, or for
PyPI the author/maintainer metadata, is neither publisher nor maintainer
of the version compared with, within `publisher_days`, default 90) and
`not-checked` (a changed lockfile kind it doesn't read, a new pip index,
an update moving off the public registry); `allowed`, `updated`,
`removed` don't fail. Offline by default (allow list; install scripts from
package-lock v2/v3 `hasInstallScript` and poetry.lock file lists); a
"Checks:" block and the JSON `checks` say what each check covered and why
the rest wasn't checked; an offline run never reports an online check.
Policy keys: `deps.allow` (`package`, `reason`, `install_scripts`,
`publisher_change`), `deps.fail_on`, `deps.lockfiles`,
`deps.publisher_days`. The output lists allow rules the change adds to the
policy; `--base-policy` uses main's. Tests: 29 dependency gate tests, and
TestDepsGatePromise on generated changes across the five formats: 400 new
unallowed packages all failed, 800 unchanged/allowed sets all passed, 80
new install scripts all failed, 400 broken lockfiles all stopped with an
error (test/results/deps/promise.txt). The release workflow smoke-tests
deps on the testdata/deps fixture (10 changes, 3 failing). No agent could
be spawned for the independent review, so it was a second pass of my own;
it found and fixed self-allowing policies, new pip indexes and packages
moved off the registry passing, poetry.lock junk swallowed inside arrays,
and JSON check statuses with spaces. The full record-results.sh wasn't
re-run (licence scanning unchanged; no site number comes from
test/results/tests.txt). Released 2026-10-31 as v0.3.0: five archives plus
SHA256SUMS, all latest/download links answer 200, the downloaded Linux
build's checksum matches and it prints 0.3.0 and fails the fixture change
with exit 1. remixware.com: post "Remixware Now Fails Pull Requests That
Bring In Risky Dependencies", reference (deps command, Dependency Gate
section, policy keys, exit codes), quick start step 6, download page smoke
test line, hero badge 0.3.

0.3.1 (2026-10-31): the independent agent review of the deps gate that
0.3.0 owed. Checked: every lockfile reader against crafted input (npm v1-3
and shrinkwrap, requirements against pip 26.2.1's own parser, poetry.lock,
go.mod/go.sum), name normalisation, aliases, direct/transitive, removals,
symlinks and -r/-c paths, --base-ref handling, the --online cache, exit
codes and JSON. 13 confirmed bugs, each with a failing-first test in
deps_review_test.go, all fixed: package-lock.json keys matched without case
by encoding/json ("LINK", "haſInstallScript", "LockfileVersion" hid
packages or scripts; now read as exact keys from maps); pip's Python line
breaks (\r, \f, U+2028...) hid requirements in comments; `-c` constraints
files' index options were ignored; test-/dev-requirements.txt weren't
read; VCS and source-archive requirements updated freely (now
install-script, as poetry's git packages were); poetry.lock `[[ package ]]`
headers, multi-line strings and inline or dotted `source` slipped past
(duplicate keys are now errors); go.work wasn't seen (now not-checked); a
new lockfile that's a symlink was skipped (links are now followed inside
the project, on disk and in git); deleting package-lock.json or poetry.lock
while keeping the manifest passed (now not-checked); policy edits to
fail_on, lockfiles, publisher_days and ignore weren't shown (now
`policy_changes`); the --online cache answered for a mirror with another
registry's facts (now keyed by registry). Not a bug: a comment ending in a
backslash joins the next line in pip too. The benchmark folder in
test/results stays from 0.2.1: the shared sandbox ran at load 6 on 2 cores,
so its speeds were noise; licence scanning is unchanged. Released as
v0.3.1 (tag at 3b571d1): five archives plus SHA256SUMS, every
latest/download link answers 200 to a GET (a HEAD gets 401 from the asset
host), the downloaded Linux build's checksum matches, and it prints 0.3.1
and fails the fixture change with exit 1. remixware.com: the reference's
Dependency Gate section covers the new behaviour, and the gallery was
copied again from test/results/gallery (only version and time changed).

## Gaps and to-do

Known gaps, not planned yet:

- Dependency gate (0.3.0): yarn.lock, pnpm-lock.yaml, Pipfile.lock,
  uv.lock, Cargo.lock and other lockfiles aren't read (a change to one is
  not-checked, failing by default). A package.json or pyproject.toml without
  a lockfile isn't read. Symbolic-link lockfiles are skipped on both sides.
  An npm package kept at the same version but resolved from another host
  isn't flagged (only updates that move off the registry are). PyPI has no
  uploader record, so its publisher check uses metadata and is weak. A
  base version that's gone from the registry makes an online run exit 2.
  Left open by the 0.3.1 review: npm git dependencies run `prepare` on
  install, which hasInstallScript doesn't record; a PyPI release with
  wheels for some platforms only counts as having a wheel (offline and
  online); `.npmrc` registry changes aren't seen (integrity hashes in
  package-lock still hold); symlinked folders holding lockfiles aren't
  walked; downgrades are plain updates; a v1 lockfile's alias
  (`npm:other@1.0.0`) is named by its alias, so an allow rule for the alias
  name allows it; lockfile names compare case-sensitively. Possible next:
  read a committed cache offline, pnpm/yarn readers, dogfood the gate in
  this repo's CI on pull requests.
- Copies under 50 tokens or 5 lines aren't reported, by design. Renamed
  copies of 50 to 99 tokens are found about half the time (see
  test/results/benchmark/recall.txt).
- Python functions are often short, so Python recall is the lowest.
- A file whose name holds a backslash or starts like a drive (`c:x.go`,
  possible on Linux and macOS) gets its findings from `--scanoss`, but
  reading the saved answers back with `--from` leaves it out (paths in
  results files are untrusted and may come from Windows), so that check
  exits 2.
- `--scanoss` findings are what SCANOSS says: Remixware doesn't hold the
  matched source, so the HTML report shows only the project's side of
  them, and a SCANOSS answer it can't map to a licence reads as review.
  SCANOSS may name a fork that has the same files (it named
  github.com/copejon/logrus for logrus code in the live check).
- The free SCANOSS service may limit how often it's asked and gives no
  guarantees; a large project means many requests (32 files or 32 KB each,
  4 at a time).
- "Names changed" matches can pair look-alike blocks against a big index:
  comparing pip's own code with the 18 releases it vendors paired an
  import block in pip/_internal/cache.py with one in a setuptools test, and
  a pytest list of date strings with a list of version specifiers. The
  benchmark's unrelated projects show none, and the pip case study scans
  only the vendored folder. Fixing it means judging renamed matches
  differently (imports, literal lists), then re-running the benchmark.
- A copy is named by where it was found, which can be a middleman: pip's
  change_root (from CPython's distutils) matched setuptools' copy of
  distutils, and pip's _jaraco_text.py matched the jaraco.text that
  setuptools vendors without its licence, so both read as setuptools' MIT
  code with attribution missing.
- A copy that keeps its source's header counts as credited without the
  source's NOTICE text; nearby.go asks for the NOTICE, keepsNotice doesn't.
- A source whose licence names no copyright holder is credited by its
  licence text in the nearest folder with a licence file, so a copy put
  into another library's folder with the same bare text would count.
- Licence detection that the 0.2.1 review saw and that 0.2.0 shares: the
  SPDX OFL-1.1 text is read as MIT-0 (so OFL code would need no notice); a
  BSD-3 text without "Neither the name of" (go-difflib) is read as BSD-2;
  licence files named after licences (LICENSE-APACHE and LICENSE-MIT,
  COPYING.GPL and COPYING.LGPL) are read as a choice even when a short
  LICENSE says the parts differ.
- Licence texts outside the eight built in are taken from the component's
  own licence file; when the index doesn't have it, `notice` asks the user
  to finish the entry by hand.
- A licence file holding the full texts of two licences is read as the
  first, unless the second is a condition such as the Commons Clause.
- When a manifest and a licence file name different licences, the
  manifest's wins.
- A Cargo workspace member indexed without its workspace folder has no
  licence from the workspace, so its copies show as no-license.
- Header choices are read from their wording ("dual licensed", "at your
  option", "either the X or the Y", "Alternatively ... X"). Other wordings
  read as all the named licences applying, the safe side.
- Symbolic links in a project are counted, not followed: code in a linked
  file or folder isn't checked unless it's in the project too.
- The Flask docs theme in requests (a BSD text with an extra request) is
  reported as LicenseRef-Unrecognised, which is honest but means review.
- Python namespace packages installed without RECORD files (rare, old
  installers) take the licences of every distribution sharing the folder.
- A licence file that points elsewhere and then quotes a different licence
  for a binary (Go's crypto/internal/boring LICENSE: Go licence for the
  code, OpenSSL for a .syso) is read as the quoted licence.
- GPL-1.0 combinations are handled only roughly; it's rare.
- The workflows use actions/checkout@v4, setup-go@v5 and
  upload/download-artifact@v4, which GitHub now runs on Node 24 with a
  deprecation warning. Newer majors exist (checkout v7 in 2026); move one
  at a time in a quiet moment and watch CI.

## Next

Nothing is approved after 0.3.0. Ask the owner before starting anything
new.

## Done: deps case studies (2026-10-31)

`scripts/deps_case_studies.sh REMIXWARE CACHE` records the released 0.3.1
gate, offline, on three real commits and the use example recipes
(test/results/case-studies/deps, README there): TypeScript 4139807e7
(esbuild: 23 added, 1 install-script, passes with inputs/esbuild.json),
Hugo c7165589 (gocloud.dev: 18 new modules, 2 direct; 12 left with an
example Go/Google allow list), pytorch/serve b260776b5 (2 new pip indexes
not-checked, 2 not-allowed, 6 updated). remixware.com (3758e5c): three
case study posts, "Five Tested Recipes for the Remixware Dependency Gate"
(use cases), links from the dependency gate post and the quick start.

Found during case studies: an unpinned requirement (`neuronx-cc`, no
version) shows an empty version, in the text (`pypi/neuronx-cc , added`) and
in the JSON (`"to": [""]`, numpy's `from` starts with ""); cosmetic, not
fixed here. Not a bug, worth a line in the docs: with `deps.lockfiles` set,
a new lockfile not on the list isn't read, so a new requirements file with
`--extra-index-url` passes (recipes/monorepo-lockfiles.txt).
