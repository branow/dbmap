"use strict";
// Structure stage: columns, keys, indexes, parameters and synonym targets.
//
// One bounded query per source per database — five round trips, around 8,800
// narrow metadata rows across the three stage databases. Every source reads
// sys.* catalog views only, so no user data page is touched and no query needs
// a memory grant worth the name.
//
// Sources are data. Each declares the SQL it sends and how to shape the rows it
// gets back, so adding one is a row rather than a branch, and a test can assert
// what the build would send without a server.

const scope = require("./scope.js");
const sqlcmd = require("./sqlcmd.js");
const health = require("./health.js");

const typeList = () =>
  scope
    .typeCodes()
    .map((t) => `'${t}'`)
    .join(",");

const schemaList = () => scope.EXCLUDED_SCHEMAS.map((s) => `'${s}'`).join(",");

const IN_SCOPE = () => `o.is_ms_shipped = 0 AND o.type IN (${typeList()}) AND s.name NOT IN (${schemaList()})`;

const SOURCES = [
  {
    name: "columns",
    sql: () => `SELECT s.name, o.name, c.name, t.name, c.max_length, c.precision, c.scale,
  c.is_nullable, c.is_identity, c.is_computed
FROM sys.columns c
JOIN sys.objects o ON o.object_id = c.object_id
JOIN sys.schemas s ON s.schema_id = o.schema_id
JOIN sys.types t ON t.user_type_id = c.user_type_id
WHERE ${IN_SCOPE()}
ORDER BY s.name, o.name, c.column_id`,
    shape: (r) => ({
      owner: `${r[0]}.${r[1]}`,
      value: {
        name: r[2],
        type: r[3],
        length: widthOf(r[3], r[4], r[5], r[6]),
        nullable: r[7] === "1",
        identity: r[8] === "1",
        computed: r[9] === "1",
      },
      into: "columns",
    }),
  },
  {
    name: "indexes",
    sql: () => `SELECT s.name, o.name, i.name, i.is_primary_key, i.is_unique, c.name
FROM sys.indexes i
JOIN sys.objects o ON o.object_id = i.object_id
JOIN sys.schemas s ON s.schema_id = o.schema_id
JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
WHERE ${IN_SCOPE()} AND i.type > 0 AND ic.is_included_column = 0
ORDER BY s.name, o.name, i.index_id, ic.key_ordinal`,
    shape: (r) => ({
      owner: `${r[0]}.${r[1]}`,
      value: { name: r[2], primary: r[3] === "1", unique: r[4] === "1", column: r[5] },
      into: "indexRows",
    }),
  },
  {
    name: "foreignKeys",
    sql: () => `SELECT ps.name, po.name, pc.name, rs.name, ro.name, rc.name
FROM sys.foreign_key_columns fkc
JOIN sys.objects po ON po.object_id = fkc.parent_object_id
JOIN sys.schemas ps ON ps.schema_id = po.schema_id
JOIN sys.columns pc ON pc.object_id = fkc.parent_object_id AND pc.column_id = fkc.parent_column_id
JOIN sys.objects ro ON ro.object_id = fkc.referenced_object_id
JOIN sys.schemas rs ON rs.schema_id = ro.schema_id
JOIN sys.columns rc ON rc.object_id = fkc.referenced_object_id AND rc.column_id = fkc.referenced_column_id
ORDER BY ps.name, po.name, pc.name`,
    shape: (r) => ({
      owner: `${r[0]}.${r[1]}`,
      value: { column: r[2], references: `${r[3]}.${r[4]}.${r[5]}` },
      into: "foreignKeys",
    }),
  },
  {
    name: "parameters",
    sql: () => `SELECT s.name, o.name, p.name, t.name, p.max_length, p.is_output
FROM sys.parameters p
JOIN sys.objects o ON o.object_id = p.object_id
JOIN sys.schemas s ON s.schema_id = o.schema_id
JOIN sys.types t ON t.user_type_id = p.user_type_id
WHERE ${IN_SCOPE()}
ORDER BY s.name, o.name, p.parameter_id`,
    shape: (r) => ({
      owner: `${r[0]}.${r[1]}`,
      value: { name: r[2] || "(returns)", type: `${r[3]}${widthOf(r[3], r[4], 0, 0)}`, output: r[5] === "1" },
      into: "parameters",
    }),
  },
  {
    name: "synonyms",
    sql: () => `SELECT s.name, sn.name, sn.base_object_name
FROM sys.synonyms sn
JOIN sys.schemas s ON s.schema_id = sn.schema_id
ORDER BY s.name, sn.name`,
    shape: (r) => ({ owner: `${r[0]}.${r[1]}`, value: r[2], into: "target" }),
  },
];

// varchar(50), decimal(18,2), int — the width a reader needs, not the raw
// catalog numbers. max_length is bytes, so nvarchar halves it and -1 is (max).
const SIZED = ["varchar", "nvarchar", "char", "nchar", "varbinary", "binary"];
const SCALED = ["decimal", "numeric"];

function widthOf(type, maxLength, precision, scale) {
  if (SCALED.includes(type)) return `(${precision},${scale})`;
  if (!SIZED.includes(type)) return "";
  if (Number(maxLength) === -1) return "(max)";
  const chars = type.startsWith("n") ? Number(maxLength) / 2 : Number(maxLength);
  return `(${chars})`;
}

// Folds shaped rows into a map of object key -> structure fields.
function collect(shaped) {
  const byOwner = new Map();
  for (const { owner, value, into } of shaped) {
    const entry = byOwner.get(owner) ?? {};
    if (into === "target") entry.target = value;
    else (entry[into] = entry[into] ?? []).push(value);
    byOwner.set(owner, entry);
  }
  return byOwner;
}

// Index rows arrive one row per column; fold them into one entry per index and
// lift the primary key out, since a reader wants it named separately.
function foldIndexes(indexRows = []) {
  const byName = new Map();
  for (const row of indexRows) {
    const entry = byName.get(row.name) ?? { name: row.name, primary: row.primary, unique: row.unique, columns: [] };
    entry.columns.push(row.column);
    byName.set(row.name, entry);
  }
  const all = [...byName.values()];
  return {
    primaryKey: all.find((i) => i.primary)?.columns ?? [],
    indexes: all.filter((i) => !i.primary).map(({ name, unique, columns }) => ({ name, unique, columns })),
  };
}

// Runs every source against one target, health-checked between each, and
// returns a map of "schema.name" -> structure ready for fingerprinting.
async function fetchStructure(target, deps = {}) {
  const run = deps.run ?? sqlcmd.run;
  const shaped = [];

  for (const source of SOURCES) {
    await health.assertHealthy(target, `structure/${source.name} on ${target.logical}`, deps);
    const rows = await run({ host: target.host, database: target.physical, query: source.sql() }, deps);
    for (const row of rows) if (row.length >= 3) shaped.push(source.shape(row.map((c) => c.trim())));
    deps.logger?.info?.(`structure: ${target.logical}/${source.name} — ${rows.length} rows`);
  }

  const collected = collect(shaped);
  const structure = {};
  for (const [key, entry] of collected) {
    const { indexRows, ...rest } = entry;
    structure[key] = { ...rest, ...foldIndexes(indexRows) };
  }
  return structure;
}

module.exports = { SOURCES, widthOf, collect, foldIndexes, fetchStructure };
