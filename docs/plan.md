# graphify-test-runner — plan

## Context
Build a Go CLI in `/Users/nsx001164/src/go-graphify-test-runner` (empty git repo, unborn `main`, no commits). It:
1. runs `graphify update .` so `graphify-out/graph.json` is current;
2. computes the diff from `merge-base(HEAD, <base>)` to the working tree;
3. round 1: asks SystemOne (OpenRouter, model `jev-latest`) one yes/no question **per Go test file**: "should this file run?";
4. round 2: for every YES file, one yes/no question **per top-level test function**;
5. runs the selected tests with `go test -run`.
Go-only (user decision). Only the relevant slice of the graph is sent, because the endpoint caps a request at ~33k input tokens.

## Verified facts (probed this session)
- Endpoint: `POST https://openrouter.ai/api/alpha/decisions`, header `Authorization: Bearer $OPENROUTER_API_KEY`, `Content-Type: application/json`.
  Request: `{"model":"jev-latest","state":"<text>","questions":{"<key>":{"type":"noul","instructions":"<text>"}}}`.
  Response: `{"id":..,"model":"typesafe/jev-1.13-20260917","answers":{"<key>":{"type":"noul","noul":0.64}},"usage":{"input_tokens":..,"output_tokens":..,"cost":0.0000119},"provider":"TypeSafe"}`.
  Keys are echoed back verbatim, including `/`, `::` and spaces.
- Limit: ~33k input tokens per request (state + all questions). Above that: HTTP 400, body `{"error":{"message":"HTTP 400: {\"detail\":{\"error_type\":\"max_tokens_exceeded\"}}","code":400}}`. Go source is ~3 chars/token. 300 short questions in one request is fine (~0.4 s).
- `graphify update <path>` (pipx `graphifyy`): AST-only re-extraction, no LLM. Writes `<path>/graphify-out/graph.json`. Exits 1 if the new graph has fewer nodes than the old one; `--force` overrides. When the path is relative, `source_file` values are relative to the process cwd.
- `graph.json` schema: `nodes[]` `{id,label,file_type,source_file,source_location:"L<n>"}`. Functions are labelled `Name()`, methods `.Name()`, and each file has a node labelled with its basename at `L1`. `links[]` `{relation,source,target,confidence}` with relations `contains|calls|method`. For `calls`, `source` is the caller and `target` the callee (every test→code edge in `nsf` has the Test func as source).

