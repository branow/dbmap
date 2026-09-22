"use strict";
// Unit tests for the structure sources, the module body batcher and redaction.

const { test } = require("node:test");
const assert = require("node:assert/strict");
const structure = require("../structure.js");
const modules = require("../modules.js");
const sqlcmd = require("../sqlcmd.js");

test("every structure query is a read and reaches only catalog views", () => {
  for (const source of structure.SOURCES) {
    const sql = source.sql();
    assert.doesNotThrow(() => sqlcmd.assertReadOnly(sql), source.name);
    const tables = [...sql.matchAll(/\b(?:FROM|JOIN)\s+([A-Za-z_][\w.]*)/g)].map((m) => m[1]);
    for (const t of tables) assert.match(t, /^sys\./, `${source.name} reads ${t}`);
  }
});

test("structure queries skip the cdc schema and out-of-scope types", () => {
  const columns = structure.SOURCES.find((s) => s.name === "columns").sql();
  assert.match(columns, /s\.name NOT IN \('cdc'\)/);
  assert.match(columns, /o\.type IN \('U','V','P','PC','FN','IF','TF','FT','SN'\)/);
});

test("column widths read the way a developer writes them", () => {
  assert.equal(structure.widthOf("int", "4", "10", "0"), "");
  assert.equal(structure.widthOf("varchar", "50", "0", "0"), "(50)");
  assert.equal(structure.widthOf("nvarchar", "100", "0", "0"), "(50)");
  assert.equal(structure.widthOf("nvarchar", "-1", "0", "0"), "(max)");
  assert.equal(structure.widthOf("decimal", "9", "18", "2"), "(18,2)");
});

test("index rows fold into one entry per index with the primary key lifted out", () => {
  const folded = structure.foldIndexes([
    { name: "PK_Orders", primary: true, unique: true, column: "OrderID" },
    { name: "IX_Customer", primary: false, unique: false, column: "CustomerID" },
    { name: "IX_Customer", primary: false, unique: false, column: "OrderDate" },
  ]);
  assert.deepEqual(folded.primaryKey, ["OrderID"]);
  assert.deepEqual(folded.indexes, [{ name: "IX_Customer", unique: false, columns: ["CustomerID", "OrderDate"] }]);
});

test("foldIndexes copes with a table that has no indexes at all", () => {
  assert.deepEqual(structure.foldIndexes([]), { primaryKey: [], indexes: [] });
  assert.deepEqual(structure.foldIndexes(), { primaryKey: [], indexes: [] });
});

test("collect groups every source under its owning object", () => {
  const collected = structure.collect([
    { owner: "dbo.Orders", value: { name: "OrderID" }, into: "columns" },
    { owner: "dbo.Orders", value: { name: "Status" }, into: "columns" },
    { owner: "dbo.LegacyOrders", value: "SyncStore.dbo.Orders", into: "target" },
  ]);
  assert.equal(collected.get("dbo.Orders").columns.length, 2);
  assert.equal(collected.get("dbo.LegacyOrders").target, "SyncStore.dbo.Orders");
});

test("module bodies batch at 40 and query only the ids given", () => {
  const ids = Array.from({ length: 95 }, (_, i) => i + 1);
  const batches = modules.batch(ids);
  assert.deepEqual(
    batches.map((b) => b.length),
    [40, 40, 15],
  );
  assert.match(modules.buildQuery([7, 9]), /m\.object_id IN \(7,9\)/);
});

test("the cached body is capped well above what a prompt will use", () => {
  assert.match(modules.buildQuery([1]), new RegExp(`LEFT\\(m\\.definition, ${modules.MAX_DEFINITION_CHARS}\\)`));
  assert.ok(
    modules.MAX_DEFINITION_CHARS > require("../describe-object.js").MAX_BODY_CHARS,
    "the cache keeps the fuller copy",
  );
});

test("the module query is a read and takes the memory grant cap like any other", () => {
  const args = sqlcmd.buildArgs({ host: "h", database: "d", query: modules.buildQuery([1]), mode: "raw" });
  assert.match(args.at(-1), /MAX_GRANT_PERCENT = 1/);
  assert.ok(args.includes("-y"), "raw mode keeps untruncated output");
  assert.ok(!args.includes("-W"), "sqlcmd rejects -y 0 alongside -W");
  assert.ok(!args.includes("-h"), "sqlcmd rejects -y 0 alongside -h");
});

