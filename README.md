# go-smart-test-runner

Uses AI to select the tests to run: it indexes the repository with [Grove](https://github.com/provasign/grove), computes the diff from
`merge-base(HEAD, <base>)` to the working tree, then asks a decision model
(SystemOne / OpenRouter `jev-latest`) one yes/no question per test file and,
for each file it approves, one per test function. Only the selected tests run,
via `go test -run`.

## Install

### Homebrew (macOS / Linux)

```bash
brew install nsxbet/tap/smart-test-runner
```

Published to the org tap [`NSXBet/homebrew-tap`](https://github.com/NSXBet/homebrew-tap)
(the same tap as `aihub`, `tasks`, `conduit-agent`). Homebrew-managed installs
update with `brew upgrade smart-test-runner` — the tool detects a Homebrew
install (Cellar/Caskroom path) and prints that command. Pre-releases are not
published to the tap.

### Install script (macOS / Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/NSXBet/go-smart-test-runner/main/install.sh | sh
```

Downloads the release binary for your OS and architecture (linux/darwin ×
amd64/arm64), verifies its SHA-256 against the release's `checksums.txt`, and
installs it. Honours `INSTALL_DIR` (default `/usr/local/bin`, else
`~/.local/bin`), `VERSION` (pin a tag), and `BASE_URL` (mirror). Windows users
should take the `.zip` from the [releases page](https://github.com/NSXBet/go-smart-test-runner/releases).

### `go install`

```bash
go install github.com/NSXBet/go-smart-test-runner/cmd/smart-test-runner@latest
```

The main package lives under `cmd/smart-test-runner/`, so the toolchain names
the binary `smart-test-runner` (a binary is named after the last element of
its import path — installing the module root would produce
`go-smart-test-runner`). `@latest` resolves to the highest release tag, and
the Go toolchain embeds that tag in the binary, so `smart-test-runner version`
reports the real version — no extra step needed.

Either way, `grove` must be on `PATH` and `OPENROUTER_API_KEY` set.

## Version

```bash
smart-test-runner version   # v1.2.3, or "dev" for a local build
smart-test-runner --version # same
```

The version comes from, in order: the value the release build injects
(`-ldflags -X .../internal/version.Version`), then the module version the Go
toolchain embeds (a `go install ...@<tag>` build), then `dev`.

## Staying up to date

On a normal run the tool checks (cached for 15 minutes) whether a newer release
exists and prints a one-line hint to stderr:

```
A new version of smart-test-runner is available: v1.2.0 (you have v1.1.0)
Update with:  smart-test-runner upgrade
         or:  go install github.com/NSXBet/go-smart-test-runner/cmd/smart-test-runner@v1.2.0
```

```bash
smart-test-runner check-update   # report only
smart-test-runner upgrade        # reinstall the latest release via go install
```

- The check never blocks a run: it is best-effort, time-boxed, and any failure
  is silent.
- `upgrade` picks the right mechanism for how the binary was installed:
  Homebrew installs (a Cellar/Caskroom path) run `brew upgrade`; everything else
  reinstalls via `go install`. The install script's `INSTALL_DIR` is honoured by
  re-running the script.
- Suppress it with `--no-update-check` or `SMART_TEST_RUNNER_NO_UPDATE_CHECK=1`.
- It is skipped automatically for `--json` (so stdout stays a clean document)
  and for `version`/`help`.
- `SMART_TEST_RUNNER_UPDATE_API` overrides the release API root (mirrors).
- `upgrade` validates the tag before shelling out to `go install`.

### Graph backend

Test selection is driven by the [Grove](https://github.com/provasign/grove) code
graph. On each run the tool runs `grove index .` (incremental — a warm run is a
no-op), then `grove impact <changed-file>` per changed file to learn which test
files transitively reach it. That closure is what the model is asked to judge.

Grove's native Go type analyzer is off by default (`--no-native`): it panics on
modules whose dependencies export Go 1.27 type data
(`export data version 4 is greater than maximum supported version 2`), which is
any repo on Go 1.27. The tree-sitter path still resolves cross-package calls.
Set `SMART_TEST_RUNNER_GROVE_NATIVE=1` to opt back in once that is fixed upstream.

### Configuration

The SystemOne (decisions) integration is fully configurable by environment —
nothing is hardcoded:

| Variable | Default | Purpose |
|---|---|---|
| `GOSMARTTESTRUNNER_SYSTEMONE_URL` | OpenRouter `https://openrouter.ai/api/alpha/decisions` | decisions endpoint |
| `GOSMARTTESTRUNNER_SYSTEMONE_MODEL` | `jev-latest` | decision model |
| `GOSMARTTESTRUNNER_SYSTEMONE_TOKEN` | `OPENROUTER_API_KEY`, else `AIHUB_TOKEN` against the AI Hub gateway | bearer token |

Precedence is **flag > env > default** for URL and model. A bare base URL (no
path) gets `/api/alpha/decisions` appended, so
`GOSMARTTESTRUNNER_SYSTEMONE_URL=https://ai-llm-gateway.fbr.land` works as-is.

The token has no flag — secrets should not land in shell history or process
args. Resolution order: `GOSMARTTESTRUNNER_SYSTEMONE_TOKEN`, then
`OPENROUTER_API_KEY`, then `AIHUB_TOKEN` (only when the endpoint is the AI Hub
gateway, so it is never sent to OpenRouter).

```bash
# defaults — nothing set, uses OpenRouter + OPENROUTER_API_KEY
smart-test-runner --base main

# point at a self-hosted gateway
export GOSMARTTESTRUNNER_SYSTEMONE_URL=https://ai-llm-gateway.fbr.land
export GOSMARTTESTRUNNER_SYSTEMONE_MODEL=jev-latest
export GOSMARTTESTRUNNER_SYSTEMONE_TOKEN=...
```

## Releases

Pushing a `v*` tag triggers `.github/workflows/release.yml`, which lints, tests,
and runs [GoReleaser](.goreleaser.yaml) to publish binaries for linux, darwin
and windows on amd64 and arm64, plus `checksums.txt`.

```bash
git tag v0.1.0 && git push origin v0.1.0
```

## Usage

```bash
smart-test-runner                       # diff vs origin/main
smart-test-runner --base main           # diff vs a local branch
smart-test-runner --dry-run             # print the selection, don't run
smart-test-runner --verbose             # stream the model exchange for auditing
smart-test-runner --json                # machine-readable result on stdout
smart-test-runner --json --verbose      # ... including the judging
smart-test-runner --all                 # skip selection: run go test ./...
smart-test-runner --all -- -race        # ... forwarding flags to go test
smart-test-runner -- -race -count=1     # everything after -- goes to go test
```

| Flag | Default | Meaning |
|---|---|---|
| `--repo` | `.` | repository path |
| `--base` | `origin/main` | base ref for `merge-base` |
| `--threshold` | `0.5` | `noul >= threshold` means run it |
| `--model` | `jev-latest` | decision model (env `GOSMARTTESTRUNNER_SYSTEMONE_MODEL`) |
| `--endpoint` | OpenRouter `alpha/decisions` | decisions endpoint (env `GOSMARTTESTRUNNER_SYSTEMONE_URL`) |
| `--dry-run` | `false` | print selection and `go test` commands, do not run |
| `--verbose` | `false` | print the full decisioning exchange to stderr |
| `--json` | `false` | emit the whole result as JSON on stdout |
| `--all` | `false` | run the whole suite (`go test ./...`) instead of selecting from the diff |
| `--no-update-check` | `false` | skip the check for a newer release |

Anything after `--` is forwarded verbatim to `go test`, so all `go test` flags
keep working (`-- -race -count=1 -v`; `-run` is managed by the tool).

### Reading the output

A selected run prints what it is about to do, then the outcome — one line per
package, and the captured `go test` output only when it says something the line
above does not:

```
smart-test-runner  dc54a1ab08a8 · 7 changed files
20 test files considered, 7 selected · 49 tests considered, 14 selected
running 14 tests in 1 package
decisions cost $0.0021

  ↷ e2e  no buildable Go files
  ✓ internal/cmd  6 tests  510ms
  ✗ pkg/alpha  1 test  990ms

✗ pkg/alpha
    --- FAIL: TestAdd (0.00s)
        alpha_test.go:5: boom

FAIL  1 of 3 packages failed
```

- `✓` passed, `✗` failed, `↷` skipped — a package whose files are all behind a
  build tag the run does not enable (`e2e/`, `integration/`). Skipping is not a
  failure.
- A bare pass prints only `ok pkg 0.1s`, which the package line already says, so
  it is not repeated. A forwarded `-v` (`=== RUN`) or a failure adds real detail
  and is printed indented under its package.
- Colour is used only on a terminal; a pipe, a redirect or `NO_COLOR` renders
  plain text.

`--dry-run` reports the exact commands that would run instead of a pass/fail
claim, since nothing executed.

### `--all` — use it as `go test`

`--all` skips the diff, the code graph and the model, and forwards to `go test`
from the repository root. It needs no `OPENROUTER_API_KEY` and no Grove index,
so one command covers both cases:

```bash
smart-test-runner --all                     # every test (go test ./...)
smart-test-runner                           # only the tests the change can affect

smart-test-runner --all -- ./pkg/mine       # just one package
smart-test-runner --all -- -run TestLogin   # one test, across all packages
smart-test-runner --all -- -race -count=1   # with flags
```

Everything after `--` is forwarded verbatim; `./...` is only appended when you
have not named a target yourself, so an explicit package or `-run` filter is
never overridden. `--dry-run` and `--json` compose (the document carries
`"all": true`).

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
