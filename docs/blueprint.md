# dbmap — database index for agents

CLI reads a database catalog -> writes a compact TSV index an agent reads in one pass.

pain: every session touching a DB starts from zero — list tables, describe columns, guess
the join, three more queries to learn a status column holds six values. repeated in full
next session against a schema that did not change.

NOT a query runner, NOT a schema-diff or migration tool, NOT an ORM, NOT a data catalog UI.
NOT ever pointed at production (a connection marked `production: true` refuses to resolve).
NOT a foreign-key graph (2 declared FKs across 3 databases measured; joins are convention).
NOT shipping the JS — `reference/js/` is a proven engine to port from, not to ship.

go 1.25, single binary, cobra. phase 1 engines: sqlserver, postgres. phase 1 describe
backends: anthropic api, openai-compatible, headless claude code.

the written TSV tree = SOURCE OF TRUTH for staleness. no sidecar state file — a parallel
file drifts, and `modified` earns its place for a reader anyway.

`DESIGN.md` = the measured reference data behind every constant here. not restated.

**no deployment-specific identifiers anywhere in this repo** — no real hostnames, realms,
database names, schema or object names, or employer references, in docs, code, tests or
fixtures. every such value is user data: it lives in the user's config or keychain (S3/S4),
never in the tree. measurements survive anonymisation; names do not.

## global decisions

**G1 — transport is `database/sql` + native drivers, not a CLI subprocess.**
pooling is the whole perf fix (606 sample round trips are login setup, not query time);
`context` cancellation is real; no `-y 0` flag conflicts, no `~~OBJ~~` sentinel parsing, no
tab-separated stdout splitting -> a procedure body containing a tab stops being a bug class.
same shape for every engine: `pgx` and `go-mssqldb` both present `database/sql`, so engines
differ in their queries, not their plumbing.

the pooling win is **independent of auth**: it holds for postgres, for sql logins, and for
kerberos alike. only *which driver* provides it is in question below.

**G1a — kerberos on sqlserver: pure-go works single-realm, fails cross-realm.** measured, not
assumed. `go-mssqldb`'s `integratedauth/krb5` is a thin wrapper over `jcmturner/gokrb5`:

- ccache, keytab and raw-credential login modes all present; EPA / channel binding landed
  2026-03 -> an instance with Extended Protection on is fine.
- macOS default ccache is `API:`-type (keychain-backed) and gokrb5 reads `FILE:` only ->
  `kinit -c FILE:<path>` is mandatory, and is the likeliest cause of field reports that
  pure-go `-E` "does not work".
