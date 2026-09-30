# Test suite analysis — graphify-test-runner

Full grading verdicts from Stage 2 (Grade) and the test-double & fixture ledger.
This file is the unbounded sink; main context keeps counts and the top verdicts only.

## Grading verdicts

Graded 4 test files (6 tests) against the weak-test catalog.

### Weak tests

**1. `internal/decide/decide_test.go:61` — `TestDecideServerError`**
- Pattern: **assertion-free test**.
- Why it fails to catch regressions: the test asserts only that `Decide` returns
  *some* error (`err == nil` fails the test), never that it is the *right* error.
  A regression that returned an unrelated error — or wrapped a nil-pointer
  panic into an error — would still pass. In plain words: the test proves the
  call failed, not that it failed for the reason it should.
- Suggested replacement: assert the error names the actual failure, e.g.
  `strings.Contains(err.Error(), "decisions HTTP 500")` for the 500 case, and
  add a 400-without-`max_tokens_exceeded` case asserting the body is surfaced.

### Clean tests

- `internal/repo/repo_test.go:8` `TestParseUnifiedDiff` — asserts exact range
  values for added / pure-deletion / omitted-count hunks, a deleted file, and a
  dropped non-Go path. Real value assertions; keep.
- `internal/graph/graph_test.go:32,43` — assert membership and the exact
  indirect-path string. Real value assertions; keep.
- `internal/gotest/gotest_test.go:11` — asserts the exact accepted name set
  (`Test`, `TestA`, `Test_b`), excluding `Testfoo`/`TestMain`/methods. Keep.
- `internal/decide/decide_test.go:11` `TestDecideSplitOnTokenLimit` — asserts
  answer count, the missing-key default, that the server was forced to reject a
  batch, the max accepted batch size, and accumulated cost. Keep.

## Test-double & fixture ledger

Classes: `boundary` — a real external edge (network, clock, randomness); it stays.
`scaffold` — a stand-in that exists only because the code is hard to test as
written; test debt, retired by a named refactor. `data` — constructed test data
that is permanent and correct.

| Double / fixture | Where | Class | Reason |
|---|---|---|---|
| `httptest.Server` (400/500 responder) | decide_test.go | `boundary` | stands in for the real OpenRouter HTTP edge |
| `httptest.Server` (per-key answerer) | decide_test.go | `boundary` | same edge; needed to exercise batching without a live key |
| in-memory `Graph` literal (`testGraph`) | graph_test.go | `data` | constructed graph data, permanent and correct |
| `t.TempDir` + written `x_test.go` | gotest_test.go | `data` | real filesystem fixture; the correct way to test parsing |
| planned fixture git repo | Phase 7/10 | `data` | a throwaway repo built with real `git`; not a stand-in for behavior |

No `scaffold` entries — nothing in this suite exists to paper over untestable code.
