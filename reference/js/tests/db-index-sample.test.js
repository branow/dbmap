"use strict";
// Unit tests for the sampler and the describe prompts.

const { test } = require("node:test");
const assert = require("node:assert/strict");
const sample = require("../sample.js");
const describe = require("../describe-object.js");
const { HEALTH_QUERY } = require("../health.js");

const HEALTHY = [["12", "Available physical memory is high", "0", "0"]];
const TARGET = { host: "h", physical: "d", logical: "d" };

const COLUMNS = [
  { name: "OrderID", type: "int", length: "" },
  { name: "CustomerEmail", type: "varchar", length: "(100)" },
  { name: "ShipAddress1", type: "nvarchar", length: "(100)" },
  { name: "Notes", type: "nvarchar", length: "(max)" },
  { name: "Payload", type: "xml", length: "" },
  { name: "StatusName", type: "varchar", length: "(20)" },
];

test("columns naming a person's data are recognised", () => {
  for (const name of [
    "Email",
    "CustomerEmail",
    "HomePhone",
    "SSN",
    "DateOfBirth",
    "Address1",
    "PostalCode",
    "CardNumber",
  ])
    assert.equal(sample.isPii(name), true, name);
});

test("an ordinary column is not mistaken for personal data", () => {
  for (const name of ["OrderID", "StatusName", "CreatedOn", "Total", "ItemCode"])
    assert.equal(sample.isPii(name), false, name);
});

test("blobs and (max) columns are not sampleable", () => {
  assert.equal(sample.isSampleable({ type: "xml", length: "" }), false);
  assert.equal(sample.isSampleable({ type: "varbinary", length: "(50)" }), false);
  assert.equal(sample.isSampleable({ type: "nvarchar", length: "(max)" }), false);
  assert.equal(sample.isSampleable({ type: "varchar", length: "(20)" }), true);
});

test("the projection keeps safe columns and records why the rest were dropped", () => {
  const { selected, withheld } = sample.planProjection(COLUMNS);
  assert.deepEqual(selected, ["OrderID", "StatusName"]);
  assert.deepEqual(withheld, [
    { name: "CustomerEmail", reason: "pii" },
    { name: "ShipAddress1", reason: "pii" },
    { name: "Notes", reason: "unsampleable" },
    { name: "Payload", reason: "unsampleable" },
  ]);
});

test("the sample query takes the top rows without ordering them", () => {
  const sql = sample.buildQuery("dbo.Orders", ["OrderID", "StatusName"]);
  assert.match(sql, /SELECT TOP \(25\)/);
  assert.doesNotMatch(sql, /ORDER BY/i, "an ORDER BY would rank the whole table");
  assert.match(sql, /FROM \[dbo\]\.\[Orders\]/);
});

test("every sampled cell is capped so one wide column cannot dominate", () => {
  const sql = sample.buildQuery("dbo.Orders", ["Notes"]);
  assert.match(sql, /LEFT\(CAST\(\[Notes\] AS nvarchar\(200\)\), 200\)/);
});

test("a table of nothing but personal data is never queried", async () => {
  let queried = false;
  const run = async (opts) => {
    if (opts.query === HEALTH_QUERY) return HEALTHY;
    queried = true;
    return [];
  };
  const entry = {
    key: "dbo.People",
    object: { rows: 10 },
    structure: { columns: [{ name: "Email", type: "varchar" }] },
  };

  assert.equal(await sample.sampleTable(TARGET, entry, { run }), null);
  assert.equal(queried, false);
});

test("a sample knows whether it is the whole table", async () => {
  const run = async (opts) => (opts.query === HEALTH_QUERY ? HEALTHY : [["1", "Pending"]]);
  const structure = {
    columns: [
      { name: "Id", type: "int" },
      { name: "Name", type: "varchar", length: "(20)" },
    ],
  };

  const small = await sample.sampleTable(TARGET, { key: "dbo.S", object: { rows: 11 }, structure }, { run });
  assert.equal(small.complete, true);

  const big = await sample.sampleTable(TARGET, { key: "dbo.B", object: { rows: 616802364 }, structure }, { run });
  assert.equal(big.complete, false);
});

test("complete tables are sampled before partial ones when a run is capped", () => {
  const entries = [
    { key: "dbo.Huge", object: { kind: "table", rows: 616802364 } },
    { key: "dbo.Lookup", object: { kind: "table", rows: 11 } },
    { key: "dbo.Empty", object: { kind: "table", rows: 0 } },
    { key: "dbo.Proc", object: { kind: "procedure", rows: 0 } },
  ];
  assert.deepEqual(
    sample.order(entries).map((e) => e.key),
    ["dbo.Lookup", "dbo.Huge"],
  );
});

test("a rendered sample names the columns it is withholding", () => {
  const rendered = sample.renderSample({
    key: "dbo.Orders",
    columns: ["OrderID"],
    withheld: [
      { name: "CustomerEmail", reason: "pii" },
      { name: "Notes", reason: "unsampleable" },
    ],
    rows: [["1"]],
    complete: false,
  });
  assert.match(rendered, /first 25 rows/);
  assert.match(rendered, /withheld as personal data: CustomerEmail/);
  assert.doesNotMatch(rendered, /Notes/, "an unsampleable column is not a privacy note");
});

test("the schema field is named for the answer, not for a description of it", () => {
  assert.deepEqual(describe.SINGLE_SCHEMA.required, ["sentence"]);
  assert.ok(!describe.SINGLE_SCHEMA.properties.description, "a field called description gets filled with one");
  assert.match(describe.SINGLE_SCHEMA.properties.sentence.description, /Never describe what the sentence would say/);

  const item = describe.BATCH_SCHEMA.properties.objects.items;
  assert.deepEqual(item.required, ["name", "sentence"]);
  assert.ok(!item.properties.description, "the batch schema must not reintroduce the trap");
});

