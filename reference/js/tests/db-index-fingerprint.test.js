"use strict";
// Unit tests for the staleness fingerprints and the two-tier planner.

const { test } = require("node:test");
const assert = require("node:assert/strict");
const fp = require("../fingerprint.js");
const plan = require("../plan.js");

const TABLE = {
  rows: 5000,
  triggers: 0,
  columns: [
    { name: "OrderID", type: "int", nullable: false, identity: true },
    { name: "Status", type: "varchar", length: "(20)", nullable: true },
  ],
  primaryKey: ["OrderID"],
  foreignKeys: [{ column: "CustomerID", references: "dbo.Customers.CustomerID" }],
  indexes: [{ name: "IX_Status", columns: ["Status"], unique: false }],
};

test("sample depth saturates at the number of rows the describer sees", () => {
  assert.equal(fp.SAMPLE_ROWS, 25);
  assert.equal(fp.sampleDepth(0), 0);
  assert.equal(fp.sampleDepth(8), 8);
  assert.equal(fp.sampleDepth(9), 9);
  assert.equal(fp.sampleDepth(25), 25);
  assert.equal(fp.sampleDepth(26), 25);
  assert.equal(fp.sampleDepth(616802364), 25);
});

test("an enum gaining a value changes its fingerprint", () => {
  assert.notEqual(fp.tableFingerprint({ ...TABLE, rows: 8 }), fp.tableFingerprint({ ...TABLE, rows: 9 }));
});

test("a table filling up from empty changes its fingerprint", () => {
  assert.notEqual(fp.tableFingerprint({ ...TABLE, rows: 0 }), fp.tableFingerprint({ ...TABLE, rows: 100 }));
});

test("growth above the sample size keeps a table fingerprint", () => {
  const forty = fp.tableFingerprint({ ...TABLE, rows: 40 });
  assert.equal(forty, fp.tableFingerprint({ ...TABLE, rows: 60 }));
  assert.equal(forty, fp.tableFingerprint({ ...TABLE, rows: 616802364 }));
});

test("a table shrinking back below the sample size changes its fingerprint", () => {
  assert.notEqual(fp.tableFingerprint({ ...TABLE, rows: 40 }), fp.tableFingerprint({ ...TABLE, rows: 10 }));
});

test("adding a column changes a table fingerprint", () => {
  const grown = {
    ...TABLE,
    columns: [...TABLE.columns, { name: "CreatedOn", type: "datetime", nullable: false }],
  };
  assert.notEqual(fp.tableFingerprint(TABLE), fp.tableFingerprint(grown));
});

test("making a column nullable changes a table fingerprint", () => {
  const relaxed = {
    ...TABLE,
    columns: [TABLE.columns[0], { ...TABLE.columns[1], nullable: false }],
  };
  assert.notEqual(fp.tableFingerprint(TABLE), fp.tableFingerprint(relaxed));
});

test("a new foreign key or index changes a table fingerprint", () => {
  assert.notEqual(fp.tableFingerprint(TABLE), fp.tableFingerprint({ ...TABLE, foreignKeys: [] }));
  assert.notEqual(fp.tableFingerprint(TABLE), fp.tableFingerprint({ ...TABLE, indexes: [] }));
});

test("a redeployed but byte-identical procedure keeps its fingerprint", () => {
  const body = "CREATE PROCEDURE dbo.GetOrder @id int AS\nSELECT * FROM dbo.Orders WHERE OrderID = @id";
  assert.equal(fp.moduleFingerprint(body), fp.moduleFingerprint(body));
});

test("line endings and trailing whitespace do not change a module fingerprint", () => {
  const unix = "CREATE PROCEDURE dbo.GetOrder AS\nSELECT 1\n";
  const windows = "CREATE PROCEDURE dbo.GetOrder AS   \r\nSELECT 1\r\n";
  assert.equal(fp.moduleFingerprint(unix), fp.moduleFingerprint(windows));
});

test("a real edit changes a module fingerprint", () => {
  const before = "CREATE PROCEDURE dbo.GetOrder AS SELECT 1";
  const after = "CREATE PROCEDURE dbo.GetOrder AS SELECT 2";
  assert.notEqual(fp.moduleFingerprint(before), fp.moduleFingerprint(after));
});

test("fingerprint refuses a kind it has no rule for", () => {
  assert.throws(() => fp.fingerprint("trigger", {}), /no fingerprint defined/);
});

function state(entries) {
  return new Map(Object.entries(entries));
}

test("planFetch pulls new objects and leaves untouched ones alone", () => {
  const objects = [
    { schema: "dbo", name: "Orders", kind: "table", modified: "2026-01-01T00:00:00", rows: 5000 },
    { schema: "dbo", name: "Customers", kind: "table", modified: "2026-01-01T00:00:00", rows: 5000 },
  ];
  const prior = state({
    "dbo.Orders": { modified: "2026-01-01T00:00:00", fingerprint: "x", rows: 5000 },
  });

  const result = plan.planFetch(objects, prior);
  assert.deepEqual(
    result.fetch.map((o) => o.name),
    ["Customers"],
  );
  assert.deepEqual(
    result.reuse.map((o) => o.name),
    ["Orders"],
  );
  assert.equal(result.reasons["dbo.Customers"], "new");
});

test("planFetch pulls an object whose modify_date moved", () => {
  const objects = [{ schema: "dbo", name: "GetOrder", kind: "procedure", modified: "2026-02-14T09:00:00", rows: 0 }];
  const prior = state({
    "dbo.GetOrder": { modified: "2024-07-25T09:00:00", fingerprint: "x", rows: 0 },
  });
  assert.equal(plan.planFetch(objects, prior).reasons["dbo.GetOrder"], "modified");
});

