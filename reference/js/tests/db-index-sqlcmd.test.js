"use strict";
// Unit tests for the db-index read-only gate, argv builder and row parser.

const { test } = require("node:test");
const assert = require("node:assert/strict");
const sqlcmd = require("../sqlcmd.js");
const manifest = require("../manifest.js");

test("assertReadOnly passes the queries the build actually sends", () => {
  assert.equal(sqlcmd.assertReadOnly(manifest.buildQuery()), manifest.buildQuery());
  assert.doesNotThrow(() => sqlcmd.assertReadOnly("WITH t AS (SELECT 1 AS a) SELECT a FROM t"));
});

test("assertReadOnly rejects every write and forbidden verb", () => {
  const writes = [
    "INSERT INTO dbo.T VALUES (1)",
    "UPDATE dbo.T SET a = 1",
    "DELETE FROM dbo.T",
    "MERGE dbo.T AS t USING dbo.S AS s ON 1 = 1",
    "DROP TABLE dbo.T",
    "TRUNCATE TABLE dbo.T",
    "ALTER TABLE dbo.T ADD a int",
    "GRANT SELECT ON dbo.T TO x",
    "BACKUP DATABASE AppCore TO DISK = 'x'",
    "DBCC FREEPROCCACHE",
    "EXEC dbo.SomeProc",
  ];
  for (const sql of writes) assert.throws(() => sqlcmd.assertReadOnly(sql), /refusing to send it/, sql);
});

test("assertReadOnly rejects a write smuggled behind a leading read", () => {
  const bypasses = [
    "SELECT 1; DROP TABLE dbo.T",
    "SELECT * INTO #staging FROM dbo.Orders",
    "SELECT 1 FROM dbo.T; EXEC sp_configure 'show advanced options', 1",
    "SELECT 1 WHERE 1 = 1 UNION SELECT 1; TRUNCATE TABLE dbo.T",
  ];
  for (const sql of bypasses) assert.throws(() => sqlcmd.assertReadOnly(sql), /refusing to send it/, sql);
});

test("assertReadOnly is fail-closed on anything it cannot classify", () => {
  const unreadable = ["/* SELECT 1 */ SELECT 1", "-- comment\nSELECT 1", "", "   ", "DECLARE @a int; SELECT @a"];
  for (const sql of unreadable)
    assert.throws(() => sqlcmd.assertReadOnly(sql), /refusing to send it|must open with SELECT/, JSON.stringify(sql));
});

test("a column name containing a denied word is not a denied statement", () => {
  assert.doesNotThrow(() => sqlcmd.assertReadOnly("SELECT o.create_date, o.modify_date FROM sys.objects o"));
});

test("buildArgs always carries the read-only connection flags", () => {
  const args = sqlcmd.buildArgs({ host: "h", database: "AppCore", query: "SELECT 1" });
  assert.ok(args.includes("-K"));
  assert.equal(args[args.indexOf("-K") + 1], "ReadOnly");
  assert.ok(args.includes("-M"), "multi-subnet failover");
  assert.ok(args.includes("-b"), "exit non-zero on SQL error");
  assert.equal(args[args.indexOf("-d") + 1], "AppCore");
  assert.match(args.at(-1), /^SELECT 1\n/);
});

test("buildArgs refuses to build argv for a write", () => {
  assert.throws(() => sqlcmd.buildArgs({ host: "h", database: "d", query: "DELETE FROM dbo.T" }), /refusing/);
});

test("row mode and raw mode use flag sets sqlcmd accepts together", () => {
  const rows = sqlcmd.buildArgs({ host: "h", database: "d", query: "SELECT 1" });
  assert.ok(rows.includes("-W") && rows.includes("-h") && !rows.includes("-y"));

  const raw = sqlcmd.buildArgs({ host: "h", database: "d", query: "SELECT 1", mode: "raw" });
  assert.ok(raw.includes("-y"), "raw keeps full-width values");
  assert.ok(!raw.includes("-W") && !raw.includes("-h"), "sqlcmd rejects -y 0 with either flag");
});

test("an unknown output mode is refused rather than guessed", () => {
  assert.throws(() => sqlcmd.buildArgs({ host: "h", database: "d", query: "SELECT 1", mode: "csv" }), /unknown/);
});

test("parseRows drops blank lines and the rows-affected footer", () => {
  const rows = sqlcmd.parseRows("dbo\tOrders\tU\r\ndbo\tCustomers\tU\n\n(2 rows affected)\n");
  assert.deepEqual(rows, [
    ["dbo", "Orders", "U"],
    ["dbo", "Customers", "U"],
  ]);
});

test("parseRows keeps a single-row footer out of the data", () => {
  assert.deepEqual(sqlcmd.parseRows("dbo\tT\tU\n\n(1 row affected)\n"), [["dbo", "T", "U"]]);
});

test("run rejects with the database and host when sqlcmd fails", async () => {
  const execFile = (_cmd, _args, _opts, cb) => cb(new Error("boom"), "", "Login failed.");
  await assert.rejects(
    sqlcmd.run({ host: "stagehost", database: "AppCore", query: "SELECT 1" }, { execFile }),
    /AppCore@stagehost: Login failed\./,
  );
});
