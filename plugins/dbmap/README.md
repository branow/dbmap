# dbmap

Answers questions about a database without connecting to one - what a table
holds, what a stored procedure does, which procedures write to a table, what a
lookup code means.

It works from an index of the schema: a directory of plain TSV and SQL files,
one row per object with a one-sentence description, and the SQL of every view,
procedure and function. Reading it needs no connection, no credential and no
binary. Building it needs the
[dbmap](https://github.com/branow/dbmap) CLI, and only on the machine that
builds.

```
/plugin marketplace add branow/dbmap
/plugin install dbmap@dbmap
```

## Skills

| Skill | For |
|---|---|
| `dbmap` | Reading the index: what a table holds, what a procedure does, what touches it |
| `dbmap-build` | Building or refreshing an index with the dbmap CLI |
| `dbmap-config` | Which indexes this machine reads, fetching a published one, the staleness reminder |

## How a tree is found

An index tree is a directory holding `databases.tsv` - a roster of the databases
under it - and one directory per database. Three ways one reaches a reader, and
the read skill treats them identically:

- **Committed in the repository.** `.dbmap/` beside the project, found by
  walking up from wherever the session is running. Nothing is registered and
  nothing is configured; cloning the repository is the whole install.
- **Built for one machine.** Under the plugin's data directory, registered by
  the build that wrote it.
- **Published by a team.** A git repository somebody else builds into, cloned
  once and pulled since.

The last two are rows in `roots.json` in the plugin's data directory, which is
the only state this plugin owns. Uninstalling the plugin is uninstalling
everything it did.

## Staleness

Off until asked for. `/dbmap-config remind 720` reports at session start when the
stalest database in a tree is older than that; `/dbmap-config remind off` stops.
The age comes from the build stamp the CLI writes into the roster, so a rebuild
silences it immediately.

## Tests

```sh
npm test
```

The registry, the roster parser, git against a real local remote, the staleness
decision, and the wiring that is done by string rather than by import.
