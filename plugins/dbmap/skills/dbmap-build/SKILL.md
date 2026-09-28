---
name: dbmap-build
description: Use when a developer asks to build, update or rebuild a database index. Runs the dbmap CLI, which reads the catalog, describes every object with a model, and writes the index tree.
argument-hint: [<database>] [--match X] [--limit N]
allowed-tools: [Read, Bash(command -v dbmap), Bash(dbmap *), Bash(node ${CLAUDE_PLUGIN_ROOT}/scripts/roots.js*), Bash(awk *), Bash(tail *), Monitor, TaskStop]
---

# dbmap - Build

Developer tool, and the only part of this plugin that needs the `dbmap` binary.
The binary is the whole build: it decides what is stale, reads the catalog,
describes objects with a model, writes the tree. This skill points it somewhere
and reports.

A build reads a live database and costs one model call per changed object.
**Never build against production when a staging copy of the schema exists.**

## Step 0: Prerequisites

```bash
command -v dbmap
dbmap doctor [<connection>]
```

Missing binary - stop, give the install line, let the developer run it:
`brew install branow/tap/dbmap`, or
`scoop bucket add branow https://github.com/branow/scoop-bucket && scoop install dbmap`,
or `curl -fsSL https://raw.githubusercontent.com/branow/dbmap/main/scripts/install.sh | sh`.

`doctor` names a missing connection, backend or profile; `dbmap connection add
--help` covers each. Never put a credential on a command line - the CLI prompts
and stores it in the OS keychain. Everything timing out is a VPN or an expired
ticket, not configuration.

## Step 1: Decide `<out>`

Ask if the request does not say:

- **committed with the project** - `.dbmap` (the default). Anyone who clones
  reads it; nothing to register
- **this machine only** - `node ${CLAUDE_PLUGIN_ROOT}/scripts/roots.js add`
  prints the path and registers it
- **published for a team** - a checkout of the repository that carries the tree;
  commit and push it there on a branch. Teammates run
  `/dbmap-config add --remote=<url> --subdir=<dir>` once, then `/dbmap-config`

## Step 2: Plan

```bash
dbmap index [<connection>] --db <database> --out <out> --dry-run
```

Reads the catalog listing and stops. Relay the counts in one line. Get a yes
before Step 3 when `refetch` is more than a handful - a first build of a few
hundred procedures is a few hundred model calls.

## Step 3: Build, one database at a time

```bash
dbmap index [<connection>] --db <database> --out <out> 2> <log>
```

`run_in_background: true`; a first build takes minutes. Never two at once - they
share one server and one backend. Arm a `Monitor` on `tail -f -n 0 <log>`,
summarize new lines in one short line, `TaskStop` it when the run ends.

Exit codes: `4` auth, `5` not found, `6` unavailable - the server out of room,
but also a failed describe batch. Read the summary before deciding; on the
server, stop.

## Step 4: Repair a non-zero `missing`

A failed batch writes those rows with a fingerprint and no description, so the
next run's modify signal calls them untouched and they stay empty forever.

```bash
awk -F'\t' 'NR>1 && $NF=="" {print $1}' <out>/<connection>/<database>/*.tsv
dbmap index [<connection>] --db <database> --match <shared substring> --force --out <out>
```

A batch is alphabetically adjacent, so a short substring covers most of them.
One that fails again alone, with no tokens spent, is the model declining that
object: leave it and name it.

## Step 5: Report

`node ${CLAUDE_PLUGIN_ROOT}/scripts/roots.js list` shows the tree as the `dbmap`
skill will see it. Report per database what was described and what it cost, plus
whatever `--limit` left for the next run.

`--match` and `--limit` merge into the tree already there: untouched rows keep
their descriptions and fingerprints, so a narrowed run never makes the next full
one pay again.
