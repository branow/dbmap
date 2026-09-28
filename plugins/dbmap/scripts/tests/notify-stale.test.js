"use strict";
// What the session-start reminder says, and - more often - what it stays quiet
// about. A message that appears when nothing is wrong is a message people learn
// to skip, which costs the one case it exists for.

const { test } = require("node:test");
const assert = require("node:assert");

const { message } = require("../notify-stale.js");

function tree(overrides) {
  return {
    name: "team",
    path: "/srv/index",
    remote: null,
    present: true,
    databases: [],
    oldestBuilt: null,
    ageHours: null,
    ...overrides,
  };
}

function database(name, built) {
  return { connection: "stage", database: name, engine: "sqlserver", counts: {}, built };
}

test("a tree younger than the threshold says nothing", () => {
  const trees = [tree({ ageHours: 100, oldestBuilt: "2026-03-05T09:00:00Z" })];

  assert.equal(message(trees, 720), null);
});

test("an overdue tree names the database that dates it", () => {
  const trees = [
    tree({
      ageHours: 1000,
      oldestBuilt: "2026-01-02T09:00:00Z",
      databases: [database("AppCore", "2026-03-05T09:00:00Z"), database("Reporting", "2026-01-02T09:00:00Z")],
    }),
  ];

  assert.match(message(trees, 720), /stage\/Reporting was last indexed 41 days ago \(2026-01-02\)/);
});

// A published tree that was never fetched reads as an empty index, which looks
// like a database with nothing in it rather than like a missing clone.
test("a registered remote that was never fetched is reported before any age", () => {
  const trees = [
    tree({ name: "acme", remote: "git@example.internal:acme/index.git", present: false }),
    tree({ name: "local", ageHours: 5000, oldestBuilt: "2025-01-02T09:00:00Z" }),
  ];

  assert.match(message(trees, 720), /the acme index has not been fetched yet/);
});

// A tree nobody ever built is not overdue; it does not exist. Nudging about it
// every session would nag people who only read somebody else's index.
test("a local path with nothing in it is not a stale index", () => {
  const trees = [tree({ present: false })];

  assert.equal(message(trees, 720), null);
});

test("a tree with no build stamp is not reported as old", () => {
  const trees = [tree({ present: true, ageHours: null })];

  assert.equal(message(trees, 720), null);
});
