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
  `scanossapi.go` sends them to the service (`--scanoss`) in batches of 32
  files or 32 KB, file names replaced by numbers, and reads the answers with
  the code that reads results files. The answers are untrusted input: only
  names that were sent count, and a response is read up to 64 MB.
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

## Gaps and to-do

Known gaps, not planned yet:

- Copies under 50 tokens or 5 lines aren't reported, by design. Renamed
  copies of 50 to 99 tokens are found about half the time (see
  test/results/benchmark/recall.txt).
- Python functions are often short, so Python recall is the lowest.
- `--scanoss` findings are what SCANOSS says: Remixware doesn't hold the
  matched source, so the HTML report shows only the project's side of
  them, and a SCANOSS answer it can't map to a licence reads as review.
- The free SCANOSS service may limit how often it's asked and gives no
  guarantees; a large project means many requests (32 files or 32 KB each,
  4 at a time).
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

Approved by the owner on 2026-10-26: 0.2 adds a built-in `--scanoss`
backend, so `remixware scan --scanoss` asks the SCANOSS service directly
instead of reading a results file. Ask the owner before starting it.
