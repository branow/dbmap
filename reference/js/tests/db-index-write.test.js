"use strict";
// Unit tests for the index file writer and the state round-trip.

const { test } = require("node:test");
const assert = require("node:assert/strict");
const { mkdtempSync, readFileSync, readdirSync, existsSync } = require("node:fs");
const { tmpdir } = require("node:os");
const { join } = require("node:path");
const write = require("../write.js");

function tmp() {
  return mkdtempSync(join(tmpdir(), "dbmap-index-"));
}

function entry(overrides = {}) {
  return {
    key: "dbo.Orders",
    object: { kind: "table", rows: 5000, kb: 2048, triggers: 1, modified: "2026-01-01T00:00:00" },
    structure: {
      columns: [
        { name: "OrderID", type: "int", length: "", nullable: false, identity: true, computed: false },
        { name: "Status", type: "varchar", length: "(20)", nullable: true, identity: false, computed: false },
      ],
      primaryKey: ["OrderID"],
      indexes: [{ name: "IX_Status", unique: false, columns: ["Status"] }],
    },
    fingerprint: "abc123",
    description: "One row per placed order.",
    ...overrides,
  };
}

const PROC = entry({
  key: "dbo.GetOrder",
  object: { kind: "procedure", rows: 0, kb: 0, triggers: 0, modified: "2026-02-01T00:00:00" },
  structure: {
    parameters: [
      { name: "@OrderID", type: "int", output: false },
      { name: "@Total", type: "money", output: true },
    ],
  },
  description: "Returns one order by id.",
});

test("sizes read in the unit a human would use", () => {
  assert.equal(write.size(16), "16 KB");
  assert.equal(write.size(2048), "2 MB");
  assert.equal(write.size(58072064), "55 GB");
});

test("a catalog is written per kind, and only for kinds present", () => {
  const dir = tmp();
  const result = write.writeIndex(dir, [entry(), PROC]);

  assert.deepEqual(result.catalogs, ["tables.tsv (1)", "procedures.tsv (1)"]);
  assert.ok(!existsSync(join(dir, "views.tsv")), "no views, no views.tsv");
});

test("the table catalog carries size, staleness and description, not columns", () => {
  const dir = tmp();
  write.writeIndex(dir, [entry()]);
  const lines = readFileSync(join(dir, "tables.tsv"), "utf8").trim().split("\n");

  assert.equal(lines[0], "name\trows\tsize\ttriggers\tcols\tmodified\tfingerprint\tdescription");
  assert.equal(lines[1], "dbo.Orders\t5000\t2 MB\t1\t2\t2026-01-01T00:00:00\tabc123\tOne row per placed order.");
});

test("no state file is written beside the catalogs", () => {
  const dir = tmp();
  write.writeIndex(dir, [entry()]);
  assert.ok(!existsSync(join(dir, "state.tsv")), "staleness lives in the catalogs themselves");
});

test("nothing derivable from a stored column is stored beside it", () => {
  assert.ok(!write.header(write.CATALOGS[0]).includes("sampleDepth"), "sample depth is min(rows, 25)");
});

test("procedure parameters render inline with output markers", () => {
  const dir = tmp();
  write.writeIndex(dir, [PROC]);
  const row = readFileSync(join(dir, "procedures.tsv"), "utf8").trim().split("\n")[1];

  assert.equal(
    row,
    "dbo.GetOrder\t@OrderID int, @Total money out\t2026-02-01T00:00:00\tabc123\tReturns one order by id.",
  );
});

test("a function's return type is lifted out of its parameters", () => {
  const fn = entry({
    key: "dbo.BinaryToDecimal",
    object: { kind: "function", rows: 0, kb: 0, triggers: 0, modified: "2026-02-01T00:00:00" },
    structure: {
      parameters: [
        { name: "(returns)", type: "bigint", output: true },
        { name: "@Input", type: "varchar(50)", output: false },
      ],
    },
    description: "Converts a binary string to a number.",
  });

  assert.equal(write.returns(fn), "bigint");
  assert.equal(write.params(fn), "@Input varchar(50)");
});

