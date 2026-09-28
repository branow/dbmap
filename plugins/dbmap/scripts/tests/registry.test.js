"use strict";
// The pointer file: the only state this plugin owns, and the one place a wrong
// answer makes a tree unreadable.

const { test } = require("node:test");
const assert = require("node:assert");
const { mkdtempSync, writeFileSync, readFileSync } = require("node:fs");
const { join } = require("node:path");
const { tmpdir } = require("node:os");

const { createRegistry } = require("../lib/registry.js");

function registry() {
  return createRegistry(mkdtempSync(join(tmpdir(), "dbmap-data-")));
}

test("a registered tree comes back with an absolute path", () => {
  const reg = registry();
  reg.add({ name: "team", path: "/srv/index" });

  assert.deepEqual(reg.read().roots, [
    { name: "team", path: "/srv/index", checkout: null, remote: null, branch: null },
  ]);
});

// A published tree is a repository with the index somewhere inside it. git owns
// the checkout; the reader needs the tree, and they are not the same directory.
test("a remote root separates the checkout from the tree inside it", () => {
  const reg = registry();
  const entry = reg.add({
    name: "team",
    path: "/data/clones/team",
    remote: "git@example.internal:acme/index.git",
    subdir: "databases",
  });

  assert.equal(entry.checkout, "/data/clones/team");
  assert.equal(entry.path, "/data/clones/team/databases");
});

// Re-registering is how somebody corrects a path they got wrong. Refusing it
// would leave the wrong one in place with no obvious way out.
test("registering the same name twice replaces it", () => {
  const reg = registry();
  reg.add({ name: "team", path: "/srv/old" });
  reg.add({ name: "team", path: "/srv/new" });

  assert.equal(reg.read().roots.length, 1);
  assert.equal(reg.read().roots[0].path, "/srv/new");
});

test("removing reports whether there was anything to remove", () => {
  const reg = registry();
  reg.add({ name: "team", path: "/srv/index" });

  assert.equal(reg.remove("team"), true);
  assert.equal(reg.remove("team"), false);
  assert.deepEqual(reg.read().roots, []);
});

test("the reminder is off until a threshold is set, and off again after", () => {
  const reg = registry();
  assert.equal(reg.remindAfterHours(), null);

  reg.setRemindAfterHours(720);
  assert.equal(reg.remindAfterHours(), 720);

  reg.setRemindAfterHours(null);
  assert.equal(reg.remindAfterHours(), null);
});

// The write path must refuse a file it cannot parse. Rewriting it from the
// empty default would silently drop every root the read failed on.
test("a malformed pointer file fails a write and not a read", () => {
  const reg = registry();
  writeFileSync(reg.file(), "{ not json");

  assert.deepEqual(reg.read().roots, []);
  assert.throws(() => reg.readStrict(), /not valid JSON/);
  assert.throws(() => reg.add({ name: "team", path: "/srv/index" }), /not valid JSON/);
  assert.match(readFileSync(reg.file(), "utf8"), /not json/);
});

// Setting a threshold must not disturb the roots, and registering must not
// disturb the threshold: they are written through the same file.
test("roots and the reminder do not overwrite each other", () => {
  const reg = registry();
  reg.setRemindAfterHours(720);
  reg.add({ name: "team", path: "/srv/index" });

  assert.equal(reg.remindAfterHours(), 720);
  assert.equal(reg.read().roots.length, 1);
});

// The one that silently breaks a sync: subdir is folded into path when the root
// is registered, so re-deriving it on every read would push the tree one
// directory deeper each time the file is loaded.
test("reading a root back does not re-apply its subdir", () => {
  const reg = registry();
  const added = reg.add({
    name: "team",
    path: "/data/clones/team",
    remote: "git@example.internal:acme/index.git",
    subdir: "databases",
  });

  const [first] = reg.read().roots;
  const [second] = reg.read().roots;

  assert.deepEqual(first, added);
  assert.deepEqual(second, added);
});

// A hand-edited file can lose a field. A row that addresses nothing is dropped
// rather than resolved against whatever directory the session happens to be in.
test("a root with no path is dropped rather than guessed at", () => {
  const reg = registry();
  writeFileSync(reg.file(), JSON.stringify({ version: 1, roots: [{ name: "team" }] }));

  assert.deepEqual(reg.read().roots, []);
});
