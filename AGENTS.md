# Working on dbmap

Instructions for a coding agent changing this repository. `README.md` says what the
tool does; `docs/DESIGN.md` says why it is built this way, including the layer tree
and the reasoning behind every constant. Read both before writing code, and do not
re-derive a constant that already has a stated justification.

## Stack

Go, two modules: `github.com/branow/dbmap` (root) and `github.com/branow/dbmap/llm`
(nested, importable on its own). Cobra for the CLI.

Dependencies are **pinned** in both `go.mod` files. Never run a bare `go get`, never
upgrade a pin as a side effect. `go mod tidy` is fine once your code has imports. If
you genuinely need a new dependency, stop and ask rather than adding one.

Run both module test suites; the nested one does not run from the root:

```sh
go build ./... && go vet ./... && go test ./...
cd llm && go test ./...
gofmt -l .        # must print nothing
```

## Non-negotiables

- **No deployment-specific identifiers.** No real hostnames, realms, database names,
  schema or object names, employer or product references — not in code, docs, tests,
  fixtures, or the prompt strings the tool sends to a model. Use `example.internal`,
  `EXAMPLE.LOCAL`, `AppCore`. Every real value is user data that lives in the user's
  config or keychain at run time, never in the tree.
- **Secrets never enter the config file**, a log line, an error message, or a test
  fixture. A connection record holds structured non-secret fields; the secret is
  fetched at connect time and the connection string is assembled in memory. Do not
  write a credential-shaped literal into a test — build the value instead.
- **Every query this tool sends is a read.** Queries are constants in the engine
  packages, and a test walks all of them and fails if one is not. Add a query, and
  that test covers it automatically; do not add a runtime check that re-audits our
  own SQL.
- **Every statement carries its engine's resource guard**, applied centrally in the
  query path rather than at call sites. A guard applied per call site is a guard the
  next call site forgets.
- **No emoji.** Not in code, comments, docs, or commit messages.

## Code

- Errors are structured values, never formatted strings. Put context on a field, not
  into a message. There is exactly one place that turns an error into a process exit
  status: `cmdutil.ExitCode`, called from `main.go`.
- Packages in this repo trust each other. No defensive re-validation between internal
  packages — validate external input once, at the boundary, then trust it.
- Rule lists are **data walked by an engine**, never an if/else ladder. `StopConditions`,
  `FetchReasons`, `Catalogs`, `Kinds`, `Prompts`, `signatures` are all tables. A new
  case should be a new row.
- Dependencies flow bottom-up. Siblings do not import each other, and the domain never
  imports the shell. `internal/catalog` is the shared vocabulary and imports nothing.
- Doc-comment every exported symbol: one line on what it is, plus *why* where the code
  would otherwise look arbitrary. Comments explain why, never restate the code. No
  commented-out code, no committed TODOs, no multi-paragraph essays.
- `gofmt` clean, imports ordered, lines at most 100 columns. Prefer short,
  descriptive-within-context names.

## Tests

- Tests live beside the code as `*_test.go`. Whoever writes the code writes its tests.
- Table-driven. **No network, no real database, no real keychain, no real model call**
  in any test. Every external dependency sits behind an interface with an in-memory
  fake. The one test that touches a real keychain is gated behind both a build tag and
  an environment variable so it never runs by accident.
- Test the decision, not the implementation. Several tests exist specifically to stop a
  past mistake being reintroduced — the structured-output field being renamed, a
  sampled table's value domain being withheld, a catalog boolean being read in only one
  spelling. If one of those fails, the fix is almost certainly in the code, not the test.

## Commits

The repo builds and passes tests at **every** commit. One commit is one meaningful step,
landed with the tests that prove it. Never "commit now, test later".

Conventional Commits: `feat:`, `fix:`, `test:`, `refactor:`, `docs:`, `chore:`. Lowercase
first word, no body, no attribution or co-author line.

## Scope

If a change you need falls outside what you were asked to do — a dependency, a `go.mod`
edit, a public API change in `llm`, a rename that ripples — stop and report it rather
than making it. When several agents work at once, each edits only its assigned paths and
reports anything it needs elsewhere.

## Never commit

Any `config.yml`, any `credentials.yml`, any `*.local.*`, the `.dbmap/` output tree, build
output, or a file containing a real credential. `.gitignore` is allowlist-style
(deny-by-default) — do not add a rule that tracks these.
