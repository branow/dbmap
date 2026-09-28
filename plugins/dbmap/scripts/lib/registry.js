"use strict";
// The pointer file: where this machine knows index trees to be. One JSON file
// in the plugin's data directory, and the only state this plugin owns.
//
// It exists because a tree can live in three different places and the read
// skill must not care which. A tree committed in the repo is found by walking
// up from the session's directory and is never registered at all. A tree built
// for one person alone lands under this plugin's data directory. A tree an
// organization publishes lives in a git repository, cloned once and pulled
// since. All three end up as a path, and a path is all the reader needs.

const { existsSync, mkdirSync, readFileSync, writeFileSync, renameSync } = require("node:fs");
const { join, resolve, basename } = require("node:path");
const { homedir } = require("node:os");

// Data root, in precedence order:
//   DBMAP_PLUGIN_DATA  - explicit override, for tests and manual runs
//   CLAUDE_PLUGIN_DATA - the harness-provided persistent directory, exported to
//                        hook subprocesses
//   homedir fallback   - for a bare `node roots.js` outside the harness
const DEFAULT_ROOT =
  process.env.DBMAP_PLUGIN_DATA ||
  process.env.CLAUDE_PLUGIN_DATA ||
  join(homedir(), ".claude", "plugins", "data", "dbmap-dbmap");

const FILE = "roots.json";
const EMPTY = { version: 1, roots: [] };

function createRegistry(root = DEFAULT_ROOT) {
  const file = () => join(root, FILE);

  // The private tree: where a build lands when it is for this machine only.
  const localTreePath = () => join(root, "index");
  // A registered remote is cloned here, one directory per root name, so two
  // teams' trees never share a checkout.
  const clonePath = (name) => join(root, "clones", name);

  // A malformed file is an error the caller must see, because the write path
  // would otherwise rewrite it from the empty default and drop every root the
  // read could not parse.
  function readStrict() {
    if (!existsSync(file())) return { ...EMPTY, roots: [] };
    let parsed;
    try {
      parsed = JSON.parse(readFileSync(file(), "utf8"));
    } catch (err) {
      throw new Error(`${file()} is not valid JSON: ${err.message}`);
    }
    if (!parsed || typeof parsed !== "object" || !Array.isArray(parsed.roots)) {
      throw new Error(`${file()} has no roots array`);
    }
    return { version: 1, ...parsed, roots: parsed.roots.filter(usable).map(stored) };
  }

  // The read every caller that only wants to LIST uses. A broken file must not
  // stop a session starting, and a reader that finds no roots degrades to the
  // repo-local tree, which is the common case anyway.
  function read() {
    try {
      return readStrict();
    } catch {
      return { ...EMPTY, roots: [] };
    }
  }

  function write(state) {
    mkdirSync(root, { recursive: true });
    const tmp = `${file()}.tmp.${process.pid}`;
    writeFileSync(tmp, JSON.stringify({ version: 1, ...state }, null, 2) + "\n");
    renameSync(tmp, file());
  }

  // Registering the same name twice replaces it. Re-running a registration
  // after moving a tree is the ordinary way to correct one, and refusing it
  // would leave the wrong path in place with no obvious way to fix it.
  function add({ name, path, remote = null, subdir = null, branch = null }) {
    const state = readStrict();
    const entry = normalize({ name, path, remote, subdir, branch });
    state.roots = [...state.roots.filter((r) => r.name !== entry.name), entry];
    write(state);
    return entry;
  }

  function remove(name) {
    const state = readStrict();
    const kept = state.roots.filter((r) => r.name !== name);
    const removed = kept.length !== state.roots.length;
    if (removed) write({ ...state, roots: kept });
    return removed;
  }

  // The reminder threshold, in hours, or null for off - which is the state
  // every install starts in. Nobody is nudged about a tool they do not run.
  function setRemindAfterHours(hours) {
    const { remindAfterHours, ...rest } = readStrict();
    write(hours === null ? rest : { ...rest, remindAfterHours: hours });
  }

  function remindAfterHours() {
    const value = read().remindAfterHours;
    return typeof value === "number" && value > 0 ? value : null;
  }

  return {
    root,
    file,
    localTreePath,
    clonePath,
    read,
    readStrict,
    write,
    add,
    remove,
    setRemindAfterHours,
    remindAfterHours,
  };
}

// Turns what a person typed into a stored root, ONCE, on the way in. checkout
// is what git owns; path is the tree inside it, which is the same directory
// whenever the tree is the whole repository.
//
// subdir is resolved here and not kept, so this must never run over a record
// that has already been through it - reading would join the subdir again and
// point the tree one directory deeper on every load.
function normalize(entry) {
  const checkout = resolve(entry.path);
  const subdir = entry.subdir || null;
  return {
    name: entry.name || basename(checkout) || "root",
    path: subdir ? join(checkout, subdir) : checkout,
    checkout: entry.remote ? checkout : null,
    remote: entry.remote || null,
    branch: entry.branch || null,
  };
}

// A stored root is read back as it was written. The file is this plugin's own,
// so the read fills in what an older version omitted and does not re-derive
// anything.
function stored(entry) {
  return {
    name: entry.name,
    path: entry.path,
    checkout: entry.checkout ?? null,
    remote: entry.remote ?? null,
    branch: entry.branch ?? null,
  };
}

// A row with no name or no path addresses nothing. Dropping it beats carrying
// it to a caller that would resolve it to the current directory.
function usable(entry) {
  return Boolean(entry) && typeof entry.name === "string" && typeof entry.path === "string";
}

module.exports = { createRegistry, DEFAULT_ROOT };
