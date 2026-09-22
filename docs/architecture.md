# Architecture — layer tree, ownership, milestones

Design decisions live in `docs/blueprint.md`; measured data lives in `DESIGN.md`. This file
answers two questions only: what depends on what, and who owns which files.

## Abstraction-layer tree

Dependencies flow bottom -> top. A parent depends on its children; **siblings never import
each other**. The domain never imports the shell.

```
main.go
└── cmd/                     cobra tree; flag parsing only, zero business logic
    └── internal/cmdutil     Factory: the dependency bundle + ExitCode translation
        ├── internal/config        named connections, named backends, profiles, precedence
        ├── internal/credentials   keychain Store + env path + opt-in plaintext
        ├── internal/iostreams     in/out/err, tty, color, prompts
        ├── internal/output        table | json writers
        │
        ├── internal/index         the build pipeline — composes the domain below
        │   ├── internal/connect       connection record -> DSN -> pooled *sql.DB
        │   │   └── internal/engine        Engine interface + safety contract
        │   │       ├── internal/engine/sqlserver
        │   │       └── internal/engine/postgres
        │   ├── internal/cache         resumable on-disk fetch cache
        │   ├── internal/redact        secret + PII rules as data
        │   ├── internal/plan          two-tier staleness planner
        │   │   └── internal/fingerprint   sampleDepth + per-kind content hashes
        │   ├── internal/describe      prompts per kind, cost batching, name matching
        │   │   └── llm (module)           provider seam — see docs/blueprint-llm.md
        │   └── internal/render        the TSV tree + reading staleness back out of it
        │
        └── internal/catalog      the shared vocabulary: Object, Structure, Column,
                                  Index, Param, Sample, Kind. imports nothing.
```

`internal/catalog` is the leaf every domain package speaks; it holds types and pure helpers
only, and must never import an engine, a driver, or the shell.

## File ownership

One owner per path. **Do not edit a file outside your paths** — report the need instead.

| Owner | Owns |
|---|---|
| `shell` | `main.go`, `cmd/**`, `internal/cmdutil/**`, `internal/config/**`, `internal/credentials/**`, `internal/iostreams/**`, `internal/output/**` |
| `core` | `internal/catalog/**`, `internal/fingerprint/**`, `internal/plan/**`, `internal/render/**` |
| `llm` | `llm/**` (its own Go module) |
| `engine` | `internal/engine/**`, `internal/connect/**`, `internal/redact/**`, `internal/cache/**` |
| `pipeline` | `internal/index/**`, `internal/describe/**`, `cmd/index.go`, `cmd/doctor.go` |
| lead only | `go.mod`, `go.sum`, `llm/go.mod`, `llm/go.sum`, `CLAUDE.md`, `docs/**`, `.gitignore`, `DESIGN.md`, `reference/**` |

`reference/js/**` is **read-only** for everyone. It is the specification being ported, not
code to modify.

## Milestones

Each milestone is green at its end: builds, `go vet` clean, `gofmt` clean, tests pass.

**M1 — shell + pure core + llm module.** No database, no network, no model call. Owners
`shell`, `core`, `llm` work in parallel; their paths are disjoint and nothing in M1 crosses
between them.

- `shell`: S1-S6. Root command and persistent flags; `Factory`; `config.yml` with the two
  named namespaces and `flag > env > file > default` precedence; keychain `Store` with an
  in-memory fake and the no-silent-plaintext-fallback rule; iostreams; table/json output;
  the typed error vocabulary and `ExitCode`. Commands: `connection`, `backend`, `profile`,
  `config`, `completion`, `version`. Not `index` or `doctor` — those are M3.
- `core`: R1, R6, R7, R11. Ports `reference/js/fingerprint.js`, `plan.js`, `write.js` and
  their test files, which transfer almost literally.
- `llm`: the whole `llm` module per `docs/blueprint-llm.md` — `Client` interface, three
  providers, the retry/concurrency/cache/usage middleware, tested against a fake transport.

**M2 — engine.** R2-R5, R9, R12. Starts with the Kerberos decider (`kinit` then `klist`: a
`krbtgt/B@A` referral ticket means cross-realm, which rules out the pure-Go driver and
selects ODBC — see blueprint G1a/G1b). Then the `Engine` interface, both implementations,
the read-only gate and per-engine guard, redaction, the resumable cache.

**M3 — pipeline.** R8, R10, R13, R14. Sampling, the describe stage wired to the `llm`
module, `cmd/index`, `cmd/doctor`. Composition only — every part already exists and is
tested by here.

**M4 — the full run.** The thing that has never happened: a complete single-database index,
end to end. Expect it to find real bugs; empty tables are the canary for prompt bugs.

## Cross-cutting contracts

Fixed in M1 because later milestones build against them:

- **Errors**: every package exports typed errors; `cmdutil.ExitCode(err)` is the single
  translation point, called only from `main.go`. Exit table lives in `docs/blueprint.md` S6.
- **Config precedence** is implemented as methods on `config.Config`, never re-derived at a
  call site.
- **Secrets** are reached only through `credentials.Store`. No other package touches a
  keychain or reads a password from anywhere.
- **Logging** happens at boundaries — commands and the engine's query path — never inside
  pure domain logic.
