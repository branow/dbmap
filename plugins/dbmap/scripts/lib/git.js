"use strict";
// The two git commands a published tree ever needs. They live here rather than
// in the skill so fetching an index is one approval and one deterministic path,
// not a model composing git from a description.

const { spawnSync } = require("node:child_process");
const { existsSync, mkdirSync, rmSync } = require("node:fs");
const { dirname } = require("node:path");

function run(argv, cwd) {
  const result = spawnSync("git", argv, { cwd, encoding: "utf8" });
  return {
    ok: result.status === 0,
    detail: (result.stderr || result.error?.message || "").trim(),
  };
}

// Brings one root's checkout to the remote's current state. A checkout that is
// there is RESET, never merged: it is a copy of somebody else's index and
// nothing local should ever have edited it. A checkout too broken to fetch is
// thrown away and cloned again, because there is nothing in it to lose.
function pull(root) {
  if (!existsSync(root.checkout)) return clone(root);

  const branch = root.branch ? [root.branch] : [];
  const fetched = run(["fetch", "--depth", "1", "origin", ...branch], root.checkout);
  if (!fetched.ok) {
    rmSync(root.checkout, { recursive: true, force: true });
    const recloned = clone(root);
    return { ...recloned, action: recloned.ok ? "recloned; the checkout was broken" : recloned.action };
  }

  const reset = run(["reset", "--hard", "FETCH_HEAD"], root.checkout);
  return { ok: reset.ok, action: reset.ok ? "updated" : "could not update", detail: reset.detail };
}

function clone(root) {
  mkdirSync(dirname(root.checkout), { recursive: true });
  const branch = root.branch ? ["--branch", root.branch] : [];
  const result = run(["clone", "--depth", "1", ...branch, root.remote, root.checkout]);
  return { ok: result.ok, action: result.ok ? "cloned" : "could not clone", detail: result.detail };
}

module.exports = { pull, clone };