test("a table-valued function with no return row still reports a table", () => {
  assert.equal(write.returns({ structure: { parameters: [] } }), "table");
});

test("tables and views get a column file, procedures do not", () => {
  const dir = tmp();
  const view = entry({
    key: "dbo.ActiveOrders",
    object: { kind: "view", rows: 0, kb: 0, triggers: 0, modified: "2026-01-01T00:00:00" },
  });
  const result = write.writeIndex(dir, [entry(), view, PROC]);

  assert.equal(result.columnFiles, 2);
  assert.deepEqual(readdirSync(join(dir, "columns")).sort(), ["dbo.ActiveOrders.tsv", "dbo.Orders.tsv"]);
});

test("a column file leads with what the table is and ends with its keys", () => {
  const rendered = write.renderColumns(entry());
  assert.match(rendered, /^# dbo\.Orders @abc123\n/);
  assert.match(rendered, /# rows 5000 {2}size 2 MB {2}triggers 1/);
  assert.match(rendered, /# One row per placed order\./);
  assert.match(rendered, /OrderID\tint\tnot null\tidentity/);
  assert.match(rendered, /Status\tvarchar\(20\)\tnull/);
  assert.match(rendered, /# pk OrderID/);
  assert.match(rendered, /# index IX_Status \(Status\)/);
});

test("a view's column file omits row counts it does not have", () => {
  const view = entry({ object: { kind: "view", rows: 0, kb: 0, triggers: 0, modified: "x" } });
  assert.doesNotMatch(write.renderColumns(view), /# rows/);
});

test("a unique index is marked and a declared foreign key is kept", () => {
  const withFk = entry();
  withFk.structure.foreignKeys = [{ column: "CustomerID", references: "dbo.Customers.CustomerID" }];
  withFk.structure.indexes = [{ name: "IX_Ref", unique: true, columns: ["CustomerID"] }];
  const rendered = write.renderColumns(withFk);

  assert.match(rendered, /# index IX_Ref \(CustomerID\) unique/);
  assert.match(rendered, /# fk CustomerID -> dbo\.Customers\.CustomerID/);
});

test("state survives a write and read round trip through the catalogs", () => {
  const dir = tmp();
  write.writeIndex(dir, [entry(), PROC]);
  const state = write.readState(dir, (path) => (existsSync(path) ? readFileSync(path, "utf8") : null));

  const orders = state.get("dbo.Orders");
  assert.equal(orders.fingerprint, "abc123");
  assert.equal(orders.modified, "2026-01-01T00:00:00");
  assert.equal(orders.description, "One row per placed order.");
  assert.equal(orders.rows, 5000, "row count comes back so growth can be compared");

  const proc = state.get("dbo.GetOrder");
  assert.equal(proc.fingerprint, "abc123");
  assert.equal(proc.rows, 0, "a procedure has no rows to compare");
});

test("parseCatalog ignores the header and blank lines rather than indexing them", () => {
  const catalog = write.CATALOGS[0];
  const state = write.parseCatalog(write.header(catalog).join("\t") + "\n\n# comment\n", catalog);
  assert.equal(state.size, 0);
});

test("a tab or newline in a description cannot break a row", () => {
  const dirty = entry({ description: "One row\tper order\nalways" });
  const dir = tmp();
  write.writeIndex(dir, [dirty]);
  const lines = readFileSync(join(dir, "tables.tsv"), "utf8").trim().split("\n");

  assert.equal(lines.length, 2, "still one header and one row");
  assert.match(lines[1], /One row per order always/);
});

test("no foreign key catalog is written, because the schema has no graph", () => {
  const dir = tmp();
  write.writeIndex(dir, [entry()]);
  assert.ok(!readdirSync(dir).some((f) => /foreign|fk/i.test(f)));
});
