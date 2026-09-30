# go-graphify-test-runner

Uses AI to select the tests to run: it refreshes the local [graphify](https://pypi.org/project/graphifyy/) code graph, computes the diff from
`merge-base(HEAD, <base>)` to the working tree, then asks a decision model
(SystemOne / OpenRouter `jev-latest`) one yes/no question per test file and,
for each file it approves, one per test function. Only the selected tests run,
via `go test -run`.

## Install

```bash
go install github.com/NSXBet/go-graphify-test-runner@latest
```

Requires `graphify` on `PATH` and `OPENROUTER_API_KEY` in the environment.

## Usage

```bash
graphify-test-runner                       # diff vs origin/main
graphify-test-runner --base main           # diff vs a local branch
graphify-test-runner --dry-run             # print the selection, don't run
graphify-test-runner --verbose             # stream the model exchange for auditing
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

Anything after `--` is forwarded verbatim to `go test`, so all `go test` flags
keep working (`-- -race -count=1 -v`, `-- -run` is managed by the tool).

### `--verbose` — auditing the decision

`--verbose` writes the whole exchange with the decision model to **stderr**,
so the selection is auditable: the shared state text, every question and its
instructions, each HTTP `POST`, the raw JSON response, and the parsed `noul`
per question. Normal progress and the final report stay on **stdout**.

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
