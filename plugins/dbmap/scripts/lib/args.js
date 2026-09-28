"use strict";
// The smallest argument parser that covers this plugin's scripts: --key=value,
// bare --flag, and everything else positional. No dependencies, because a
// plugin that needs an install step before its hook can run is a plugin that
// breaks session start.

function parse(argv) {
  const flags = {};
  const positionals = [];
  for (const arg of argv) {
    if (!arg.startsWith("--")) {
      positionals.push(arg);
      continue;
    }
    const body = arg.slice(2);
    const eq = body.indexOf("=");
    if (eq === -1) flags[body] = true;
    else flags[body.slice(0, eq)] = body.slice(eq + 1);
  }
  return { flags, positionals };
}

module.exports = { parse };
