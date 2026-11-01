# The Remixware GitHub Action

`action.yml` in this repo is a composite action. It downloads a Remixware
release for the runner and checks it against the release's SHA256SUMS. Then
it runs `remixware check` (copied code and its licences) or `remixware deps`
(dependency changes against the base branch). Each finding or change that
breaks the policy becomes an annotation on the pull request, the job summary
gets the result, and the reports are uploaded as an artifact. It follows
the pattern in [PXTA's ACTION.md](https://github.com/softdevstudio/pxta/blob/main/ACTION.md).

## Copied code: `check`

```yaml
name: Licence check
on: pull_request
permissions:
  contents: read
jobs:
  remixware:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: softdevstudio/remixware@v0.4.0
        with:
          index-folders: |
            third_party
```

`index-folders` indexes the open source you might have copied from (a
vendored folder, or a checkout of a library made in an earlier step), and
the project is compared with it. Use `index` instead for `.rmx` files you
keep, `from` for SCANOSS results files, or `scanoss: "true"` to ask the
SCANOSS service. Each annotation sits on the copied lines and names the
source, its licence and what's missing. Before it reaches the job summary,
annotations or log, text from the project, its policy or an index is
cleaned, so a file name, a component name or an allow rule's reason can't
forge a workflow command or add Markdown.

## Dependency changes: `deps`

```yaml
name: Dependency gate
on: pull_request
permissions:
  contents: read
jobs:
  deps:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: softdevstudio/remixware@v0.4.0
        with:
          command: deps
```

On a pull request, the base is `origin/<base branch>`. The action fetches
it (one commit) when the checkout doesn't have it, so the default
`fetch-depth: 1` works (it uses the checkout's credentials, so keep
`actions/checkout`'s default `persist-credentials` on a private
repository, or fetch the base yourself and set `base-ref`). The policy
comes from the base branch
(`base-policy: true`), so a pull request can't allow its own packages.
Outside a pull request, set `base-ref` (for example `origin/main` after
fetching it). Each failing change is annotated on the lockfile line that
names the package. `online: "true"` adds the install-script and publisher
checks against the npm registry and PyPI.

## Inputs and outputs

| Input | Default | What it does |
|---|---|---|
| `command` | `check` | `check` or `deps`. |
| `path` | `.` | Project folder. |
| `index` | | `check`: `.rmx` index files, one per line. |
| `index-folders` | | `check`: folders to index first, one per line. |
| `from` | | `check`: SCANOSS or `remixware scan --json` results files, one per line. |
| `scanoss` | `false` | `check`: also ask the SCANOSS service (fingerprints only, never code). A key goes in `env: SCANOSS_API_KEY`. |
| `base-ref` | | `deps`: revision to compare with; empty on a pull request means the base branch. |
| `base-policy` | `true` | `deps`: use the base's `.remixware.json` (ignored when `policy` is set). |
| `online` | `false` | `deps`: also ask the registries. |
| `policy` | | Policy file; empty uses `.remixware.json` in the project. |
| `fail-on` | | Statuses that fail, comma separated; empty uses the policy's or the defaults. |
| `args` | | More options for the command, such as `--exclude testdata`. |
| `out` | `remixware-report` | Report folder. |
| `upload` | `true` | Upload the reports as an artifact. |
| `artifact-name` | `remixware-report` | Artifact name (needs to differ when the action runs twice in a job). |
| `fail` | `true` | Fail the step when the policy is broken; `false` reports only. |
| `version` | | Release tag (v0.4.0 or later) or `latest`. Empty runs the release the action is pinned to (`@v0.4.0` runs 0.4.0); a branch or SHA pin runs the latest. |
| `remixware-path` | | Use this binary instead of downloading one (for testing the action). |

Outputs: `result` (`pass` or `fail`), `exit-code` (0 pass, 1 policy broken,
2 usage or input error), `version` (the release that ran) and `report`
(the folder). A usage or input error, such as an unreadable lockfile or
index, always fails the step, whatever `fail` says. The artifact holds
`remixware.json` and `remixware.html` for `check`, and `deps.json` for
`deps`. It is uploaded only when the command finished (exit 0 or 1).

The flags behind this, `--format github` and `--summary FILE`, work
without the action too; the README's "Commands" section describes them.
