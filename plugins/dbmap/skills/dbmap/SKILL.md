---
name: dbmap
description: Use when a task touches a database object - a table's columns, what a procedure or function does, what writes to a table, what a lookup holds, or which database has it. Reads a local index; no connection.
allowed-tools: [Read, Glob, Grep, Bash(node ${CLAUDE_PLUGIN_ROOT}/scripts/roots.js*)]
---

# dbmap - Read

Four steps against plain files. No connection, no credential, no binary.

## Step 1: Locate the index

```bash
node ${CLAUDE_PLUGIN_ROOT}/scripts/roots.js list
```

Prints every tree on this machine, the databases in each and when they were
built. `<DB>` below is `<printed path>/<connection>/<database>`.

Nothing listed means no index exists: say so and offer `/dbmap-build`. Do not
query a database instead.

## Step 2: Read the catalog for the kind

`<DB>/tables.tsv`, `views.tsv`, `procedures.tsv`, `functions.tsv`,
`synonyms.tsv`. One row per object: `<schema>.<name>` first, description last.

Read the whole file rather than grepping it - it is a list of one-sentence
descriptions, which is how you find an object whose name you do not know.
`modified` and `fingerprint` are build state; ignore them.

## Step 3: Read the object

- `<DB>/columns/<schema>.<name>.tsv` - table or view: every column, type,
  nullability, then `# pk`, `# index`, `# fk`
- `<DB>/bodies/<schema>.<name>.sql` - view, procedure or function: its SQL,
  credentials redacted

## Step 4: Cross-object questions - grep the bodies

A description names an object's main tables, not all of them.

```bash
# everything that mentions the table
grep -rl "dbo.Orders" <DB>/bodies/

# of those, what writes to it
grep -rliE "(insert|update|delete|merge)[^;]{0,40}dbo\.Orders" <DB>/bodies/
```

## Limits

Structure, not data. A question about values, or about an object newer than the
build date, needs the live database - say so rather than inferring it from the
schema. If a file does not match the above, `<printed path>/README.md` is the
tree's own format reference.
