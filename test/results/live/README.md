# Live check against the SCANOSS service

On 2026-10-26 the free SCANOSS service (api.osskb.org) turned away GitHub's
runners for the rest of the month: HTTP 503, "Rate limit exceeded", with a
wait until 2026-11-01 00:00 UTC. It gave `remixware scan --scanoss` that
answer on Linux, macOS and Windows runners. SCANOSS's own scanner, run next
to it, got the same answer on macOS and none at all on Linux and Windows. So
the live check workflow couldn't get a result. This check was made from
another connection instead, with `scripts/scanoss_live.py`:

1. `request` built the test project, the same one the workflow uses:
   logrus v1.9.3's `text_formatter.go` copied whole as
   `internal/logging/format.go`, and its `WithFields` function inside a
   file of our own, `internal/fields/fields.go`. It captured the request
   remixware sends for that project. `request.wfp` is all of it apart from
   the `format=plain` field: two files named `1.go` and `2.go`, each with
   its MD5 checksum, its size and the hashes of its text.
2. That request went to `https://api.osskb.org/scan/direct` from a browser
   on the owner's connection, in the same multipart form.
3. `record` replayed the service's answer to remixware through a stand-in
   server. `check.txt` is what remixware reported.

SCANOSS traced both files to logrus, under the name of a fork that has the
same files (`github.com/copejon/logrus`): `format.go` as a whole-file match
of `text_formatter.go`, and lines 7-31 of `fields.go` as a snippet of
`entry.go` lines 126-150, both under MIT. The service's answer itself isn't
kept here, since its terms allow the information it gives to be used for
software identification only.

`scripts/record-results.sh` leaves this folder alone.