test("planFetch pulls a table that filled up without any DDL", () => {
  const objects = [{ schema: "dbo", name: "Orders", kind: "table", modified: "2026-01-01T00:00:00", rows: 50000 }];
  const prior = state({
    "dbo.Orders": { modified: "2026-01-01T00:00:00", fingerprint: "x", rows: 0 },
  });
  assert.equal(plan.planFetch(objects, prior).reasons["dbo.Orders"], "sample");
});

test("planFetch pulls an eight-row enum that gained a ninth value", () => {
  const objects = [{ schema: "dbo", name: "OrderType", kind: "table", modified: "2026-01-01T00:00:00", rows: 9 }];
  const prior = state({
    "dbo.OrderType": { modified: "2026-01-01T00:00:00", fingerprint: "x", rows: 8 },
  });
  assert.equal(plan.planFetch(objects, prior).reasons["dbo.OrderType"], "sample");
});

test("planFetch ignores growth above the sample size", () => {
  const prior = state({
    "dbo.Orders": { modified: "2026-01-01T00:00:00", fingerprint: "x", rows: 5000 },
  });
  for (const rows of [40, 200, 616902364]) {
    const objects = [{ schema: "dbo", name: "Orders", kind: "table", modified: "2026-01-01T00:00:00", rows }];
    assert.deepEqual(plan.planFetch(objects, prior).fetch, [], String(rows));
  }
});

test("planFetch pulls a table that shrank back below the sample size", () => {
  const objects = [{ schema: "dbo", name: "Orders", kind: "table", modified: "2026-01-01T00:00:00", rows: 10 }];
  const prior = state({
    "dbo.Orders": { modified: "2026-01-01T00:00:00", fingerprint: "x", rows: 5000 },
  });
  assert.equal(plan.planFetch(objects, prior).reasons["dbo.Orders"], "sample");
});

test("a state entry with no recorded row count refetches once", () => {
  const objects = [{ schema: "dbo", name: "Orders", kind: "table", modified: "2026-01-01T00:00:00", rows: 200 }];
  const prior = state({ "dbo.Orders": { modified: "2026-01-01T00:00:00", fingerprint: "x" } });
  assert.equal(plan.planFetch(objects, prior).reasons["dbo.Orders"], "sample");
});

test("planFetch reports objects dropped from the database", () => {
  const objects = [{ schema: "dbo", name: "Orders", kind: "table", modified: "2026-01-01T00:00:00", rows: 5 }];
  const prior = state({
    "dbo.Orders": { modified: "2026-01-01T00:00:00", fingerprint: "x", rows: 5 },
    "dbo.RetiredProc": { modified: "2020-01-01T00:00:00", fingerprint: "y", rows: 0 },
  });
  assert.deepEqual(plan.planFetch(objects, prior).dropped, ["dbo.RetiredProc"]);
});

test("planDescribe spends nothing on a release that redeployed identical procedures", () => {
  const body = "CREATE PROCEDURE dbo.GetOrder AS SELECT 1";
  const object = { schema: "dbo", name: "GetOrder", kind: "procedure", modified: "2026-02-14T09:00:00" };
  const prior = state({
    "dbo.GetOrder": {
      modified: "2024-07-25T09:00:00",
      fingerprint: fp.moduleFingerprint(body),
      rows: 0,
      description: "Returns one order by id.",
    },
  });

  const result = plan.planDescribe([{ object, payload: { definition: body } }], prior);
  assert.equal(result.describe.length, 0);
  assert.equal(result.unchanged.length, 1);
});

test("planDescribe sends a procedure whose body actually changed", () => {
  const object = { schema: "dbo", name: "GetOrder", kind: "procedure", modified: "2026-02-14T09:00:00" };
  const prior = state({
    "dbo.GetOrder": {
      modified: "2024-07-25T09:00:00",
      fingerprint: fp.moduleFingerprint("CREATE PROCEDURE dbo.GetOrder AS SELECT 1"),
      rows: 0,
      description: "Returns one order by id.",
    },
  });

  const result = plan.planDescribe(
    [{ object, payload: { definition: "CREATE PROCEDURE dbo.GetOrder AS SELECT 2" } }],
    prior,
  );
  assert.equal(result.describe.length, 1);
});

test("planDescribe redescribes a matching fingerprint that never got a description", () => {
  const body = "CREATE PROCEDURE dbo.GetOrder AS SELECT 1";
  const object = { schema: "dbo", name: "GetOrder", kind: "procedure", modified: "2026-02-14T09:00:00" };
  const prior = state({
    "dbo.GetOrder": { modified: "2026-02-14T09:00:00", fingerprint: fp.moduleFingerprint(body), rows: 0 },
  });
  assert.equal(plan.planDescribe([{ object, payload: { definition: body } }], prior).describe.length, 1);
});

test("describable drops objects scope marked as not worth a sentence", () => {
  const entries = [
    { object: { schema: "dbo", name: "Orders", kind: "table", describe: true } },
    { object: { schema: "dbo", name: "LegacyOrders", kind: "synonym", describe: false } },
  ];
  assert.deepEqual(
    plan.describable(entries).map((e) => e.object.name),
    ["Orders"],
  );
});

test("the sample rule is table-only, not a no-op that happens never to fire", () => {
  const rule = plan.FETCH_REASONS.find((r) => r.reason === "sample");
  assert.deepEqual(rule.kinds, ["table"]);

  const proc = { schema: "dbo", name: "GetOrder", kind: "procedure", modified: "2026-01-01T00:00:00", rows: 900 };
  const prior = state({ "dbo.GetOrder": { modified: "2026-01-01T00:00:00", fingerprint: "x", rows: 0 } });
  assert.deepEqual(plan.planFetch([proc], prior).fetch, [], "a row count cannot make a procedure stale");
});
