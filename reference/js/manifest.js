"use strict";
// Manifest stage: one query per database listing every in-scope object with the
// fingerprint the rest of the build versions against.
//
// `modify_date` is to an object what a commit hash is to a repo clone: unchanged
// since the last build means the object's body and columns are already cached
// and neither needs refetching nor redescribing. That is what keeps run two
// nearly free and what lets a killed run resume where it stopped.
//
// Size and row counts come from sys.dm_db_partition_stats, which reads stored
// page totals. COUNT(*) and sp_spaceused would scan, and scanning 606 tables is
// how a build turns into memory pressure on the instance.

const scope = require("./scope.js");
const sqlcmd = require("./sqlcmd.js");

const COLUMNS = ["schema", "name", "type", "modified", "rows", "kb", "triggers", "objectId"];

function buildQuery() {
  const types = scope
    .typeCodes()
    .map((t) => `'${t}'`)
    .join(",");
  const schemas = scope.EXCLUDED_SCHEMAS.map((s) => `'${s}'`).join(",");

  return `SELECT s.name, o.name, o.type,
  CONVERT(varchar(19), o.modify_date, 126),
  ISNULL(st.row_total, 0), ISNULL(st.kb_total, 0), ISNULL(tg.n, 0), o.object_id
FROM sys.objects o
JOIN sys.schemas s ON s.schema_id = o.schema_id
OUTER APPLY (
  SELECT SUM(CASE WHEN p.index_id < 2 THEN p.row_count ELSE 0 END) AS row_total,
         SUM(p.used_page_count) * 8 AS kb_total
  FROM sys.dm_db_partition_stats p WHERE p.object_id = o.object_id
) st
OUTER APPLY (
  SELECT COUNT(*) AS n FROM sys.triggers t WHERE t.parent_id = o.object_id
) tg
WHERE o.is_ms_shipped = 0
  AND o.type IN (${types})
  AND s.name NOT IN (${schemas})
ORDER BY s.name, o.name`;
}

// Turns sqlcmd rows into manifest entries, dropping any type scope does not
// recognise rather than guessing a kind for it.
function parseManifest(rows) {
  const entries = [];
  for (const row of rows) {
    if (row.length < COLUMNS.length) continue;
    const [schema, name, type, modified, rowCount, kb, triggers, objectId] = row.map((c) => c.trim());
    const kind = scope.kindOf(type);
    if (!kind) continue;
    entries.push({
      schema,
      name,
      kind: kind.kind,
      type,
      describe: kind.describe,
      modified,
      rows: Number(rowCount) || 0,
      kb: Number(kb) || 0,
      triggers: Number(triggers) || 0,
      objectId: Number(objectId) || 0,
    });
  }
  return entries;
}

// Fetches the manifest for one { host, physical, logical, env } target.
async function fetchManifest(target, deps = {}) {
  const run = deps.run ?? sqlcmd.run;
  const rows = await run({ host: target.host, database: target.physical, query: buildQuery() }, deps);
  return {
    env: target.env,
    database: target.logical,
    physical: target.physical,
    objects: parseManifest(rows),
  };
}

// Counts per kind, for a build's progress line.
function summarize(manifest) {
  const counts = {};
  for (const o of manifest.objects) counts[o.kind] = (counts[o.kind] ?? 0) + 1;
  return { database: manifest.database, total: manifest.objects.length, counts };
}

module.exports = { buildQuery, parseManifest, fetchManifest, summarize, COLUMNS };
