# go-graphify-test-runner

Uses AI to select the tests to run: it refreshes the local [graphify](https://pypi.org/project/graphifyy/) code graph, computes the diff from
`merge-base(HEAD, <base>)` to the working tree, then asks a decision model
(SystemOne / OpenRouter `jev-latest`) one yes/no question per test file and,
for each file it approves, one per test function. Only the selected tests run,
via `go test -run`.

## Install

### Homebrew (macOS / Linux)

```bash
brew install NSXBet/tap/graphify-test-runner
```

Homebrew-managed installs update with `brew upgrade graphify-test-runner` — the
tool detects a Homebrew install (Cellar/Caskroom path) and tells you so. Formula
is published to [`NSXBet/homebrew-tap`](https://github.com/NSXBet/homebrew-tap)
by the release workflow.

### Install script (macOS / Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/NSXBet/go-graphify-test-runner/main/install.sh | sh
```

Downloads the release binary for your OS and architecture (linux/darwin ×
amd64/arm64), verifies its SHA-256 against the release's `checksums.txt`, and
installs it. Honours `INSTALL_DIR` (default `/usr/local/bin`, else
`~/.local/bin`), `VERSION` (pin a tag), and `BASE_URL` (mirror). Windows users
should take the `.zip` from the [releases page](https://github.com/NSXBet/go-graphify-test-runner/releases).

### `go install`

```bash
go install github.com/NSXBet/go-graphify-test-runner@latest
```

`@latest` resolves to the highest release tag, and the Go toolchain embeds that
tag in the binary, so `graphify-test-runner version` reports the real version —
no extra step needed.

Either way, `graphify` must be on `PATH` and `OPENROUTER_API_KEY` set.

## Version

```bash
graphify-test-runner version   # v1.2.3, or "dev" for a local build
graphify-test-runner --version # same
```

The version comes from, in order: the value the release build injects
(`-ldflags -X .../internal/version.Version`), then the module version the Go
toolchain embeds (a `go install ...@<tag>` build), then `dev`.

## Staying up to date

On a normal run the tool checks (once a day, cached) whether a newer release
exists and prints a one-line hint to stderr:

```
A new version of graphify-test-runner is available: v1.2.0 (you have v1.1.0)
Update with:  graphify-test-runner upgrade
         or:  go install github.com/NSXBet/go-graphify-test-runner@v1.2.0
```

```bash
graphify-test-runner check-update   # report only
graphify-test-runner upgrade        # reinstall the latest release via go install
```

- The check never blocks a run: it is best-effort, time-boxed, and any failure
  is silent.
- `upgrade` picks the right mechanism for how the binary was installed:
  Homebrew installs (a Cellar/Caskroom path) run `brew upgrade`; everything else
  reinstalls via `go install`. The install script's `INSTALL_DIR` is honoured by
  re-running the script.
- Suppress it with `--no-update-check` or `GRAPHIFY_TEST_RUNNER_NO_UPDATE_CHECK=1`.
- It is skipped automatically for `--json` (so stdout stays a clean document)
  and for `version`/`help`.
- `GRAPHIFY_TEST_RUNNER_UPDATE_API` overrides the release API root (mirrors).
- `upgrade` validates the tag before shelling out to `go install`.

## Releases

Pushing a `v*` tag triggers `.github/workflows/release.yml`, which lints, tests,
and runs [GoReleaser](.goreleaser.yaml) to publish binaries for linux, darwin
and windows on amd64 and arm64, plus `checksums.txt`.

```bash
git tag v0.1.0 && git push origin v0.1.0
```

## Usage

```bash
graphify-test-runner                       # diff vs origin/main
graphify-test-runner --base main           # diff vs a local branch
graphify-test-runner --dry-run             # print the selection, don't run
graphify-test-runner --verbose             # stream the model exchange for auditing
graphify-test-runner --json                # machine-readable result on stdout
graphify-test-runner --json --verbose      # ... including the judging
graphify-test-runner -- -race -count=1     # everything after -- goes to go test
```

| Flag | Default | Meaning |
|---|---|---|
| `--repo` | `.` | repository path |
| `--base` | `origin/main` | base ref for `merge-base` |
| `--threshold` | `0.5` | `noul >= threshold` means run it |
| `--model` | `jev-latest` | decision model |
| `--endpoint` | OpenRouter `alpha/decisions` | decisions endpoint |
| `--dry-run` | `false` | print selection and `go test` commands, do not run |
| `--verbose` | `false` | print the full decisioning exchange to stderr |
| `--json` | `false` | emit the whole result as JSON on stdout |
| `--no-update-check` | `false` | skip the check for a newer release |

Anything after `--` is forwarded verbatim to `go test`, so all `go test` flags
keep working (`-- -race -count=1 -v`; `-run` is managed by the tool).

### `--verbose` — auditing the decision

`--verbose` writes the whole exchange with the decision model to **stderr**:
the state text, every question and its instructions, each HTTP `POST`, the raw
JSON response, and the parsed `noul` per question. Normal progress and the
final report stay on **stdout**.

### `--json` — machine-readable result

`--json` writes one JSON document to **stdout** — the merge-base, changed
files, both rounds (scores and what each selected), the cost, and the final
selection grouped by package:

```json
{
  "merge_base": "dc54a1ab08a8",
  "changed_files": ["p/a.go"],
  "state_chars": 313,
  "rounds": [
    {"name": "round 1 (files)", "threshold": 0.5, "scores": {"p/a_test.go": 0.9}, "selected": ["p/a_test.go"]}
  ],
  "cost_usd": 0.0000378,
  "selected": {"p": ["TestA"]}
}
```

With `--verbose` the document gains a `judging` array holding each exchange
(`status`, `raw_response`, `question_keys`, `instructions`, `answers`, token
counts). When `--json` is on, `go test` output is diverted to **stderr**, so
stdout stays a parseable document.

## Development

```bash
go test ./...                        # unit tier
go test -tags=integration ./...      # integration tier (real git, real go test, httptest)
go test -tags=e2e ./...              # end-to-end tier (assembled binary)
golangci-lint run ./...              # strict NSX v2 config (.golangci.yml)
golangci-lint fmt ./...              # gofumpt + goimports + gci
```

### Pre-commit hook

`.githooks/pre-commit` runs formatting, tests and lint on staged Go changes.
Enable it once per clone:

```bash
git config core.hooksPath .githooks
```

It checks formatting (`golangci-lint fmt --diff`), runs `go test ./...`, and
runs `golangci-lint run ./...`; a failure blocks the commit. Bypass in an
emergency with `git commit --no-verify`.