test("batches fill to a character budget, not a fixed count", () => {
  const prompts = [
    { key: "a", prompt: "x".repeat(9000) },
    { key: "b", prompt: "x".repeat(9000) },
    { key: "c", prompt: "x".repeat(9000) },
  ];
  const batches = describe.batchByCost(prompts, { chars: 20000, max: 12 });
  assert.deepEqual(
    batches.map((b) => b.map((i) => i.key)),
    [["a", "b"], ["c"]],
  );
});

test("an object larger than the whole budget still goes out, alone", () => {
  const prompts = [
    { key: "huge", prompt: "x".repeat(50000) },
    { key: "small", prompt: "x" },
  ];
  const batches = describe.batchByCost(prompts, { chars: 1000, max: 12 });
  assert.deepEqual(
    batches.map((b) => b.map((i) => i.key)),
    [["huge"], ["small"]],
  );
});

test("a batch never exceeds the object cap even when every object is tiny", () => {
  const prompts = Array.from({ length: 30 }, (_, i) => ({ key: String(i), prompt: "x" }));
  const batches = describe.batchByCost(prompts, { chars: 100000, max: 12 });
  assert.deepEqual(
    batches.map((b) => b.length),
    [12, 12, 6],
  );
});

test("a batch prompt labels each object so answers can be matched back", () => {
  const prompt = describe.buildBatchPrompt([
    { key: "dbo.A", prompt: "first" },
    { key: "dbo.B", prompt: "second" },
  ]);
  assert.match(prompt, /=== OBJECT 1: dbo\.A ===/);
  assert.match(prompt, /=== OBJECT 2: dbo\.B ===/);
  assert.match(prompt, /name` exactly as given/);
});

test("describeObject reads the sentence the model wrote", async () => {
  const entry = { key: "dbo.Orders", object: { kind: "table", rows: 5 }, structure: { columns: [] } };
  const invoke = async () => ({ sentence: "  Holds one row per placed order.  " });
  const result = await describe.describeObject(entry, { model: "m", invoke, context: {} });
  assert.equal(result, "Holds one row per placed order.");
});

test("a table prompt carries its columns, its key and its sample", () => {
  const entry = {
    key: "dbo.Orders",
    object: { kind: "table", rows: 5000 },
    structure: { columns: [{ name: "OrderID", type: "int", length: "", nullable: false }], primaryKey: ["OrderID"] },
  };
  const prompt = describe.buildPrompt(entry, {
    database: "AppCore",
    sample: { key: "dbo.Orders", columns: ["OrderID"], withheld: [], rows: [["1"]], complete: false },
  });

  assert.match(prompt, /table dbo\.Orders in the AppCore database/);
  assert.match(prompt, /OrderID int not null/);
  assert.match(prompt, /Primary key: OrderID/);
  assert.match(prompt, /Sample rows:/);
});

test("a procedure prompt carries its body and forbids restating the instruction", () => {
  const entry = { key: "dbo.GetOrder", object: { kind: "procedure" }, structure: { parameters: [] } };
  const prompt = describe.buildPrompt(entry, { database: "AppCore", definition: "SELECT 1" });

  assert.match(prompt, /No parameters\./);
  assert.match(prompt, /SELECT 1/);
  assert.match(prompt, /Do not restate this instruction/);
});

test("a CLR object with no body says so rather than prompting on emptiness", () => {
  assert.match(describe.body(""), /no body available/);
});

test("an oversized body is truncated for the prompt and marked", () => {
  const long = "A".repeat(describe.MAX_BODY_CHARS + 500);
  const trimmed = describe.body(long);
  assert.ok(trimmed.length < long.length);
  assert.match(trimmed, /-- truncated$/);
});

test("buildPrompt refuses a kind it has no prompt for", () => {
  assert.throws(() => describe.buildPrompt({ object: { kind: "synonym" } }, {}), /no prompt defined/);
});

function tableEntry(name) {
  return {
    key: `dbo.${name}`,
    object: { kind: "table", schema: "dbo", name, rows: 1 },
    structure: { columns: [] },
  };
}

test("descriptions are matched back to objects by name, not by position", async () => {
  const invoke = async () => ({
    objects: [
      { name: "dbo.B", sentence: "Holds B rows." },
      { name: "dbo.A", sentence: "Holds A rows." },
    ],
  });
  const result = await describe.describeAll([tableEntry("A"), tableEntry("B")], { model: "m", invoke, cache: {} });
  assert.deepEqual(result, { "dbo.A": "Holds A rows.", "dbo.B": "Holds B rows." });
});

test("one failing batch does not lose the rest of the run", async () => {
  const entries = [tableEntry("A"), tableEntry("B")];
  const invoke = async (prompt) => {
    if (prompt.includes("dbo.A")) throw new Error("model unavailable");
    return { objects: [{ name: "dbo.B", sentence: "Holds B rows." }] };
  };

  const result = await describe.describeAll(entries, { model: "m", invoke, cache: {}, chars: 1, max: 1 });
  assert.deepEqual(result, { "dbo.B": "Holds B rows." });
});

test("an object the model skipped is reported, not silently blank", async () => {
  const warnings = [];
  const invoke = async () => ({ objects: [{ name: "dbo.A", sentence: "Holds A rows." }] });
  await describe.describeAll([tableEntry("A"), tableEntry("B")], {
    model: "m",
    invoke,
    cache: {},
    logger: { warn: (m) => warnings.push(m), info: () => {} },
  });
  assert.ok(warnings.some((w) => w.includes("dbo.B")));
});