- **cross-realm is not supported.** gokrb5 cannot do it (go-mssqldb #264, open; the upstream
  fix, gokrb5 #536, has sat unmerged for years), and gokrb5 itself was last pushed 2024-07 —
  effectively unmaintained. a `Cannot generate SSPI context` failure against an AG listener is
  usually a `[capaths]` / referral problem, i.e. exactly this gap.

-> **30-second decider, run before any code:** `kinit` then `klist`. a `krbtgt/B@A` referral
ticket means cross-realm, which means pure-go is out. single-realm means pure-go is in.

**G1b — the fallback is ODBC, not a subprocess.** `alexbrainman/odbc` over the system ODBC
driver manager uses the same GSSAPI stack the working `sqlcmd -E` already proves out,
cross-realm `[capaths]` included, and **still pools** — so the entire G1 performance argument
survives the fallback intact. cost: cgo, plus a system dependency to install. a per-query
`sqlcmd` subprocess is last resort only, and even then batches many objects per invocation.

ranked, decided at R3 behind one `Conn` interface: sql login + pure-go (simplest, if the
environment permits an account) > pure-go krb5 (single-realm only) > ODBC (cross-realm, cgo) >
sqlcmd subprocess. postgres is unaffected — scram auth, `pgx`, no question.

**G2 — the LLM seam is ours, as a nested module. no third-party abstraction adopted.**
surveyed: unified wrappers immature (`pkieltyka/go-llm` 4★, `aholstenson/llms-go` 1★,
`lexlapax/go-llms` 22★, `teilomillet/gollm` 674★ stale since march); `langchaingo` 9.7k★ not
pushed since january; `cloudwego/eino` 13k★ active but a graph-orchestration framework —
dbmap makes exactly one call shape, all cost no benefit; claude code go SDKs are unofficial
CLI wrappers (171★ / 49★) and nothing official exists in go.
decisive: **no library covers both API providers and headless claude code**, which is the
seam. -> `dbmap/llm`, own `go.mod`, importable from a second project without dragging in
dbmap's dependency graph. see `docs/blueprint-llm.md`.

**G3 — the CLI is a platform, not a script with flags.** binding rules, all of S1-S6:

- `main.go` -> thin `cmd/` cobra tree (flag parsing only, zero business logic) -> `internal/`.
- **`cmdutil.Factory`** bundles IOStreams + Config + the secret stores + a connection ctor,
  and every command constructor takes it. no globals -> every command is unit-testable with a
  fake factory and an in-memory store.
- **named entries, `flag > env > config file > built-in default`**, implemented as methods on
  `Config` so precedence lives in one place and is testable without a process.
- **defining an entry never activates it.** `add` writes settings; `switch` changes what is
  current. one command, one effect.
- **secrets never touch the config file.** OS keychain behind a `Store` interface
  (`Get/Set/Delete` per name) with an in-memory fake for tests -> CI never touches a keychain.
- **typed errors, one translation point.** business code returns typed errors; a single
  `cmdutil.ExitCode(err)` in `main.go` maps them to the documented exit table (S6). never a
  string-matched message, never an `os.Exit` in a command.
- **verify before store.** `add` probes the thing it just configured with the cheapest
  possible real call and refuses on a definitive rejection; ambiguous failures store with a
  warning. a typo fails at setup, not on the first real run. `--no-verify` opts out.
- **nothing is a CI dead end.** every interactive prompt has a flag; `--password-stdin` and
  env vars cover the non-tty path; `--no-input` turns any missing value into an error rather
  than a hang.

two rules specific to this tool, because it holds a *database password*, not an API token:

- **two credential axes, not one.** connections (db) and backends (llm) are independent; one
  combined profile would duplicate the llm key per database. -> two named namespaces (S3), a
  profile merely *selects* one of each. keyring keys `db:<name>` and `llm:<name>`.
- **no silent plaintext fallback.** default `secrets.fallback: never` — a keychain failure is
  a hard error naming the fix, never a quiet 0600 file on disk, because a transient dbus
  hiccup would otherwise persist a DB password permanently. plaintext is opt-in
  (`--allow-plaintext`, or config). headless CI uses env vars (S4), which is what makes
  `never` safe as the default.

## chunks

shell — the CLI platform (S-series):

```
S1  cli          command surface, noun-verb vocabulary, persistent flags
S2  factory      cmdutil.Factory: the dependency bundle and testability seam
S3  config       config.yml — named db connections, named llm backends, profiles,
                 precedence. non-secret only
S4  secrets      keyring Store, env-var path, opt-in plaintext, what is NEVER stored
S5  io           streams, tty/color/no-input, prompts, table|json|tsv writers
S6  errors       typed error vocabulary -> documented exit-code table
```

domain — the index pipeline (R-series):

```
R1  catalog      engine-agnostic data model — the vocabulary every chunk speaks
R2  engine       the interface + the safety contract (read-only gate, guard, health)
R3  connect      connection record -> DSN -> pooled Conn; auth modes per engine
R4  sqlserver    engine impl: sys.objects, DMVs, MAX_GRANT_PERCENT
R5  postgres     engine impl: pg_catalog, READ ONLY txn, no fetch signal
R6  fingerprint  sampleDepth + per-kind content hashes
R7  plan         two-tier staleness planner (modify signal -> fetch, hash -> describe)
R8  sample       projection planning, PII rules, TOP n with no ORDER BY
R9  redact       secrets out of module bodies on arrival, before the cache
R10 describe     prompts per kind, cost batching, name-keyed answer matching
R11 index        the TSV tree + reading staleness back out of it
R12 cache        resumable on-disk fetch cache
R13 cmd:index    the main command: stage order, failure policy, resumption
R14 cmd:doctor   connectivity + health + auth preflight, sends nothing heavy
```

llm module (`docs/blueprint-llm.md`): own file — separate module, separate lifecycle, and a
second consumer outside this repo.

## provisional command surface (S1 will pin this down)

```
dbmap connection add|list|remove|test <name>   db connections; secret -> keychain
dbmap backend    add|list|remove|test <name>   llm backends; api key -> keychain
dbmap profile    create|list|switch|show       binds one connection + one backend + prefs
dbmap index <connection> [--db X] [--match S] [--limit N] [--samples N] [--dry-run]
dbmap doctor [<connection>]
dbmap config get|set|list
dbmap completion | version
```

`connection test` and `backend test` expose the `verify before store` probe for reuse after the
fact: one cheap catalog read per engine, one trivial schema-constrained prompt per backend.

order rationale: S1-S6 first — they are the skeleton every later chunk plugs into, and they
are the part that makes this "a good CLI app" rather than a script with flags. R1 is the
vocabulary; R2 the contract R4/R5 satisfy; R3 is where S4's stored secret becomes a live
pool. R6-R7 are pure and portable (the JS tests transfer almost literally). R10 depends on
the llm blueprint. R13 is last: only composition.

---

RATIFY THIS DECOMPOSITION AND THE ORDER BEFORE ANY CHUNK IS WRITTEN.
