---
name: dbmap-config
description: Use when a developer wants to see which database indexes this machine reads, connect it to one a team publishes, fetch or update that index, or be reminded (or stop being reminded) when an index is overdue a rebuild.
argument-hint: [list | add <path> | add --remote=<url> [--subdir=<dir>] | sync [<name>] | remove <name> | remind <hours>|off]
allowed-tools: [Bash(node ${CLAUDE_PLUGIN_ROOT}/scripts/roots.js*)]
---

# dbmap - Config

Pass `$ARGUMENTS` through and relay what it prints:

```bash
node ${CLAUDE_PLUGIN_ROOT}/scripts/roots.js $ARGUMENTS
```

- `list` - every index this machine reads, its databases, when each was built
- `add <path>` - read a tree built elsewhere on this machine
- `add --remote=<git url> [--subdir=<dir in the repo>] [--branch=<branch>]` -
  read a tree a team publishes; follow with `sync`
- `sync [<name>]` - clone or update the published trees
- `remove <name>`
- `remind <hours>` / `remind off` - session-start warning that an index is
  overdue, off until set

An index committed in the current repository is found automatically and is never
registered. Exits non-zero on a bad argument.

A schema moves far more slowly than code, so `remind 720` (a month) suits it
better than a threshold in days. When an index is overdue, offer `/dbmap-build`
rather than repeating the warning.
