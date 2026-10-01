# dbmap CLI reference

Every command takes `-o json`, every prompt has a flag, and `--no-input` turns a
missing value into an error instead of a hang.

## What a build writes

```
.dbmap/
  databases.tsv     one row per database: engine, object counts, when it was built
  README.md         the layout, for whoever reads the tree
  <connection>/<database>/
    tables.tsv      name, rows, size, triggers, columns, modified, fingerprint, description
    views.tsv
    procedures.tsv  parameters inline
    functions.tsv   return type and parameters inline
    synonyms.tsv
    columns/<schema>.<name>.tsv  one per table and view: every column, key, index
    bodies/<schema>.<name>.sql   one per view, procedure and function
```

The two files at the root are what let a tree be read by someone who never
installs `dbmap`: `databases.tsv` says what is indexed and how old it is, and
`README.md` says how to read it.

`tables.tsv` — one row per object, with a sentence that says what a row *is*:

```
name                    rows  size    triggers  cols  modified  fingerprint       description
public.order_statuses   3     32 KB   0         3               b8faf991d5fdbc5d  Maps order status codes to their descriptions, one row per status.
public.orders           500   104 KB  0         5               8a928f2e9dabdf50  Holds one row per placed order, keyed by order id.
```

`columns/public.order_statuses.tsv` — the full shape, keys and indexes included:

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

`bodies/public.open_orders.sql` — the SQL itself, because a one-sentence
description names an object's *main* tables, not all of them:

```sql
-- public.open_orders @94141fe08d0f22c2
-- Exposes pending orders from the orders table, showing id and total per order.

 SELECT o.id, o.total
   FROM orders o
     JOIN order_statuses s ON s.id = o.status_id
  WHERE s.name = 'Pending'::text;
```

Bodies arrive already redacted, so a credential never reaches one. Everything is
plain text, so an agent — or `grep` — reads it without a parser.

## Connections and backends

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

dbmap profile create dev --connection prod-replica --backend gpt
dbmap profile switch dev
```

Supported engines are `postgres` and `sqlserver`; authentication is `scram`
(PostgreSQL), `sqllogin` (SQL Server) or `kerberos` (both).

TLS is always on and the server's certificate is verified. A server behind an
internal certificate authority this machine does not trust needs
`--trust-server-certificate` on the connection, which encrypts without proving
who answered.

Kerberos needs no extra configuration — `dbmap` finds your existing tickets, and
converts the credential cache itself when the driver cannot read the platform's
own format. Run `kinit` if you have no ticket; that is the whole setup.

Backends are `anthropic`, `openai` (any OpenAI-compatible endpoint, including
Ollama and vLLM) and `claudecode`, which shells out to the Claude Code CLI and
needs no API key of its own.

## Building an index

```sh
dbmap index [connection] [flags]

  --db string        database to index; defaults to the connection's
  --match string     only objects whose name contains this substring
  --limit int        process at most this many objects
  --samples int      tables to sample: 0 for none, -1 for every table described
  --out string       where the index tree is written (default ".dbmap")
  --backend string   model backend to describe with
  --force            rebuild everything, ignoring both staleness tiers
  --dry-run          report the plan; send no query beyond the catalog listing
  --quiet            silence progress
```

`--match` and `--limit` cover only what they selected and **merge** into
whatever the tree already holds: rows they did not touch keep their descriptions
and their fingerprints, so a narrowed run never makes the next full one pay to
redescribe the rest. That is what makes them the right way to try `dbmap`
against a database you do not own.

### Progress output

Progress goes to **stderr** and the summary to stdout, so `-o json` pipes
cleanly while a person watching the terminal sees the work. Every stage that
does work per object names that object, and a table names itself *before* it is
read, so a run that stalls says what it stalled on:

```
manifest: 412 objects
plan: 37 objects to refetch, 375 objects the modify signal proves untouched
fetch: structure for 37 objects
fetch: bodies for 18 objects
sample: 1/12 dbo.OrderStatuses (3 rows, whole table)
sample: 2/12 dbo.Orders (25 of 4218914 rows)
describe: 37 objects in 4 batches, 4 batches at a time
describe: batch 1/4 answered (12 objects)
describe: dbo.OrderStatuses  Maps order status codes to their descriptions.
describe: dbo.Orders  Holds one row per placed order, keyed by id.
write: tables.tsv (412 rows)
write: 380 column files, 194 body files under .dbmap/prod/AppCore
```

### What a dry run can and cannot say

`--dry-run` reports only what it can actually know. It reads the manifest and
stops, so it can say how many objects must be re-read and why — but not how many
will be described, because that is decided by a fingerprint of content it has
not fetched. It reports an upper bound and says so, rather than printing a guess
in the column a real run fills with a fact:

```
$ dbmap index --dry-run
objects     412
dry-run     true
refetch     37
untouched   375
dropped     0
reasons     modified 30, new 7
describe    at most 37, decided by content fingerprint after the fetch
```

## Checking a connection

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

## Scripting and CI

Secrets can come from the environment (`DBMAP_SECRET_DB_<NAME>`,
`DBMAP_SECRET_LLM_<NAME>`), which is how a CI runner works with no keychain at
all.

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

## Installing from source

```sh
go install github.com/branow/dbmap@latest
```

Requires Go 1.26+ and nothing else: no platform needs a C toolchain. On macOS
the keychain backend loads the Security framework at run time, so each stored
secret is still bound to the binary that stored it. Windows and Linux use
Credential Manager and the Secret Service respectively, neither of which binds
an item to one program.
