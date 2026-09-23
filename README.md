# dbmap

Read a database's catalog and write a compact index a coding agent can read in
one pass: what tables exist, what a row of each one is, what the lookup codes
mean, and what every procedure does.

Every session that touches an unfamiliar database starts from zero — list the
tables, describe the columns, guess the join, run three more queries to learn
that a status column holds six values. `dbmap` does that once, writes the
answers to a small tree of TSV files, and on the next run re-reads only what
actually changed.

- **SQL Server and PostgreSQL**, behind one interface. Other engines are a
  package, not a rewrite.
- **Descriptions from any model** — the Anthropic API, any OpenAI-compatible
  endpoint (including Ollama and vLLM), or the Claude Code CLI you already have
  logged in.
- **Safe on large, busy databases by construction.** Every query is proven
  read-only before a connection is opened, carries a resource cap, and never
  scans a table.
- Credentials live in the OS keychain, never in a config file.

## What it produces

```
.dbmap/<connection>/<database>/
  tables.tsv        name, rows, size, triggers, columns, modified, fingerprint, description
  views.tsv
  procedures.tsv    parameters inline
  functions.tsv     return type and parameters inline
  synonyms.tsv
  columns/<schema>.<name>.tsv   one per table and view: every column, key, index
  bodies/<schema>.<name>.sql    one per view, procedure and function
```

`tables.tsv`:

```
name                    rows  size    triggers  cols  modified             fingerprint       description
public.order_statuses   3     32 KB   0         3                          b8faf991d5fdbc5d  Maps order status codes to their descriptions, one row per status.
public.orders           500   104 KB  0         5                          8a928f2e9dabdf50  Holds one row per placed order, keyed by order id.
```

`columns/public.order_statuses.tsv`:

```
# public.order_statuses @b8faf991d5fdbc5d
# rows 3  size 32 KB  triggers 0
# Maps order status codes to their descriptions, one row per status.
column          type     null      extra
id              integer  not null
name            text     not null
email_contact   text     null
# pk id
```

`bodies/public.open_orders.sql`:

```sql
-- public.open_orders @94141fe08d0f22c2
-- Exposes pending orders from the orders table, showing id and total per order.

 SELECT o.id, o.total
   FROM orders o
     JOIN order_statuses s ON s.id = o.status_id
  WHERE s.name = 'Pending'::text;
```

It is all plain text, so an agent — or `grep` — can read it without a parser.

Bodies are written for the same reason columns are. A description is one
sentence and names an object's *main* tables, not all of them, so it cannot
answer the question you ask most after "what exists":

```sh
# what touches this table, and which of those write to it?
grep -rl "dbo.Orders" .dbmap/prod/AppCore/bodies/
grep -rliE "(insert|update|delete)[^;]{0,40}dbo\.Orders" .dbmap/prod/AppCore/bodies/
```

That answers exactly, with no database connection — which is the whole premise
of the index. Bodies arrive already redacted, so a credential never reaches one.

## Installation

**Homebrew** (macOS and Linux):

```sh
brew install branow/tap/dbmap
```

**Scoop** (Windows):

```powershell
scoop bucket add branow https://github.com/branow/scoop-bucket
scoop install dbmap
```

