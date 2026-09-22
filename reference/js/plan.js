"use strict";
// Staleness planner: turns a fresh manifest plus the previously written index
// into the two work sets the build runs.
//
//   planFetch    what to pull from SQL Server, by modify_date
//   planDescribe what to send to the model, by fingerprint
//
// Both are pure. The fetch stage runs between them, and the split is what makes
// a rebuild after a release cost a few dozen describe calls instead of 1237:
// the release moves modify_date on everything it touched, and the fingerprint
// then discards the ones whose content is unchanged.
//
// An object missing from `state` is new and always both fetched and described.
// An object in `state` but absent from the manifest was dropped from the
// database; planFetch reports it so the writer can remove its index row.

const { fingerprint, sampleDepth } = require("./fingerprint.js");

function keyOf(object) {
  return `${object.schema}.${object.name}`;
}

// Reasons are recorded per object so a build log can say why it did the work.
//
// `sample` applies to tables only. A table is described partly from rows, so
// growth can make the description stale on its own; a procedure is described
// from its body, which modify_date already covers. Scoping the rule to tables
// says that, where letting it run on every kind only worked by accident —
// procedures report zero rows, so it never fired.
const FETCH_REASONS = [
  { reason: "new", kinds: null, when: (object, prior) => !prior },
  { reason: "modified", kinds: null, when: (object, prior) => prior.modified !== object.modified },
  {
    reason: "sample",
    kinds: ["table"],
    when: (object, prior) => sampleDepth(prior.rows) !== sampleDepth(object.rows),
  },
];

function applies(rule, object) {
  return rule.kinds === null || rule.kinds.includes(object.kind);
}

// state: Map<key, { modified, fingerprint, rows?, description? }>
// Returns { fetch, reuse, dropped, reasons }.
function planFetch(objects, state = new Map()) {
  const fetch = [];
  const reuse = [];
  const reasons = {};
  const seen = new Set();

  for (const object of objects) {
    const key = keyOf(object);
    seen.add(key);
    const prior = state.get(key);
    const hit = FETCH_REASONS.find((r) => applies(r, object) && r.when(object, prior));
    if (hit) {
      reasons[key] = hit.reason;
      fetch.push(object);
    } else {
      reuse.push(object);
    }
  }

  const dropped = [...state.keys()].filter((key) => !seen.has(key));
  return { fetch, reuse, dropped, reasons };
}

// `fetched` is [{ object, payload, ... }] straight from the fetch stage:
// payload is whatever fingerprint() needs for that kind. Every other field on
// an entry is carried through untouched, because the describe stage needs the
// structure and key that came with it. Objects whose content hash matches the
// index keep their existing description and cost nothing.
function planDescribe(fetched, state = new Map()) {
  const describe = [];
  const unchanged = [];

  for (const item of fetched) {
    const current = fingerprint(item.object.kind, item.payload);
    const prior = state.get(keyOf(item.object));
    const entry = { ...item, fingerprint: current };
    if (prior && prior.fingerprint === current && prior.description) unchanged.push(entry);
    else describe.push(entry);
  }

  return { describe, unchanged };
}

// Only objects scope marks describable reach the model; a synonym is indexed
// for its target, not for a generated sentence about it.
function describable(entries) {
  return entries.filter((e) => e.object.describe !== false);
}

function summarize({ fetch, reuse, dropped }, describePlan) {
  return {
    fetch: fetch.length,
    reuse: reuse.length,
    dropped: dropped.length,
    describe: describePlan ? describePlan.describe.length : undefined,
    unchanged: describePlan ? describePlan.unchanged.length : undefined,
  };
}

module.exports = { keyOf, planFetch, planDescribe, describable, summarize, FETCH_REASONS };
