# dbmap

A CLI that reads a database's catalog and writes a compact index an agent can
read in one pass: what tables exist, what a row of each one is, what the lookup
codes mean, what every procedure does.

Built because every session that touches a database starts from zero — list the
tables, describe the columns, guess the join, run three more queries to learn
that a status column holds six values. That work is repeated in full next
session against a schema that did not change.

Part of a family of map tools: `dbmap`, `sitemap`, `repomap`.

## Status

The engine exists and is proven, as JavaScript, in `reference/js/`. It has been
run against a live SQL Server estate: 1,237 objects catalogued, structure and
bodies fetched, a partial index built and descriptions generated. Go is the
target implementation; the JS is a reference to port from, not to ship.

Not yet done: a full single-database run end to end (largest tried is 12
objects), sampling at scale (606 tables), and any engine but SQL Server.

## What it produces

Per environment and database:

- `tables.tsv` — one row per table: name, rows, size, triggers, column count,
  modified, fingerprint, description
- `views.tsv` — name, column count, modified, fingerprint, description
- `procedures.tsv` — name, parameters inline, modified, fingerprint, description
- `functions.tsv` — name, return type, parameters, modified, fingerprint,
  description
- `synonyms.tsv` — name, target
- `columns/<schema>.<name>.tsv` — per table and view: every column with type and
  nullability, then the primary key and indexes as trailing comments

Real output:

```
name                rows   size   triggers cols modified            fingerprint      description
dbo.OrderStatuses   11     16 KB  0        2    2026-08-06T00:14:34 da8fde0d9b084f02 Maps order status codes to their descriptions, one row per status.
```

```
# dbo.LedgerDelta @93aff0125b347f5d
# rows 33490305  size 4 GB  triggers 0
column                type     null      extra
LedgerDeltaID         int      not null  identity
ChangeLogID           int      null
# pk LedgerDeltaID
# index ixCustomerID (CustomerID, ModifiedDate)
# index ixChangeLogID (ChangeLogID) unique
```

Design rules for the output:

- Tables and views get a file each, because a reader needs every column
- Procedures and functions do not, because parameters fit on one line and the
  index does not reproduce bodies
- Staleness (`modified`, `fingerprint`) lives in the catalogs themselves. An
  earlier design had a separate `state.tsv`; it was deleted because a parallel
  file drifts, and `modified` earns its place for a reader anyway — a procedure
  untouched since 2018 says something a description cannot
- Nothing derivable is stored. Sample depth is `min(rows, 25)` and `rows` is
  already a column

## The failure this is designed around

A previous attempt at this took a SQL Server instance down. Not a slow query —
the box stopped accepting connections, and the cluster service could not
restart, because Windows itself ran out of memory.

Measured cause, on the stage instance:

- 125 GB total physical memory
- `max server memory` configured at 119 GB
- roughly 4 GB free to the OS at rest

That leaves the network stack, the availability-group threads and the cluster
service about 6 GB between them. Any query taking a large memory grant starves
the OS. The instance is fragile by configuration; a DBA should look at that
independently, since the next person to run a heavy query trips the same wire.

The data that makes it easy to trip:

- 606 tables across three databases, 563 GB total
- 20 tables over 100M rows
- largest: 616,802,364 rows, 42 GB

So any `COUNT(*)`, `sp_spaceused` loop, or `SELECT DISTINCT` for value domains
is a multi-hundred-GB scan. That is what must never happen, and every safety
rule below exists for it.

## Safety rules (non-negotiable)

- **Every query carries `OPTION (MAXDOP 1, MAX_GRANT_PERCENT = 1)`.** Applied
  centrally in the sqlcmd wrapper, after the read-only check, so no code path
  reaches the server without it. `MAX_GRANT_PERCENT` is engine-enforced: a
  query wanting more memory spills to tempdb instead of taking RAM. Verified
  working on SQL Server 2019 (15.0.4430.1, compat 150)
