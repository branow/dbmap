"use strict";
// Unit tests for the memory headroom gate and the per-query grant cap.

const { test } = require("node:test");
const assert = require("node:assert/strict");
const health = require("../health.js");
const sqlcmd = require("../sqlcmd.js");

const TARGET = { host: "stage-sqlag.example.internal", physical: "AppCore" };
const HEALTHY = ["12", "Available physical memory is high", "0", "0"];

function reading(overrides = []) {
  return [HEALTHY.map((cell, i) => overrides[i] ?? cell)];
}

test("a server with room reports healthy", () => {
  const result = health.classify(health.parseHealth(reading()));
  assert.equal(result.ok, true);
  assert.equal(result.known, true);
});

test("the build stops when OS memory falls under the floor", () => {
  const result = health.classify(health.parseHealth(reading(["1"])));
  assert.equal(result.ok, false);
  assert.match(result.reason, /1 GB of OS memory free \(floor is 2 GB\)/);
});

test("the build stops when the server itself reports low memory", () => {
  const result = health.classify(health.parseHealth(reading(["12", "Available physical memory is low"])));
  assert.equal(result.ok, false);
  assert.match(result.reason, /Available physical memory is low/);
});

test("the build stops when SQL Server signals physical memory low", () => {
  const result = health.classify(health.parseHealth(reading(["12", "Available physical memory is high", "0", "1"])));
  assert.equal(result.ok, false);
  assert.match(result.reason, /signalled physical memory low/);
});

test("the build stops when queries are queueing for memory grants", () => {
  const result = health.classify(health.parseHealth(reading(["12", "Available physical memory is high", "9"])));
  assert.equal(result.ok, false);
  assert.match(result.reason, /9 queries are queued/);
});

test("4 GB free, the real stage reading, still clears the floor", () => {
  assert.equal(health.classify(health.parseHealth(reading(["4"]))).ok, true);
});

test("an unreadable memory state is unknown, never healthy", () => {
  const result = health.classify(null);
  assert.equal(result.ok, false);
  assert.equal(result.known, false);
  assert.match(result.reason, /could not be read/);
});

test("checkHealth reports unknown rather than throwing when the DMVs are denied", async () => {
  const run = async () => {
    throw new Error("The user does not have permission to perform this action.");
  };
  const result = await health.checkHealth(TARGET, { run });
  assert.equal(result.ok, false);
  assert.equal(result.known, false);
});

test("assertHealthy names the stage and the server it stopped before", async () => {
  const run = async () => reading(["1"]);
  await assert.rejects(
    health.assertHealthy(TARGET, "sampling dbo.Orders", { run }),
    /halting before sampling dbo\.Orders on stage-sqlag\.example\.internal — only 1 GB/,
  );
});

test("assertHealthy returns the reading when the server has room", async () => {
  const run = async () => reading();
  const result = await health.assertHealthy(TARGET, "structure", { run });
  assert.equal(result.availableGb, 12);
});

test("the health query itself takes no memory grant and touches no user table", () => {
  assert.doesNotThrow(() => sqlcmd.assertReadOnly(health.HEALTH_QUERY));
  assert.doesNotMatch(health.HEALTH_QUERY, /ORDER BY|GROUP BY|DISTINCT/i);
});

test("every query the build sends carries the memory grant cap", () => {
  const args = sqlcmd.buildArgs({ host: "h", database: "d", query: "SELECT 1" });
  assert.match(args.at(-1), /OPTION \(MAXDOP 1, MAX_GRANT_PERCENT = 1\)/);
});

test("the guard replaces a trailing semicolon rather than producing invalid SQL", () => {
  assert.equal(sqlcmd.withGuard("SELECT 1;"), "SELECT 1\nOPTION (MAXDOP 1, MAX_GRANT_PERCENT = 1)");
  assert.equal(sqlcmd.withGuard("SELECT 1"), "SELECT 1\nOPTION (MAXDOP 1, MAX_GRANT_PERCENT = 1)");
});

test("the guard is applied after the read-only check, never instead of it", () => {
  assert.throws(() => sqlcmd.buildArgs({ host: "h", database: "d", query: "DROP TABLE dbo.T" }), /refusing/);
});
