"use strict";
// Sample rows: the first N rows of a table, for the describer only.
//
// This is the one stage that reads user data, so it is the one with rules:
//
//   TOP N and no ORDER BY   a sort would rank the whole table; without one the
//                           engine stops after N rows however large the table
//                           is. This is why a 616M-row, 42 GB table is as cheap
//                           to sample as an 11-row lookup table.
//   explicit projection     SELECT * drags blobs and (max) columns along. Only
//                           columns worth reading are named.
//   every cell capped       LEFT(CAST(c AS nvarchar(200)), 200), so one wide
//                           column cannot decide the size of the result.
//   PII columns omitted     a column whose name says it holds a person's data
//                           is never selected, so those values do not reach the
//                           cache, a prompt, or the index.
//
// For a table under N rows the sample IS the whole table, which is how the
// lookup tables that hold the value domains — OrderStatuses, CustomerTypes,
// DeclineReasons — get captured without a DISTINCT scan anywhere.

const { SAMPLE_ROWS } = require("./fingerprint.js");
const sqlcmd = require("./sqlcmd.js");
const health = require("./health.js");

const CELL_CHARS = 200;

// Types that cannot be sampled usefully: blobs, documents, spatial and the
// (max) variants, which are unbounded by definition.
const UNSAMPLED_TYPES = [
  "text",
  "ntext",
  "image",
  "xml",
  "varbinary",
  "binary",
  "geography",
  "geometry",
  "hierarchyid",
  "sql_variant",
  "timestamp",
  "rowversion",
];

// A column matching any of these holds data about a person. Matched on the
// column NAME, because a type says nothing about who a value belongs to.
const PII_COLUMNS = [
  /e?mail/i,
  /phone|mobile|fax/i,
  /ssn|socialsecurity|nationalid|taxid|vatid/i,
  /birth|dob\b/i,
  /password|secret|token/i,
  /first_?name|last_?name|middle_?name|full_?name|maiden/i,
  /address|street|city|zip|postal|county|province/i,
  /card|cvv|ccnum|iban|routing|accountnumber/i,
  /latitude|longitude|geoloc/i,
  /signature|photo|avatar/i,
];

function isPii(columnName) {
  return PII_COLUMNS.some((p) => p.test(String(columnName ?? "")));
}

function isSampleable(column) {
  const type = String(column.type ?? "").toLowerCase();
  if (UNSAMPLED_TYPES.includes(type)) return false;
  return column.length !== "(max)";
}

// Splits a table's columns into the ones to select and the ones withheld, with
// the reason, so the sample can say what it is not showing.
function planProjection(columns = []) {
  const selected = [];
  const withheld = [];
  for (const column of columns) {
    if (isPii(column.name)) withheld.push({ name: column.name, reason: "pii" });
    else if (!isSampleable(column)) withheld.push({ name: column.name, reason: "unsampleable" });
    else selected.push(column.name);
  }
  return { selected, withheld };
}

function buildQuery(key, selected, rows = SAMPLE_ROWS) {
  const [schema, name] = key.split(".");
  const cells = selected.map((c) => `LEFT(CAST([${c}] AS nvarchar(${CELL_CHARS})), ${CELL_CHARS})`).join(",\n  ");
  return `SELECT TOP (${rows})\n  ${cells}\nFROM [${schema}].[${name}]`;
}

// Samples one table. Returns null when nothing may be selected, so a table of
// nothing but PII is skipped rather than queried and thrown away.
async function sampleTable(target, entry, deps = {}) {
  const run = deps.run ?? sqlcmd.run;
  const { selected, withheld } = planProjection(entry.structure?.columns);
  if (selected.length === 0) return null;

  await health.assertHealthy(target, `sample ${entry.key}`, deps);
  const rows = await run(
    { host: target.host, database: target.physical, query: buildQuery(entry.key, selected) },
    deps,
  );

  return {
    key: entry.key,
    columns: selected,
    withheld,
    rows: rows.map((r) => r.map((c) => c.trim())),
    complete: entry.object.rows <= SAMPLE_ROWS,
  };
}

// Samples up to `limit` tables, largest-value-first: a table whose whole
// contents fit in the sample is a value domain and worth more than 25 rows off
// the front of a 616M-row log, so those go first when a run is capped.
function order(entries) {
  return [...entries]
    .filter((e) => e.object.kind === "table" && e.object.rows > 0)
    .sort((a, b) => rank(a) - rank(b) || a.key.localeCompare(b.key));
}

function rank(entry) {
  return entry.object.rows <= SAMPLE_ROWS ? 0 : 1;
}

async function sampleAll(target, entries, deps = {}) {
  const queue = order(entries).slice(0, deps.limit ?? Infinity);
  const samples = {};
  for (const [index, entry] of queue.entries()) {
    const sample = await sampleTable(target, entry, deps);
    if (sample) samples[entry.key] = sample;
    deps.logger?.info?.(`sample: ${target.logical} ${index + 1}/${queue.length} ${entry.key}`);
  }
  return samples;
}

// The sample as the describer sees it: a header naming what is withheld, then
// the rows. Kept as text because that is what goes into the prompt.
function renderSample(sample) {
  if (!sample) return "";
  const head = [`${sample.key}${sample.complete ? " (complete table)" : ` (first ${SAMPLE_ROWS} rows)`}`];
  const pii = sample.withheld.filter((w) => w.reason === "pii").map((w) => w.name);
  if (pii.length) head.push(`withheld as personal data: ${pii.join(", ")}`);
  return [...head, sample.columns.join("\t"), ...sample.rows.map((r) => r.join("\t"))].join("\n");
}

module.exports = {
  CELL_CHARS,
  UNSAMPLED_TYPES,
  PII_COLUMNS,
  isPii,
  isSampleable,
  planProjection,
  buildQuery,
  sampleTable,
  sampleAll,
  order,
  renderSample,
};