- **Every query must open with `SELECT` or `WITH` and clear a deny list.** A
  script spawning sqlcmd never reaches a PreToolUse gate, so the gate is
  in-process and stricter. Fail-closed: anything unclassifiable is refused
- **Row counts and sizes come from `sys.dm_db_partition_stats`**, which reads
  stored page totals. Never `COUNT(*)`, never `sp_spaceused`
- **Samples use `TOP (N)` with no `ORDER BY`.** An `ORDER BY` ranks the whole
  table; without one the engine stops after N rows, so a 616M-row table is as
  cheap to sample as an 11-row lookup
- **No `SELECT DISTINCT` for value domains.** See "value domains" below — it is
  not needed and is the single most dangerous query shape here
- **Health is checked before every source and every batch**, not once at
  startup. Stop conditions: OS free memory under 2 GB, server reporting
  "memory is low", `process_physical_memory_low` set, or more than 5 queries
  queued for a grant. An unreadable reading is *unknown*, never healthy
- **Read-only connections** (`-K ReadOnly`), which on an availability group
  route to a secondary replica
- **Never production.** The environment table simply has no prod entry;
  resolving one throws

## Staleness: two tiers

The central idea. Two signals guard two different costs.

- **`modify_date` decides whether to FETCH.** Free (already in the manifest),
  never misses a real change, but over-reports badly: AppCore's 544 procedures
  cluster on deploy days — 58 on 2024-07-25, 53 on 2025-02-14 — because a
  release `ALTER`s a batch of them, most byte-identical to what was there
- **A content hash decides whether to DESCRIBE.** Computed locally on what came
  back. Fetching is seconds; describing is an LLM call

So a release that touches 58 procedures fetches 58 bodies (cheap) and describes
the 3 that actually changed. Without the second tier it would be 58 LLM calls.

Fingerprints:

- Modules hash their definition, normalised for line endings and trailing
  whitespace so a deployment tool rewriting CRLF does not read as an edit
- Tables have no definition, so they hash their structure: columns with type,
  nullability and identity, primary key, indexes, foreign keys, trigger count,
  plus sample depth
- Sample row *values* are deliberately excluded. They change on every run
  against a live database and would leave every table permanently dirty

### Sample depth

`sampleDepth(rows) = min(rows, SAMPLE_ROWS)` — how many rows the describer
actually sees. A table is stale when that number moves.

- `0 -> 100` — 0 vs 25, redescribe: the model had nothing, now it has a sample
- `8 -> 9` — 8 vs 9, redescribe: an enum gained a value, and at that size every
  row is a value worth naming
- `40 -> 60` — 25 vs 25, skip: the same first 25 rows
- `616M -> 617M` — skip
- `40 -> 10` — 25 vs 10, redescribe: the sample shrank

Applies to **tables only**. A procedure is described from its body, which
`modify_date` already covers. An earlier version let the rule run on every kind;
it only worked because procedures report zero rows — a silent no-op rather than
a stated rule.

An earlier design used log-scale buckets (empty/tiny/small/populated). It was
wrong: it created an arbitrary boundary at 999/1000 that caused pointless
redescribes, and it did not express the actual reason, which is "does the model
see something different".

`SAMPLE_ROWS = 25`, chosen from the data: 148 of the 491 non-empty tables hold
25 rows or fewer — the lookup tables, where the sample is the whole table. The
distribution flattens past that (raising it to 30 covers five more tables).

## Value domains

The distinct values a status or lookup column takes, and what each means. The
single most repeated discovery in day-to-day work.

They do **not** need a `DISTINCT` scan. In practice the domains are their own
lookup tables, and they are tiny:

```
dbo.OrderStatuses      11 rows     0 Incomplete, 1 Pending, 2 CC Declined, ...
dbo.CustomerStatuses    9 rows
dbo.OrderTypes         15 rows
dbo.DeclineReasons  17 rows
```

