"use strict";
// What a tree says about itself, read with nothing but the files: the roster
// the dbmap CLI writes at the root, and the walk that finds a tree committed
// beside the project.

const { test } = require("node:test");
const assert = require("node:assert");
const { mkdtempSync, mkdirSync, writeFileSync } = require("node:fs");
const { join } = require("node:path");
const { tmpdir } = require("node:os");

const roster = require("../lib/roster.js");

const HEADER = "connection\tdatabase\tengine\ttables\tviews\tprocedures\tfunctions\tsynonyms\tbuilt";

function tree(rows, dir = mkdtempSync(join(tmpdir(), "dbmap-tree-"))) {
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, "databases.tsv"), [HEADER, ...rows].join("\n") + "\n");
  return dir;
}

test("a tree's databases come back with their counts and their stamp", () => {
  const dir = tree(["stage\tAppCore\tsqlserver\t380\t14\t194\t8\t0\t2026-03-05T09:00:00Z"]);
  const [db] = roster.databases(dir);

  assert.equal(db.connection, "stage");
  assert.equal(db.database, "AppCore");
  assert.equal(db.engine, "sqlserver");
  assert.deepEqual(db.counts, { tables: 380, views: 14, procedures: 194, functions: 8 });
  assert.equal(db.dir, join(dir, "stage", "AppCore"));
});

// The age of a tree is the age of its stalest database. Reporting the newest
// build would hide exactly the database nobody has rebuilt.
test("the oldest build is what dates a tree", () => {
  const dir = tree([
    "stage\tAppCore\tsqlserver\t380\t14\t194\t8\t0\t2026-03-05T09:00:00Z",
    "stage\tReporting\tsqlserver\t12\t0\t0\t0\t0\t2025-11-02T09:00:00Z",
  ]);
  const stalest = roster.oldest(roster.databases(dir));

  assert.equal(stalest.row.database, "Reporting");
});

// A tree written by a dbmap that indexes a kind this plugin has never heard of
// must still report it. The counts are whatever columns the roster carries.
test("an unknown object kind is reported, not dropped", () => {
  const dir = mkdtempSync(join(tmpdir(), "dbmap-tree-"));
  writeFileSync(
    join(dir, "databases.tsv"),
    "connection\tdatabase\tengine\ttables\tsequences\tbuilt\n" +
      "dev\tAppCore\tpostgres\t3\t7\t2026-03-05T09:00:00Z\n",
  );

  assert.deepEqual(roster.databases(dir)[0].counts, { tables: 3, sequences: 7 });
});

// A tree from before the roster existed is still perfectly readable: the
// catalogs are there. Treating it as an error would break the read path for
// every index built by an older release.
test("a tree with no roster reads as empty rather than failing", () => {
  const dir = mkdtempSync(join(tmpdir(), "dbmap-tree-"));
  mkdirSync(join(dir, "stage"), { recursive: true });

  assert.deepEqual(roster.databases(dir), []);
  assert.equal(roster.isTree(dir), false);
});

// Discovery is what keeps the committed-tree case at zero configuration.
test("a tree committed in the repository is found from a subdirectory", () => {
  const repo = mkdtempSync(join(tmpdir(), "dbmap-repo-"));
  tree(["stage\tAppCore\tsqlserver\t1\t0\t0\t0\t0\t2026-03-05T09:00:00Z"], join(repo, ".dbmap"));
  const deep = join(repo, "src", "api");
  mkdirSync(deep, { recursive: true });

  assert.equal(roster.discover(deep), join(repo, ".dbmap"));
});

test("discovery stops rather than walking to the filesystem root forever", () => {
  const empty = mkdtempSync(join(tmpdir(), "dbmap-empty-"));

  assert.equal(roster.discover(empty), null);
});