test("bodies split on the sentinel, not on the newlines inside them", () => {
  const raw = [
    "preamble sqlcmd printed",
    "~~OBJ~~101",
    "CREATE PROCEDURE dbo.A AS",
    "\tSELECT 1",
    "",
    "~~OBJ~~102",
    "CREATE VIEW dbo.B AS SELECT 2",
    "",
    "(2 rows affected)",
  ].join("\n");

  const bodies = modules.parseBodies(raw);
  assert.match(bodies[101], /^CREATE PROCEDURE dbo\.A AS\n\tSELECT 1/);
  assert.match(bodies[102], /^CREATE VIEW dbo\.B AS SELECT 2/);
  assert.doesNotMatch(bodies[102], /rows affected/);
});

test("parseBodies ignores output with no sentinel rather than inventing one", () => {
  assert.deepEqual(modules.parseBodies("Msg 8134, Level 16"), {});
  assert.deepEqual(modules.parseBodies(""), {});
});

test("redaction strips a credential value and keeps the key", () => {
  const { text, hits } = modules.redact("EXEC dbo.Connect @conn = 'Data Source=srv;Password=hunter2;'");
  assert.doesNotMatch(text, /hunter2/);
  assert.doesNotMatch(text, /srv/);
  assert.match(text, /Password\s*=\s*<redacted>/i);
  assert.equal(hits.password, 1);
  assert.equal(hits["connection-string"], 1);
});

test("redaction strips api keys and bearer tokens", () => {
  const { text, hits } = modules.redact("SET @h = 'Bearer eyJhbGciOiJIUzI1NiJ9abc', @api_key = 'sk-live-99999'");
  assert.doesNotMatch(text, /eyJhbGciOiJIUzI1NiJ9abc/);
  assert.doesNotMatch(text, /sk-live-99999/);
  assert.equal(hits["bearer-token"], 1);
  assert.equal(hits["api-key"], 1);
});

test("redaction replaces mail recipients, the one thing stage procedures do carry", () => {
  const { text, hits } = modules.redact("@recipients = 'a.b@example.com,c.d@example.com'");
  assert.doesNotMatch(text, /@example\.com/);
  assert.equal(hits.email, 2);
  assert.match(text, /<email>,<email>/);
});

test("redaction leaves ordinary T-SQL alone", () => {
  const sql = "WHERE SourceIsHistoricalTables = 1 AND cs.Source = 'D2A' AND o.CustomerID = @CustomerID";
  const { text, hits } = modules.redact(sql);
  assert.equal(text, sql);
  assert.deepEqual(hits, {});
});

const { HEALTH_QUERY } = require("../health.js");
const HEALTHY = [["12", "Available physical memory is high", "0", "0"]];
const TARGET = { host: "h", physical: "d", logical: "d" };

// Answers the health probe with room to spare and every other query with `body`.
function runner(body, onQuery = () => {}) {
  return async (opts) => {
    if (opts.query === HEALTH_QUERY) return HEALTHY;
    onQuery(opts);
    return body;
  };
}

test("fetchModules redacts before returning and reports what it stripped", async () => {
  const objects = [{ schema: "dbo", name: "Mailer", kind: "procedure", objectId: 101 }];
  const run = runner("~~OBJ~~101\nEXEC sp_send_dbmail @recipients = 'ops@example.com'\n");
  const result = await modules.fetchModules(TARGET, objects, { run });

  assert.doesNotMatch(result.definitions["dbo.Mailer"], /ops@example\.com/);
  assert.match(result.definitions["dbo.Mailer"], /<email>/);
  assert.equal(result.redactions.email, 1);
});

test("fetchModules halts rather than fetching when memory state cannot be read", async () => {
  const objects = [{ schema: "dbo", name: "Mailer", kind: "procedure", objectId: 101 }];
  const run = async () => {
    throw new Error("permission denied");
  };
  await assert.rejects(modules.fetchModules(TARGET, objects, { run }), /halting before modules batch 1\/1/);
});

test("fetchModules skips objects that have no body to fetch", async () => {
  const objects = [{ schema: "dbo", name: "Orders", kind: "table", objectId: 1 }];
  let queried = false;
  const run = runner("", () => {
    queried = true;
  });

  const result = await modules.fetchModules(TARGET, objects, { run });
  assert.deepEqual(result.definitions, {});
  assert.equal(queried, false, "a table never reaches sys.sql_modules");
});