45 such tables in one database alone, every one under 25 rows. So sampling a
small table *is* capturing its value domain, for free, with no dangerous query.

Sampling is therefore ordered complete-tables-first, so a capped run spends its
budget on domains rather than on 25 rows off the front of a 616M-row log.

## Scope: what is indexed

Decided from measurement, not assumption.

- Included: tables, views, stored procedures (T-SQL and CLR), scalar and
  table-valued functions, synonyms
- Excluded: the `cdc` schema. All 534 inline table-valued functions across the
  three databases are auto-generated Change Data Capture functions
  (`cdc.fn_cdc_get_all_changes_<table>`), two per tracked table. Exactly one
  handwritten inline TVF exists
- Excluded: triggers. 258 in one database at close to one per table, generated
  changelog writers. The parent table records a trigger *count* instead
- Excluded: a foreign key graph. **Two** declared foreign keys exist across all
  three databases. Joins here are naming convention, enforced nowhere.
  Inferring join candidates from column names was considered and rejected —
  guesses dressed as data

Dropping cdc and triggers took the describe workload from ~2,030 objects to
1,237.

## Measured reference data

SQL Server 2019, stage, three databases.

- AppCore — 807 objects: 544 procedures, 217 tables, 23 views, 22 functions,
  1 synonym. 310 GB
- SyncStore — 384 objects: 344 tables, 23 procedures, 9 views, 8 functions.
  215 GB. Also 486 cdc functions and 258 triggers, both excluded
- SideStore — 46 objects: 45 tables, 1 procedure. 38 GB

Metadata volumes (tiny, which is the point):

- 6,122 columns, 1,314 index columns, ~1,341 parameters, 896 module bodies
- 2 foreign key columns
- AppCore module corpus: 587 modules, 3.7 MB cached

Module body sizes (AppCore, 587 modules) — drives the prompt cap:

- average 6,543 chars, max 88,623
- 350 (60%) under 4,000
- 465 (79%) under 8,000
- 529 (90%) under 16,000
- 564 (96%) under 32,000

Table row counts (606 tables, 115 empty) — drives `SAMPLE_ROWS`:

- 76 tables at 1-5 rows
- 33 at 6-10
- 17 at 11-15
- 14 at 16-20
- 8 at 21-25
- 21 at 26-50
- 20 at 51-100
- 48 at 101-1,000

## Pipeline

1. **Manifest** — one query per database against `sys.objects` joined to
   `sys.schemas`, with `sys.dm_db_partition_stats` for rows and size and a
   trigger count per parent. ~1,240 rows total, seconds. This is the checkpoint
   everything resumes from
2. **Structure** — one bounded query per source per database: columns, indexes
   and keys, foreign keys, parameters, synonyms. Health-checked between each
3. **Module bodies** — batched at 40 object ids per query. Definitions contain
   tabs and newlines, so they cannot ride a tab-separated row format; they come
   back in raw mode delimited by a sentinel line carrying the object id
4. **Sample** — one query per table, `TOP 25`, explicit projection, PII columns
   omitted
5. **Describe** — batched LLM calls
6. **Write** — the TSV tree above

Caching: keyed by object and `modify_date`, so a killed run resumes and refetches
nothing. The describe stage never holds a database connection.

### sqlcmd specifics (SQL Server engine)

- `-y 0` is mutually exclusive with both `-W` and `-h`. Raw mode must use `-y 0`
  alone; row mode uses `-h -1 -W -s <sep>`
- Module batches are delimited by `CHAR(10) + '~~OBJ~~' + object_id + CHAR(10)`,
  split on the sentinel rather than on lines
- `-M` is required for availability group listeners or the connection hangs
- `-C` to accept the self-signed certificate
- `-b` to exit non-zero on a SQL error

## Secrets and PII

