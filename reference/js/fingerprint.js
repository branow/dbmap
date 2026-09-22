"use strict";
// What "changed" means for each kind of object.
//
// Two different signals guard two different costs:
//
//   modify_date  decides whether to FETCH. It is free (already in the manifest)
//                and never misses a real change, but it over-reports: a release
//                that ALTERs 58 procedures bumps all 58 dates even when most are
//                byte-identical to what was already deployed.
//   fingerprint  decides whether to DESCRIBE. Fetching is seconds; describing is
//                an LLM call per object, so the exact check guards the expensive
//                stage and the loose one guards the cheap stage.
//
// A module fingerprints over its definition. A table has no definition, so it
// fingerprints over the structure a reader would need — columns with their
// types and nullability, keys, indexes, trigger count — plus its sample depth.
//
// Sample row VALUES are deliberately absent. They change on every run against a
// live database and would leave every table permanently dirty.

const { createHash } = require("node:crypto");

// Rows sent to the describer, and therefore also the row count above which a
// table growing tells the describer nothing new: TOP 25 of a 40-row table and
// TOP 25 of a 60-row table are the same 25 rows.
//
// 25 because 148 of the 491 non-empty tables on stage hold 25 rows or fewer —
// the lookup and enum tables, where every row is a value worth naming and the
// sample is the whole table. Past 25 the distribution flattens: raising it to 30
// covers five more tables.
const SAMPLE_ROWS = 25;

// How many rows the describer actually gets to see. Below SAMPLE_ROWS a table
// growing by one row changes what the model reads, so the description is stale;
// at or above it, growth changes nothing.
function sampleDepth(rows) {
  return Math.min(Number(rows) || 0, SAMPLE_ROWS);
}

function sha256(text) {
  return createHash("sha256").update(text, "utf8").digest("hex").slice(0, 16);
}

// Line endings and trailing whitespace differ between deployment tools without
// the code differing, so they are normalised away before hashing.
function normalizeDefinition(definition) {
  return String(definition ?? "")
    .replace(/\r\n/g, "\n")
    .split("\n")
    .map((line) => line.replace(/\s+$/, ""))
    .join("\n")
    .trim();
}

function moduleFingerprint(definition) {
  return sha256(normalizeDefinition(definition));
}

// Canonical text for a table's structure. Built as explicit lines rather than
// JSON so the hash does not move when an unrelated field is added to the
// structure objects the fetch stage happens to return.
function canonicalTable(structure) {
  const lines = [];
  for (const c of structure.columns ?? [])
    lines.push(`c ${c.name} ${c.type}${c.length ?? ""} ${c.nullable ? "null" : "notnull"}${c.identity ? " id" : ""}`);
  for (const k of structure.primaryKey ?? []) lines.push(`pk ${k}`);
  for (const f of structure.foreignKeys ?? []) lines.push(`fk ${f.column} -> ${f.references}`);
  for (const i of structure.indexes ?? [])
    lines.push(`ix ${i.name} ${(i.columns ?? []).join(",")}${i.unique ? " u" : ""}`);
  lines.push(`tg ${structure.triggers ?? 0}`);
  lines.push(`rows ${sampleDepth(structure.rows)}`);
  return lines.join("\n");
}

function tableFingerprint(structure) {
  return sha256(canonicalTable(structure));
}

// Dispatches on kind so callers never branch on it themselves.
const FINGERPRINTERS = {
  table: tableFingerprint,
  view: (s) => moduleFingerprint(s.definition),
  procedure: (s) => moduleFingerprint(s.definition),
  function: (s) => moduleFingerprint(s.definition),
  synonym: (s) => sha256(String(s.target ?? "")),
};

function fingerprint(kind, payload) {
  const fn = FINGERPRINTERS[kind];
  if (!fn) throw new Error(`db-index: no fingerprint defined for kind "${kind}"`);
  return fn(payload ?? {});
}

module.exports = {
  SAMPLE_ROWS,
  sampleDepth,
  normalizeDefinition,
  moduleFingerprint,
  canonicalTable,
  tableFingerprint,
  fingerprint,
  sha256,
};
