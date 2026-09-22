"use strict";
// Module bodies: the text of every procedure, view and function in scope.
//
// Definitions contain tabs and newlines, so they cannot ride the tab-separated
// row format the other stages use. They come back in raw mode instead, each one
// introduced by a sentinel line carrying its object_id, and the batch is split
// on that sentinel rather than on line breaks.
//
// Batched at 40 objects because the corpus is small but not uniform: AppCore's
// 603 modules total 7.6 MB with the largest single body at 173 KB, so a batch
// is a few hundred KB and a pathological one still cannot reach the point where
// buffering it matters. Health is checked between batches, not just at the
// start, so a run that begins on a healthy server still stops if it stops being
// one.

const scope = require("./scope.js");
const sqlcmd = require("./sqlcmd.js");
const health = require("./health.js");

const SENTINEL = "~~OBJ~~";
const BATCH_SIZE = 40;
const MAX_DEFINITION_CHARS = 50000;

// Procedure bodies are where credentials end up in practice — a connection
// string in a linked-server call, a key in an HTTP helper, a distribution list
// in a mail call. A body is redacted the moment it arrives, before it is
// written to the cache and long before it reaches a prompt, so a secret in the
// schema never becomes a secret on disk or a secret in an index.
//
// The patterns replace the value and keep the key, because "password = X" tells
// a reader the procedure authenticates somewhere and that is worth indexing;
// the value never is.
const REDACTIONS = [
  {
    label: "password",
    pattern: /\b(password|pwd)(\s*=\s*)(['"]?)[^\s,;'")]{1,}\3/gi,
    replace: "$1$2<redacted>",
  },
  {
    label: "connection-string",
    pattern: /\b(data source|initial catalog|integrated security)(\s*=\s*)([^;'"\n]{1,})/gi,
    replace: "$1$2<redacted>",
  },
  {
    label: "api-key",
    pattern: /\b(api[_-]?key|access[_-]?token|client[_-]?secret|secret[_-]?key)(\s*=\s*)(['"]?)[^\s,;'")]{1,}\3/gi,
    replace: "$1$2<redacted>",
  },
  { label: "bearer-token", pattern: /\bBearer\s+[A-Za-z0-9._-]{12,}/gi, replace: "Bearer <redacted>" },
  { label: "email", pattern: /[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}/g, replace: "<email>" },
];

// Returns the cleaned text and a count per pattern that fired.
function redact(definition) {
  let text = String(definition ?? "");
  const hits = {};
  for (const { label, pattern, replace } of REDACTIONS) {
    const found = text.match(pattern);
    if (!found) continue;
    hits[label] = found.length;
    text = text.replace(pattern, replace);
  }
  return { text, hits };
}

// LEFT() caps a single pathological body rather than the batch. The cache cap
// sits well above the prompt cap so the stored copy stays the fuller one: the
// whole AppCore corpus is 7.6 MB, so there is nothing to save by trimming it
// hard, and 50,000 characters keeps 99% of modules byte-complete on disk.
function buildQuery(objectIds) {
  const ids = objectIds.map((id) => Number(id)).join(",");
  return `SELECT CHAR(10) + '${SENTINEL}' + CONVERT(varchar(20), m.object_id) + CHAR(10)
  + LEFT(m.definition, ${MAX_DEFINITION_CHARS})
FROM sys.sql_modules m
WHERE m.object_id IN (${ids})`;
}

// Splits a raw batch into { objectId -> definition }. Anything before the first
// sentinel is sqlcmd's own preamble and is discarded.
function parseBodies(raw) {
  const bodies = {};
  const chunks = String(raw ?? "").split(SENTINEL);
  for (const chunk of chunks.slice(1)) {
    const breakAt = chunk.indexOf("\n");
    if (breakAt === -1) continue;
    const objectId = Number(chunk.slice(0, breakAt).trim());
    if (!objectId) continue;
    bodies[objectId] = chunk.slice(breakAt + 1).replace(/\n\(\d+ rows? affected\)\s*$/, "");
  }
  return bodies;
}

function batch(items, size = BATCH_SIZE) {
  const batches = [];
  for (let i = 0; i < items.length; i += size) batches.push(items.slice(i, i + size));
  return batches;
}

// Fetches bodies for the module-bearing objects among `objects`. Returns
// { definitions, redactions }: definitions maps "schema.name" to a redacted
// body, and redactions counts what was stripped, per pattern, so a build can
// report that a secret was found rather than silently swallowing it. An object
// whose body came back empty is omitted rather than recorded as empty.
async function fetchModules(target, objects, deps = {}) {
  const run = deps.run ?? sqlcmd.run;
  const modules = objects.filter((o) => scope.hasModule(o.kind) && o.objectId);
  const byId = new Map(modules.map((o) => [o.objectId, `${o.schema}.${o.name}`]));
  const batches = batch(modules.map((o) => o.objectId));

  const definitions = {};
  const redactions = {};
  for (const [index, ids] of batches.entries()) {
    await health.assertHealthy(target, `modules batch ${index + 1}/${batches.length} on ${target.logical}`, deps);
    const raw = await run({ host: target.host, database: target.physical, query: buildQuery(ids), mode: "raw" }, deps);
    for (const [objectId, definition] of Object.entries(parseBodies(raw))) {
      const key = byId.get(Number(objectId));
      if (!key || !definition.trim()) continue;
      const { text, hits } = redact(definition);
      definitions[key] = text;
      for (const [label, count] of Object.entries(hits)) redactions[label] = (redactions[label] ?? 0) + count;
    }
    deps.logger?.info?.(`modules: ${target.logical} batch ${index + 1}/${batches.length}`);
  }
  return { definitions, redactions };
}

module.exports = {
  SENTINEL,
  BATCH_SIZE,
  MAX_DEFINITION_CHARS,
  REDACTIONS,
  redact,
  buildQuery,
  parseBodies,
  batch,
  fetchModules,
};