- **Module bodies are redacted on arrival**, before the cache is written and
  long before a prompt. Patterns keep the key and drop the value
  (`Password = <redacted>`) — that a procedure authenticates somewhere is worth
  indexing, the credential never is. Covers passwords, connection strings, API
  keys, bearer tokens, email addresses. It reports counts rather than silently
  swallowing, so a real secret shows up in the build summary
  - Actual result across 622 procedure bodies: 7 email addresses (staff
    distribution lists in `sp_send_dbmail` calls), zero credentials
- **Sample projections omit PII columns entirely** — matched on column name,
  because a type says nothing about who a value belongs to. The sample records
  which columns it withheld, so the describer knows they exist
- **Blob and `(max)` columns are not sampled** — unbounded by definition
- **Every sampled cell is capped** at `LEFT(CAST(c AS nvarchar(200)), 200)`
- **Sample values never reach the index.** They are transient describer input

## Describing

Batched: several objects per model call, sized by prompt characters rather than
count, because objects are wildly uneven — a 200-line procedure and a two-column
lookup table are not the same unit of work. Budget 40,000 characters or 12
objects, whichever comes first. An object over budget goes alone rather than
being dropped. Roughly 100 calls for 1,237 objects instead of 1,237.

Answers come back keyed by object name and are matched by name, not position, so
a reordered response still lands correctly. An object the model skipped is
reported, not silently blank. A failed batch is logged and skipped — a partial
index beats none.

Body cap for prompts: 16,000 characters, sending 90% of modules complete. A
procedure's logic cannot be summarised from its opening because the writes are
usually at the bottom. The cache cap is higher (50,000) so the stored copy is
always the fuller one.

### Two hard-won prompt lessons

**Never name the output field `description`.** With a field named `description`
whose schema text read "one sentence saying what this object does", every model
filled it with a description *of the field*:

```
Stored procedure analysis: functionality, operation type, and data sources.
One-sentence summary of what dbo.PromoGet does and the tables it touches.
```

Sonnet was worse than Haiku — better instruction-following, more faithfully
wrong. Renaming the field to `sentence` with the schema text "Put the finished
sentence here verbatim. Never describe what the sentence would say" fixed every
case. There is a regression test pinning this.

**Give a verb-first instruction with worked examples.** Listing requirements
("say whether it reads or writes, and name the main tables") invites a small
model to restate the list. Naming the failure mode and showing good and bad
examples fixes it.

After both fixes, on Haiku:

```
dbo.PromoGet     Retrieves customer promo codes from PromoConfigurations and
                 GlobalPromoRedemptions, evaluating status and days valid by order.
dbo.PromoSet     Creates or updates a promo code in PromoConfigurations, generating
                 one if needed, after validating customer eligibility.
dbo.MonthlyHierarchySnapshotBuild
                 Builds an end-of-month compressed hierarchy snapshot by calculating
                 qualified accounts from Period, PeriodVolumes, HierarchyTree
                 and ReferralTree.
```

**Haiku is sufficient.** That output is Haiku. Do not default to a larger model.

Empty tables are the canary for prompt bugs: a table with rows gets a sample
that papers over a missing-structure bug, an empty one produces visible garbage
("Table structure is undefined"). That is how a bug where structure was being
dropped before reaching the prompt was found.

## Go architecture

Everything above the engine boundary is engine-agnostic: staleness planning,
batching, describing, TSV writing, caching, PII rules. Only the boundary itself
is per-database.

Proposed interface, roughly:

```go
type Engine interface {
    Manifest(ctx context.Context, db Database) ([]Object, error)
    Structure(ctx context.Context, db Database) (map[string]Structure, error)
    Modules(ctx context.Context, db Database, ids []ObjectID) (map[ObjectID]string, error)
    Sample(ctx context.Context, db Database, t Table, n int) (Sample, error)
    Health(ctx context.Context, db Database) (Health, error)
    Quote(identifier string) string
}
```

`sqlserver` implements it now. `postgres` and `mongo` are new packages and
nothing above the line moves.

