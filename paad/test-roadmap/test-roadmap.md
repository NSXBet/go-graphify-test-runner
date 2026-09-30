# Test roadmap — graphify-test-runner

## Decisions

- **Framework/runner**: Go standard `testing` (stdlib), colocated `_test.go` files. Detected from `go.mod`; no third-party test deps in `go.sum`.
- **Suite strategy**: unit-first, then integration, one e2e (developer choice).
- **Phase ordering**: unit phases 1–6 (pure logic, no git/network), integration phases 7–9 (real git, httptest), e2e phase 10 (full CLI against a fixture repo with a stub decisions server).
- **Weak-test rewrite**: yes — fix `internal/decide/decide_test.go` `TestDecideServerError` (assertion-free test; only checks an error is non-nil, not that it is the right error).
- **Test organization (detected, not chosen)**: Go places `_test.go` beside the code under test, so a `unit/` directory is impossible. Tiers are separated by **Go build tags** — the ecosystem's own selector — not by directory.
- **Tiers (run | coverage)**:
  - unit:        `go test ./...`                    | `go test -coverprofile=unit.out ./... && go tool cover -func=unit.out`
  - integration: `go test -tags=integration ./...`   | `go test -tags=integration -coverprofile=int.out ./... && go tool cover -func=int.out`
  - e2e:         `go test -tags=e2e ./...`           | `go test -tags=e2e -coverprofile=e2e.out ./... && go tool cover -func=e2e.out`
- **Known constraint**: the CLI's `--endpoint` is injectable, so the e2e phase stands in for OpenRouter with a local HTTP server; no live API key is needed to run the suite.

## Phase 1: Diff parser edge cases

Tier:     unit
Catches:  a `-U0` hunk whose count is omitted (`+5`) parsed as a 5-line range
          instead of 1; a deletion record dropped so a deleted file is never
          surfaced; a non-Go path leaking into the changed-file set; a diff
          with no trailing newline truncating the last hunk.
Produces: internal/repo/repo_test.go
Branch:   test-roadmap
Landed:   2026-09-30 81981ab (off-by-one a boundary)

## Phase 2: Changed-file set and diff wrappers

Tier:     unit
Catches:  `ChangedFiles` returning paths in nondeterministic map order (so the
          state text churns run to run); a deleted file (`nil` ranges) missing
          from the reported set.
Produces: internal/repo/repo_test.go
Branch:   test-roadmap
Landed:   2026-09-30 b47008a (negate a condition)

## Phase 3: Graph lookups and evidence cap

Tier:     unit
Catches:  `Evidence` returning more than the cap (a changed file with many
          callees blowing the request past the token limit); an indirect path
          reported as direct; `NodeIDFor` failing to map a test name to its
          node so evidence silently empties; `nodeLine` treating an unparsable
          location as line 0 instead of skipping the node.
Produces: internal/graph/graph_test.go
Branch:   test-roadmap
Landed:   2026-09-30 915b9a5 (drop a state transition)

## Phase 4: Test discovery and module resolution

Tier:     unit
Catches:  `ListFiles` admitting `vendor/` or `testdata/` test files (so the
          tool asks about third-party tests); `IsTestName` accepting `Testfoo`
          (lowercase after `Test`) or rejecting the bare `Test`; `ModuleRoot`
          failing to stop at the nearest `go.mod` in a nested module.
Produces: internal/gotest/gotest_test.go
Branch:   test-roadmap
Landed:   2026-09-30 27a0770 (negate a condition)

## Phase 5: Decisions client batching and weak-test fix

Tier:     unit
Catches:  the batching loop emitting a batch that exceeds the char budget
          (so a large repo 400s on every run); a missing answer defaulting to
          "do not run" instead of run; and — the weak-test fix — a non-2xx
          response returning *any* error, so the assertion checks the specific
          status/body, not merely that an error is non-nil.
Produces: internal/decide/decide_test.go
Branch:   test-roadmap
Landed:   2026-09-30 3c8f4df (alter a constant)

## Phase 6: CLI rendering and report

Tier:     unit
Catches:  `buildState` not truncating the diff (so a large diff blows the
          request token limit); `promptR1`/`promptR2` dropping the evidence
          lines; `truncate` cutting a multi-byte rune in half; `reportRound`
          miscounting the selected total.
Produces: internal/cmd/root_test.go
Branch:   test-roadmap
Landed:   2026-09-30 955d603 (negate a condition)

## Phase 7: Real git diff integration

Tier:     integration
Catches:  `ChangedLines` on a real repository mis-parsing an added file
          (`+++ b/new.go` with no `---` content), a renamed file, or a deleted
          Go file; `MergeBase` not surfacing the git stderr on an unknown base.
Produces: internal/repo/repo_integration_test.go (build tag `//go:build integration`)
Branch:   test-roadmap
Landed:   2026-09-30 918122e (alter a constant)

## Phase 8: Test execution integration

Tier:     integration
Catches:  `Run` building the wrong `go test -run` pattern (so a selected test
          is not actually executed, or a sibling test is run by accident);
          `ModuleRoot` giving a package pattern that `go test` cannot resolve;
          the grouped-by-directory loop skipping a directory.
Produces: internal/gotest/gotest_integration_test.go (build tag `//go:build integration`)
Branch:   test-roadmap
Landed:   2026-09-30 8cc5752 (alter a constant)

## Phase 9: Multi-batch decisions integration

Tier:     integration
Catches:  `Decide` sending more than one batch when a large state forces a
          split, and merging the answers losing a key across batches; a batch
          that 400s twice never splitting further.
Produces: internal/decide/decide_integration_test.go (build tag `//go:build integration`)
Branch:   test-roadmap
Landed:   2026-09-30 f121262 (drop a state transition)

## Phase 10: End-to-end CLI against a fixture repo

Tier:     e2e
Catches:  the assembled binary running the wrong tests for a change, or exiting
          0 without running anything; the `--dry-run` selection diverging from
          what the real run selects.
Produces: cmd/e2e_test.go (build tag `//go:build e2e`)
Branch:   test-roadmap
Landed:   2026-09-30 0def207 (alter a constant)
