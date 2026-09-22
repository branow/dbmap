"use strict";
// What the index covers, as data.
//
// KINDS maps a sys.objects type code to the kind recorded in the index and
// whether that object earns a generated description. EXCLUDED_SCHEMAS drops
// whole schemas before anything is fetched: `cdc` holds 534 auto-generated
// Change Data Capture functions across the three databases, two per tracked
// table, carrying no information a reader of the tracked table lacks.
//
// Triggers are deliberately absent from KINDS. SyncStore has 258 of them at
// close to one per table, generated changelog writers; the index records a
// trigger count on the parent table instead of describing each one.

const KINDS = [
  { type: "U", kind: "table", describe: true },
  { type: "V", kind: "view", describe: true },
  { type: "P", kind: "procedure", describe: true },
  { type: "PC", kind: "procedure", describe: true },
  { type: "FN", kind: "function", describe: true },
  { type: "IF", kind: "function", describe: true },
  { type: "TF", kind: "function", describe: true },
  { type: "FT", kind: "function", describe: true },
  { type: "SN", kind: "synonym", describe: false },
];

const EXCLUDED_SCHEMAS = ["cdc"];

// Object kinds that carry a body in sys.sql_modules.
const MODULE_KINDS = ["view", "procedure", "function"];

const BY_TYPE = new Map(KINDS.map((k) => [k.type, k]));

function kindOf(type) {
  return BY_TYPE.get(type) ?? null;
}

function typeCodes() {
  return KINDS.map((k) => k.type);
}

function hasModule(kind) {
  return MODULE_KINDS.includes(kind);
}

module.exports = { KINDS, EXCLUDED_SCHEMAS, MODULE_KINDS, kindOf, typeCodes, hasModule };