Engine-specific notes for later engines:

- Postgres — `pg_catalog` / `information_schema`; `pg_class.reltuples` for
  estimated counts (never `count(*)`); functions in `pg_proc`; no
  `modify_date` equivalent, so staleness needs a different fetch signal
  (possibly hashing `pg_get_functiondef` output directly, which is cheap there)
- Mongo — no schema; shape is inferred from sampled documents, and `$sample`
  or a bounded `find().limit(n)` replaces `TOP n`. The "columns" file becomes an
  inferred field list with observed types and frequencies

### Configuration

What is currently code must become config, since it is the only
deployment-specific part. In the JS reference this is `targets.js` — 62 of 1,623
lines, the *only* file with deployment-specific code. Everything else that
mentions a real database name does so in a comment explaining a constant.

Config should carry:

- environments: name to host, and which are forbidden (production is absent, not
  flagged)
- databases: logical name to physical name per environment. These do not follow
  a rule and must be spelled out — one estate appends a digit in dev, another
  renames the database outright
- health floors: the 2 GB OS floor suits a 125 GB server, not a laptop
- excluded schemas
- output root

### Describe backend

The JS reference shells out to `claude -p --output-format json --json-schema
... --allowedTools ""`. For a general tool that is too tight a coupling: make it
a `--describe-command` receiving a prompt on stdin and returning JSON, with
Claude Code as the default rather than the requirement. The invoke function is
already injected in the reference, so the seam exists.

## Testing expectations

The JS reference carries ~110 tests for this engine. Port the ones that encode
decisions rather than implementation:

- every generated query is read-only, and writes/bypasses are refused
- the memory grant cap is present on every query built
- row counts never come from a scan
- `TOP n` sampling emits no `ORDER BY`
- PII columns are never selected; a table of only PII is never queried
- sample depth transitions: 0->100, 8->9, 40->60, 616M->617M, 40->10
- the sample rule does not apply to non-tables
- a redeployed byte-identical procedure costs no description
- the schema output field is not named `description`
- batch answers match by name, not position
- production cannot be resolved
- redaction strips a credential and keeps the key; ordinary T-SQL is untouched

## Open questions

- Full-database run has not happened. 606 sample round trips at a few seconds
  each is 20-40 minutes, dominated by connection setup. Go should pool or reuse
  connections rather than spawning a client per query — the JS reference spawns
  `sqlcmd` per query, which is the main reason it is slow
- Whether to use a native driver in Go instead of shelling to `sqlcmd`. A native
  driver removes the `-y 0` flag conflicts, the sentinel parsing and the
  per-query process spawn entirely. Kerberos auth is the thing to verify first
- Incremental descriptions across schema-only changes: adding a column changes a
  table's fingerprint and forces a redescribe even when the description would
  not change. Acceptable, but a cheaper rule may exist
- Editing a value in place in a 9-row lookup keeps the row count and does not
  trigger a redescribe. Accepted as rare; a `--force` rebuild covers it

## Reference implementation

`reference/js/` is the working JavaScript, copied verbatim from the internal Claude Code
plugin where it was developed:

- `sqlcmd.js` — read-only gate, grant cap, output modes, row parsing
- `health.js` — memory headroom, stop conditions as data
- `targets.js` — environments and databases (becomes config in Go)
- `scope.js` — object kinds and excluded schemas as data
- `manifest.js` — the catalog query and parse
- `structure.js` — five metadata sources as data
- `modules.js` — batched body fetch, sentinel parsing, redaction
- `fingerprint.js` — sample depth, module and table fingerprints
- `plan.js` — the two-tier staleness planner
- `sample.js` — projection planning, PII rules, sample rendering
- `describe-object.js` — prompts per kind, batching, schemas
- `write.js` — the TSV catalogs and column files
- `build-db-index.js`, `fetch-db-manifest.js`, `fetch-db-structure.js` — drivers
- `tests/` — the test suite described above
