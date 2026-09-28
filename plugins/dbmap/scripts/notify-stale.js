#!/usr/bin/env node
"use strict";
// SessionStart hook. Says, through the hook's systemMessage, that an index tree
// is overdue a rebuild - or that a registered published tree has never been
// fetched, which reads as an empty index and is fixed by one command.
//
// Off unless `roots.js remind <hours>` set a threshold. An install that never
// opts in reads one small JSON file and prints nothing, so a person who only
// ever READS an index is never nudged towards a tool they do not run.
//
// Flag (from hooks.json): --data-dir=PATH - the plugin's data directory.

const args = require("./lib/args.js");
const roster = require("./lib/roster.js");
const { createRegistry } = require("./lib/registry.js");
const { resolveAll } = require("./roots.js");

function main(argv) {
  const { flags } = args.parse(argv);
  const registry = createRegistry(flags["data-dir"] || undefined);
  const threshold = registry.remindAfterHours();
  if (threshold === null) return null;
  return message(resolveAll(registry).trees, threshold);
}

// One line, naming the worst tree rather than every tree: a session-start
// message that lists things is a message people learn to skip.
function message(trees, threshold) {
  const unsynced = trees.find((tree) => tree.remote && !tree.present);
  if (unsynced) {
    return `dbmap: the ${unsynced.name} index has not been fetched yet. Run /dbmap-config sync`;
  }

  let worst = null;
  for (const tree of trees) {
    if (!tree.present || tree.ageHours === null) continue;
    if (worst === null || tree.ageHours > worst.ageHours) worst = tree;
  }
  if (worst === null || worst.ageHours < threshold) return null;

  const stalest = roster.oldest(worst.databases);
  const what = stalest ? `${stalest.row.connection}/${stalest.row.database}` : worst.name;
  return (
    `dbmap: ${what} was last indexed ${roster.describeAge(worst.ageHours)} ago ` +
    `(${worst.oldestBuilt.slice(0, 10)}). Run /dbmap-build to refresh it`
  );
}

let text = null;
try {
  text = main(process.argv.slice(2));
} catch {
  // A hook must never break session start, and a missed reminder costs nothing.
}
process.stdout.write(JSON.stringify(text ? { systemMessage: text } : {}) + "\n");

module.exports = { main, message };
