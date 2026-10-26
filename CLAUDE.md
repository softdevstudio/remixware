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
found 17 wording problems, fixed before publishing.

## Gaps and to-do

Known gaps, not planned yet:

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

Nothing is approved after 0.2. Ask the owner before starting anything new.