**Linux packages**: `deb`, `rpm` and `apk` packages are attached to the
[latest release](https://github.com/branow/dbmap/releases/latest).

**Shell script** (Linux and macOS; installs to `/usr/local/bin`, or
`~/.local/bin` when that is not writable):

```sh
curl -fsSL https://raw.githubusercontent.com/branow/dbmap/main/scripts/install.sh | sh
```

**From source** (Go 1.26+):

```sh
go install github.com/branow/dbmap@latest
```

On macOS, build with `CGO_ENABLED=1` (the default for a native build). The
keychain backend uses the Security framework so each stored secret is bound to
the binary that stored it; a `CGO_ENABLED=0` macOS build silently falls back to
a weaker path.

## Quick start

```sh
# 1. Tell dbmap about a database. The password goes to the keychain,
#    and the connection is probed before anything is stored.
dbmap connection add local \
  --engine postgres --host 127.0.0.1 --database appcore \
  --username app --auth scram

# 2. Tell it which model to write descriptions with.
dbmap backend add haiku --provider claudecode --model claude-haiku-4-5

# 3. Bind the two together and make it current.
dbmap profile create dev --connection local --backend haiku
dbmap profile switch dev

# 4. Check everything before a build depends on it.
dbmap doctor

# 5. Build the index.
dbmap index
```

Start small on an unfamiliar database — `--dry-run` reports the plan without
sending a single query beyond the catalog listing:

```sh
dbmap index --dry-run
dbmap index --match order --limit 20
```

## How it decides what to rebuild

A full run costs one model call per object, so `dbmap` works hard not to make
them twice. Staleness is decided in two tiers:

- The engine's **modify signal** decides what to **fetch**. That is free, it is
  already in the catalog listing, and fetching is cheap.
- A **content fingerprint** of what came back decides what to **describe**. That
  is the expensive half.

A release that touches fifty stored procedures moves fifty modify dates, so
`dbmap` re-reads fifty bodies — and describes only the ones whose text actually
changed. Re-running against an unchanged database costs nothing:

```
$ dbmap index
fetched     0
reused      3
described   0
unchanged   3
cost        0
```

PostgreSQL exposes no per-object modify date, so there the first tier is a
no-op: everything is re-read (catalog reads are cheap) and the fingerprint does
all the real work. The result is the same.

`--force` rebuilds everything and ignores both tiers.

## Usage

### Connections and backends

Database connections and model backends are two independent named sets; a
profile binds one of each. That way one model backend serves every database
without repeating its API key.

```sh
dbmap connection add prod-replica --engine sqlserver --host db.internal \
  --database AppCore --auth kerberos
dbmap connection list
dbmap connection show prod-replica
dbmap connection remove prod-replica

dbmap backend add gpt --provider openai --model gpt-4o-mini
dbmap backend add local-llm --provider openai --base-url http://127.0.0.1:11434/v1
```

Supported engines are `postgres` and `sqlserver`; authentication is `scram`
(PostgreSQL), `sqllogin` (SQL Server) or `kerberos` (both). Backends are
`anthropic`, `openai` (any OpenAI-compatible endpoint) and `claudecode`, which
shells out to the Claude Code CLI and needs no API key of its own.

A connection marked `--production` is refused: `dbmap` will not index it.

### Building an index

```sh
dbmap index [connection] [flags]

  --db string        database to index; defaults to the connection's
  --match string     only objects whose name contains this substring
  --limit int        process at most this many objects
  --samples int      tables to sample: 0 for none, -1 for every table described
  --out string       where the index tree is written (default ".dbmap")
  --backend string   model backend to describe with
  --dry-run          report the plan; send no query beyond the catalog listing
```

`--match` and `--limit` write a *partial* index covering only what they
selected, which is the right way to try `dbmap` against a database you do not
own.

### Checking a connection

`dbmap doctor` reports one line per check and sends nothing heavy — one login
and one health statement, no catalog read and no sampled row:

```
CHECK       STATUS  DETAIL
credential  pass    found under db:local
connection  pass    postgres at 127.0.0.1 via scram
auth        pass    the server accepted the login
read-only   pass    a guarded read-only statement was accepted
health      pass    the server reports room for a build
```

### Scripting

Every command takes `-o json`, every prompt has a flag, and `--no-input` turns a
missing value into an error instead of a hang. Secrets can come from the
environment (`DBMAP_SECRET_DB_<NAME>`, `DBMAP_SECRET_LLM_<NAME>`), which is how
a CI runner works with no keychain at all.

Exit codes are meaningful:

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Generic failure |
| 2 | Cancelled at a prompt |
| 3 | Validation error — bad flags, unknown name, unsupported configuration |
| 4 | Authentication or authorization failure |
| 5 | Not found |
| 6 | Unavailable — server unreachable, rate limited, or out of room |

Settings resolve as **flag > environment > config file > built-in default**.
Configuration lives in `~/.config/dbmap/config.yml` (`%AppData%\dbmap` on
Windows) and never contains a secret.

## Safety

`dbmap` is built to be pointed at a large, busy, shared database. A catalog
walk is exactly the kind of job that can cost a server more than it can spare —
`COUNT(*)` per table, an `sp_spaceused` loop, or a `SELECT DISTINCT` to learn a
column's values each read every page they touch — so the expensive query shapes
are not available to the tool at all:

- Every statement must be provably read-only — it must open with `SELECT` or
  `WITH` and clear a deny list — and is refused **before a connection is
  opened** if it cannot be proven. Anything unclassifiable is refused.
- Every statement carries its engine's resource cap: `OPTION (MAXDOP 1,
  MAX_GRANT_PERCENT = 1)` on SQL Server, and on PostgreSQL a read-only
  transaction with a statement timeout and parallelism disabled.
- Row counts and sizes come from catalog statistics. There is no `COUNT(*)` and
  no `sp_spaceused` anywhere in the tool.
- Samples read `TOP (n)` / `LIMIT n` with **no `ORDER BY`**, so sampling a
  huge table costs the same as sampling an eleven-row lookup.
- Server health is checked before every batch, not once at startup. If the
  server says it is short of memory, the run stops. If health cannot be read at
  all, that is *unknown*, never healthy — and the run stops too.
- Columns whose names suggest personal data are never read, matched on the name
  because a type says nothing about who a value belongs to. A table with nothing
  else in it is never queried at all. Withheld columns are still listed in the
  index, so the description knows they exist.
- Sampled values are shown to the model and then discarded. They never reach the
  index and never enter a fingerprint.
- Procedure bodies are redacted on arrival, before anything is cached or sent:
  patterns keep the key and drop the value, so `Password = <redacted>` still
  records that a procedure authenticates somewhere.
- A connection marked as production cannot be indexed.

## Development

```sh
go build ./...
go test ./...
cd llm && go test ./...     # the llm module is separate
```

`llm/` is a nested Go module (`github.com/branow/dbmap/llm`) with no dependency
on the rest of the tool, so it can be imported on its own by anything that wants
one structured-output call across several providers.

`docs/DESIGN.md` explains why the tool is built the way it is — the staleness
tiers, the safety rules and what they were measured against, and what each
engine has to implement.

## License

MIT. See [LICENSE](LICENSE).
