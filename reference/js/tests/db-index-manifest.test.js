"use strict";
// Unit tests for the db-index target resolution, scope rules and manifest parse.

const { test } = require("node:test");
const assert = require("node:assert/strict");
const { resolveTargets } = require("../targets.js");
const scope = require("../scope.js");
const manifest = require("../manifest.js");

test("stage resolves all three databases to their physical names", () => {
  const targets = resolveTargets("stage");
  assert.deepEqual(
    targets.map((t) => `${t.logical}=${t.physical}`),
    ["AppCore=AppCore", "SyncStore=SyncStore", "SideStore=SideStore"],
  );
  assert.ok(targets.every((t) => t.host === "stage-sqlag.example.internal"));
});

test("dev and test use their renamed physical databases", () => {
  const dev = Object.fromEntries(resolveTargets("dev").map((t) => [t.logical, t.physical]));
  assert.deepEqual(dev, { AppCore: "AppCore1", SyncStore: "Mirror1", SideStore: "SideStore1" });

  const testEnv = Object.fromEntries(resolveTargets("test").map((t) => [t.logical, t.physical]));
  assert.deepEqual(testEnv, { AppCore: "AppCore7", SyncStore: "Mirror7", SideStore: "SideStore7" });
});

test("production and the read-only replicas of it cannot be resolved", () => {
  for (const env of ["prod", "report", "monitoring", "", "PROD"])
    assert.throws(() => resolveTargets(env), /refusing environment/, env);
});

test("an unknown database name is refused rather than silently skipped", () => {
  assert.throws(() => resolveTargets("stage", "Reporting"), /unknown database/);
});

test("the cdc schema is excluded from the manifest query", () => {
  const sql = manifest.buildQuery();
  assert.match(sql, /s\.name NOT IN \('cdc'\)/);
});

test("the manifest query never scans a table for its row count", () => {
  const sql = manifest.buildQuery();
  assert.match(sql, /sys\.dm_db_partition_stats/);
  assert.doesNotMatch(sql, /COUNT\(\*\)\s+FROM\s+(?!sys\.)/i);
  assert.doesNotMatch(sql, /sp_spaceused/i);
});

test("triggers are counted on the parent table, not listed as objects", () => {
  assert.ok(!scope.typeCodes().includes("TR"));
  assert.match(manifest.buildQuery(), /sys\.triggers t WHERE t\.parent_id = o\.object_id/);
});

test("parseManifest maps type codes to kinds and coerces the numbers", () => {
  const rows = [
    ["dbo", "Orders", "U", "2026-09-03T20:57:01", "421022746", "58072064", "2", "101"],
    ["dbo", "GetOrder", "P", "2026-01-02T10:00:00", "0", "0", "0", "102"],
    ["dbo", "ActiveOrders", "V", "2026-01-02T10:00:00", "0", "0", "0", "103"],
    ["dbo", "SplitList", "IF", "2026-01-02T10:00:00", "0", "0", "0", "104"],
    ["dbo", "LegacyOrders", "SN", "2026-01-02T10:00:00", "0", "0", "0", "105"],
  ];
  const objects = manifest.parseManifest(rows);
  assert.deepEqual(
    objects.map((o) => o.kind),
    ["table", "procedure", "view", "function", "synonym"],
  );
  assert.equal(objects[0].rows, 421022746);
  assert.equal(objects[0].triggers, 2);
  assert.equal(objects[0].objectId, 101);
  assert.equal(objects[0].describe, true);
  assert.equal(objects[4].describe, false);
});

test("parseManifest drops rows whose type is outside scope", () => {
  const rows = [
    ["dbo", "SomeTrigger", "TR", "2026-01-02T10:00:00", "0", "0", "0", "106"],
    ["dbo", "PK_Orders", "PK", "2026-01-02T10:00:00", "0", "0", "0", "107"],
    ["dbo", "Orders", "U", "2026-01-02T10:00:00", "5", "8", "0", "108"],
  ];
  assert.deepEqual(
    manifest.parseManifest(rows).map((o) => o.name),
    ["Orders"],
  );
});

test("parseManifest drops a truncated row rather than shifting its cells", () => {
  assert.deepEqual(manifest.parseManifest([["dbo", "Orders", "U"]]), []);
});

test("fetchManifest labels the result with the logical and physical database", async () => {
  const run = async () => [["dbo", "Orders", "U", "2026-01-02T10:00:00", "5", "8", "0", "108"]];
  const result = await manifest.fetchManifest(
    { env: "stage", host: "h", logical: "AppCore", physical: "AppCore" },
    { run },
  );
  assert.equal(result.database, "AppCore");
  assert.equal(result.physical, "AppCore");
  assert.equal(result.env, "stage");
  assert.deepEqual(manifest.summarize(result), { database: "AppCore", total: 1, counts: { table: 1 } });
});