## Approach
Layout (NSXBet Go convention):
- root `main.go` — thin `package main`, calls `cmd.Execute()` so `go install github.com/NSXBet/go-graphify-test-runner` works.
- `cmd/root.go` — Cobra command (modeled on crush's `internal/cmd/root.go`).
- `internal/repo`, `internal/graph`, `internal/gotest`, `internal/decide` — all logic.
Dependency: `github.com/spf13/cobra`.

### 1. Scaffold
- `go mod init github.com/NSXBet/go-graphify-test-runner`.
- `.gitignore`: `graphify-out/`, `/graphify-test-runner`.
- `cmd/root.go` flags (Cobra/pflag; double-dash required, e.g. `--base`):
  - `--repo` string, default `"."`
  - `--base` string, default `"origin/main"`
  - `--threshold` float64, default `0.5` (noul ≥ threshold ⇒ yes)
  - `--model` string, default `"jev-latest"`
  - `--endpoint` string, default `"https://openrouter.ai/api/alpha/decisions"`
  - `--dry-run` bool: print the selection and the `go test` commands, don't run them
  - `flag.Args()` (after `--`) go to `go test` as extra args (`graphify-test-runner --base main -- -race -count=1`)
- API key from `OPENROUTER_API_KEY`. If empty, exit 2 with `OPENROUTER_API_KEY not set`.
- All progress output goes to stderr via `fmt.Fprintf(os.Stderr, ...)`; the selection report goes to stdout. Exit codes: 0 = ok or nothing to run, 1 = go test failed, 2 = setup/API error.

### 2. internal/repo — repo root and diff
- `Root(dir) (string, error)`: `git -C dir rev-parse --show-toplevel`. Every later command runs with `cmd.Dir = root`.
- `MergeBase(root, base)`: `git merge-base HEAD <base>`. On failure, exit 2 with `merge-base HEAD <base> failed: <stderr>`.
- `DiffText(root, mb)`: `git diff <mb>` (working tree vs merge-base, tracked files).
- `ChangedLines(root, mb) (map[string][][2]int, error)`: runs `git diff -U0 --no-color <mb>` and parses it.
  - `+++ b/<path>` sets the current file; `+++ /dev/null` (a deletion) records the path with nil ranges.
  - Each `@@ -a[,b] +c[,d] @@` adds range `[c, c+max(d,1)-1]`; `d` defaults to 1 when omitted.
  - Only `.go` paths are kept.
…
- Empty diff: print `no changes vs merge-base <short sha>` and exit 0.

### 3. internal/graph — update and graph evidence
- `Update(root)`: `exec.LookPath("graphify")`; if missing, exit 2 with `graphify not found in PATH`. Then run `graphify update .` with `Dir=root` and stdout/stderr piped to our stderr. Non-zero exit: exit 2 with `graphify update failed (if it refused to shrink after deleting code, run: graphify update . --force)`. Do not pass `--force` automatically; the user's graph is theirs.
- `Load(root)`: decode `root/graphify-out/graph.json` into
  `type graph struct{ Nodes []node `json:"nodes"`; Links []link `json:"links"` }`,
  `node{ID, Label, SourceFile, SourceLocation string}`,
  `link{Relation, Source, Target string}`.
  Build:
  - `byID map[string]*node`
  - `byFile map[string][]*node`, sorted by the parsed line number (`strconv.Atoi(strings.TrimPrefix(loc,"L"))`; unparsable ⇒ skip the node)
  - `callees map[string][]string` from `Relation=="calls"` links only
- `changedSymbols(g, changed map[string][][2]int) map[string]bool` (set of node IDs):
  - For each file, only its nodes whose label is not the file basename.
  - For each range: mark every node with line in `[start,end]`, and also the enclosing node (the node with the greatest line ≤ start).
  - Files with nil ranges (deleted) contribute nothing, since the refreshed graph no longer has their nodes; the diff text in the state still covers them.
- `evidence(g, callerIDs []string, changedIDs map[string]bool) (direct []string, indirect []string)`:
  - direct = labels of callees of any caller that are in `changedIDs`;
  - indirect = `"<changed label> via <intermediate label>"` for depth-2 paths caller→X→changed where X is not itself changed.
  - Deduplicate, sort, cap each list at 20 entries (append `"…"` when capped).

### 4. internal/gotest — test discovery and execution
- `ListFiles(root)`: `git ls-files -co --exclude-standard -- '*_test.go'`. Drop paths containing `/vendor/`, `vendor/` at the start, or `/testdata/`. If the result is empty: print `no Go test files` and exit 0.
- `Funcs(root, path) ([]Func, error)`: `go/parser.ParseFile` with `parser.SkipObjectResolution`. Keep `*ast.FuncDecl` with `Recv == nil` whose name matches Go's test rule: `name == "Test"` or `strings.HasPrefix(name,"Test")` with the next rune not lowercase (`!unicode.IsLower`). Exclude `TestMain`.
  `testFunc{Name string; Src string}`, where `Src` is the func source sliced by `fset.Position(decl.Pos()/End()).Offset`, truncated to 1000 bytes. On a parse error, return it and exit 2 naming the file.
- Graph mapping: a test func maps to the node with `SourceFile==path && Label==Name+"()"`. No node ⇒ evidence lists are empty (the question still gets asked).
- `Run(root, selected map[string][]string /*pkg dir → test names*/, extra []string, dryRun bool) int`:
  - Sort dirs. For each dir, walk up from `root/dir` to find the nearest `go.mod`, which gives the module root `m` (fall back to `root`).
  - Run `go test <extra...> -run '^(A|B|C)$' ./<rel(m, root/dir)>` with `Dir=m`, stdout/stderr passed through. Test names are Go identifiers, so no regex escaping is needed.
  - Return 1 if any run failed, else 0.
  - `--dry-run`: print each command line instead of running it, return 0.

### 5. internal/decide — SystemOne client with batching
- Types: `question{Type string `json:"type"`; Instructions string `json:"instructions"`}`; `request{Model, State string; Questions map[string]question}`; `response{Answers map[string]struct{Noul float64 `json:"noul"`} `json:"answers"`; Usage struct{Cost float64 `json:"cost"`} `json:"usage"`}`.
- `type Client struct{ http *http.Client; endpoint, key, model string; cost float64 }`. HTTP timeout 60 s.
- `(c *Client) Decide(ctx, state string, qs []Question) (map[string]float64, error)`, where `Question{Key, Instructions string}` keeps the order deterministic:
  - Batching: greedily pack questions into a batch while `len(state) + Σ(len(key)+len(instr)+40) ≤ 72000` chars (≈24k tokens, below the ~33k limit). A single question always fits because instructions are capped at 1500 chars and state at 30000.
  - Batches are sent **sequentially** (`// ponytail: sequential batches; add a bounded worker pool if wall time matters`).
  - On HTTP 400 whose body contains `max_tokens_exceeded`: if the batch has more than 1 question, split it in half and recurse on each half; with 1 question, return the error.
  - Any other non-2xx: return `fmt.Errorf("decisions HTTP %d: %s", code, body)`. The caller exits 2. Never fall back to "run nothing".
  - Missing key in `answers`: treat as `1.0` (run it — false negatives are worse than extra runs) and print `warning: no answer for <key>, running it` to stderr.
  - Add `usage.cost` to `c.cost` for each response.

### 6. cmd/root.go + internal packages — orchestration and prompts
Order: Cobra flags → `repo.Root` → `graph.Update` → `graph.Load` → `repo.MergeBase` → `repo.ChangedLines` + `repo.DiffText` → `graph.ChangedSymbols` → `gotest.ListFiles` → parse all test files → round 1 → round 2 → report → `gotest.Run`.

**State** (same for both rounds), built by `buildState`, total capped at 30000 chars:
```
Change under review: diff from merge-base <sha12> to working tree.
Changed files:
<one path per line>
Changed symbols (from code graph):
<path>: <label>, <label>, ...
Diff:
<git diff text, truncated so the whole state is ≤30000 chars; if truncated, append "\n[diff truncated]">
```

**Round 1**, one question per test file. Key = repo-relative path. Callers = all graph nodes with `SourceFile == path` (includes helpers). Instructions (truncated to 1500 chars):
```
Should Go test file `<path>` be run to validate this change? Answer yes if any test in it likely exercises changed code or behavior.
Package dir: <dir> (same directory as a changed file: yes|no).
Tests: <comma-separated names, max 40, then "…">.
Calls changed symbols directly: <direct or "none">.
Calls changed symbols indirectly: <indirect or "none">.
```
Selected files: `noul >= threshold`. If none: print report, `no test files selected`, exit 0.

**Round 2**, one question per test func across every selected file. Key = `<path>::<TestName>`. Callers = the test func's node ID. Instructions (truncated to 1500 chars):
```
Should test `<TestName>` in `<path>` be run to validate this change? Answer yes if it likely exercises changed code or behavior.
Calls changed symbols directly: <direct or "none">.
Calls changed symbols indirectly: <indirect or "none">.
Source:
<Src>
```
A selected file with zero test funcs (e.g. only benchmarks or examples) contributes nothing.

**Report** (stdout), sorted by key:
```
round 1 (files): <n> asked, <m> selected
  0.87 YES pkg/a/a_test.go
  0.12 no  pkg/b/b_test.go
round 2 (tests): <n> asked, <m> selected
  0.91 YES pkg/a/a_test.go::TestParse
cost: $<c.cost %.6f>
```
Then group the selected tests by `filepath.Dir(path)` → `gotest.Run` → `os.Exit(code)`.

## Critical files & anchors
- `internal/decide/decide.go`: `Decide` batching and split-on-`max_tokens_exceeded`. Limits: 72000 chars/request, 30000 state, 1500 per instruction.
- `internal/graph/graph.go`: `ChangedSymbols` enclosing-node rule; `callees` built only from `calls` links, in source→target direction.
- `internal/gotest/gotest.go`: test-name rule and nearest-`go.mod` module resolution for `go test`.

## Verification
Prereqs: `graphify` on PATH, `OPENROUTER_API_KEY` set, Go 1.27.

1. `go vet ./... && go test ./...` in the repo root. Permanent tests (stdlib `testing`, table-driven):
   - `internal/repo/repo_test.go`: parse a literal `-U0` diff containing an added hunk `+10,3`, a pure deletion `+7,0`, an omitted count `+5`, a deleted file `+++ /dev/null`, and a non-Go file ⇒ ranges `[10,12]`, `[7,7]`, `[5,5]`, nil for the deleted path, non-Go dropped.
   - `internal/graph/graph_test.go`: an in-memory graph with nodes at L1 (file node), L10 `A()`, L30 `B()`; range `[12,14]` ⇒ only `A()` changed, file node never. Test node T calls `X()`, which calls `A()` ⇒ direct `[]`, indirect `["A() via X()"]`.
   - `internal/decide/decide_test.go`: `httptest.Server` returns the exact 400 `max_tokens_exceeded` body when a request has >2 questions, and otherwise answers `noul:0.7` for every key except `"k3"` ⇒ 5 questions all answered; `k3` = 1.0; server saw no request with >2 questions. A second case: a server 500 ⇒ error returned.
   - `internal/gotest/gotest_test.go`: a temp `x_test.go` with `TestA`, `Test_b`, `Testfoo`, `TestMain`, `Test`, and method `(s) TestM` ⇒ `[Test, TestA, Test_b]`.
2. Fixture smoke (new behavior end to end, real API):
   ```
   D=$(mktemp -d) && cd $D && git init -q && go mod init example.com/fx
   # pkg/alpha/alpha.go: func Add(a,b int) int { return a+b }; alpha_test.go: TestAdd, TestUnrelatedAlpha (asserts 1==1)
   # pkg/beta/beta.go: func Greet() string { return "hi" }; beta_test.go: TestGreet
   git add -A && git commit -qm base && git branch base
   # edit Add to return a+b+0 (behavioral touch) and commit on HEAD
   go-graphify-test-runner --base base --dry-run
   ```
   Expect `graphify-out/graph.json` to exist; round 1 marks `pkg/alpha/alpha_test.go` YES and `pkg/beta/beta_test.go` no; round 2 asks only `pkg/alpha/...` tests with `TestAdd` YES; the dry-run prints `go test -run '^(TestAdd…)$' ./pkg/alpha`. Then run without `-dry-run` and expect exit 0 with `ok example.com/fx/pkg/alpha`. The model is probabilistic: if `TestUnrelatedAlpha` also comes back YES, that's acceptable; `beta` coming back YES or `TestAdd` coming back no means the prompt/evidence is wrong and must be investigated.
3. Scale smoke (batching against the token limit): `git clone -q /Users/nsx001164/src/nsf /tmp/nsf-smoke`, edit one function body in a `pkg/` file, commit, then `go-graphify-test-runner --repo /tmp/nsf-smoke --base HEAD~1 --dry-run`. Expect 270 round-1 questions answered, no HTTP 400, and a selection concentrated in the edited package. Delete `/tmp/nsf-smoke` and the fixture dir afterwards.

## Assumptions & contingencies
- Provider: OpenRouter `alpha/decisions` with `jev-latest`. If it 404s or the model id changes, point `--endpoint` at AI Hub `https://ai-llm-gateway.fbr.land/api/alpha/decisions` (same request/response shape; verified with `AIHUB_TOKEN`). Only the key env var differs; for that case, read `AIHUB_TOKEN` when the endpoint host is `ai-llm-gateway.fbr.land`.
- Module path `github.com/NSXBet/go-graphify-test-runner` (no remote exists yet). Change it if the repo lands elsewhere.
- Diff covers tracked files only (`git diff <mb>`); untracked non-test source files are ignored. Untracked `_test.go` files are still discovered as candidates.
- Top-level `TestXxx` only; no subtest, benchmark, fuzz or example selection.
- Don't commit or push; leave the changes in the working tree.
