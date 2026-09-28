<h1 align="center">dbmap</h1>

<p align="center"><b>Give your coding agent a map of the database.</b></p>

<p align="center">
  Index a schema once into plain text, then ask your agent about tables,
  procedures and lookups — with no connection, no credential and no tokens
  spent rediscovering what it learned yesterday.
</p>

<p align="center">
  <a href="https://github.com/branow/dbmap/actions/workflows/ci.yml"><img alt="ci" src="https://github.com/branow/dbmap/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/branow/dbmap/releases/latest"><img alt="release" src="https://img.shields.io/github/v/release/branow/dbmap"></a>
  <a href="LICENSE"><img alt="license" src="https://img.shields.io/github/license/branow/dbmap"></a>
  <img alt="go" src="https://img.shields.io/github/go-mod/go-version/branow/dbmap">
</p>

---

Every session that touches an unfamiliar database starts from zero. List the
tables. Describe the columns. Guess the join. Run three more queries to learn
that a status column holds six values. Then the context window fills, the
session ends, and the next one does the same walk again.

`dbmap` does it once and writes the answers down.

## Features

- **Ask in plain language.** What does `dbo.Orders` hold, what does this
  procedure do, which procedures write to that table, what do these status
  codes mean — answered from the index, not from a connection.
- **Works from a Claude Code plugin.** Install it and your agent finds the
  index, reads it, and offers to rebuild it when it goes stale.
- **Commit it with your code.** The index is a small tree of TSV and SQL files.
  Whoever clones the repository gets it; no setup, no binary, no credential.
- **Safe on a large, busy database.** Read-only by construction, resource-capped
  per statement, never a table scan, health-checked before every batch.
- **Cheap to keep current.** A rebuild re-describes only the objects whose
  content actually changed, so a run against an unchanged database costs
  nothing.
- **SQL Server and PostgreSQL**, behind one interface.
- **Any model.** The Anthropic API, any OpenAI-compatible endpoint (Ollama and
  vLLM included), or the Claude Code CLI you are already logged into.
- **Private by default.** Credentials live in the OS keychain, personal-data
  columns are never read, and procedure bodies are redacted before anything is
  stored.

## Get started

**1. Install the plugin** — this is all a reader needs:

```
/plugin marketplace add branow/dbmap
/plugin install dbmap@dbmap
```

**2. Build an index** — once per database, by whoever has access:

```
/dbmap-build
```

It walks you through the rest: installing the CLI, adding the connection and
the model backend, and where to write the tree — committed with the project,
kept on your machine, or published in a repository your team pulls.

**3. Ask.** No command, no slash, no flag:

> What does the Orders table hold, and which procedures write to it?

Teammates who pull a published index run `/dbmap-config sync`; an index
committed under `.dbmap/` is found on its own.

See [plugins/dbmap](plugins/dbmap/README.md) for what each skill does.

## Without the plugin

The tree is plain text, so any agent can read it and so can you:

```sh
grep "dbo.Orders" .dbmap/prod/AppCore/tables.tsv
grep -rl "dbo.Orders" .dbmap/prod/AppCore/bodies/
```

Driving the build yourself is four commands:

```sh
brew install branow/tap/dbmap

dbmap connection add local --engine postgres --host 127.0.0.1 \
  --database appcore --username app --auth scram
dbmap backend add haiku --provider claudecode --model claude-haiku-4-5
dbmap profile create dev --connection local --backend haiku && dbmap profile switch dev

dbmap index
```

On a database you do not own, start with `dbmap index --dry-run`, which reports
the plan without sending a query beyond the catalog listing. Other install
methods and every flag are in the [CLI reference](docs/CLI.md).

## Documentation

| | |
|---|---|
| [plugins/dbmap](plugins/dbmap/README.md) | The Claude Code plugin: the three skills, how an index is found |
| [docs/CLI.md](docs/CLI.md) | Installing, every command and flag, scripting, exit codes, CI |
| [docs/DESIGN.md](docs/DESIGN.md) | What the index looks like, what keeps it safe and cheap, adding an engine |
| [AGENTS.md](AGENTS.md) | The contract for an agent changing this repository |

## Development

```sh
go build ./...
go test ./...
cd llm && go test ./...     # the llm module is separate
```

`llm/` is a nested Go module (`github.com/branow/dbmap/llm`) with no dependency
on the rest of the tool, so it can be imported on its own by anything that wants
one structured-output call across several providers.

## License

MIT. See [LICENSE](LICENSE).
