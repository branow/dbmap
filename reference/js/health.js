"use strict";
// Server memory headroom, checked before the build reads anything and again
// between batches.
//
// This exists because stage is configured with max server memory at 119 GB on a
// 125 GB box, leaving the OS roughly 6 GB for the network stack, the AG threads
// and the cluster service. A query that takes a large memory grant there does
// not just run slowly, it starves Windows — which is how a previous build
// attempt took the instance down rather than merely timing out.
//
// So the build stops on its own rather than being stopped. Every batch asks the
// server how much room it has left, and a run that finds the floor breached
// halts with the numbers in the message instead of issuing the next query.

const sqlcmd = require("./sqlcmd.js");

const MIN_AVAILABLE_GB = 2;
const MAX_WAITING_GRANTS = 5;

const HEALTH_QUERY = `SELECT available_physical_memory_kb / 1048576,
  system_memory_state_desc,
  (SELECT COUNT(*) FROM sys.dm_exec_query_memory_grants WHERE grant_time IS NULL),
  (SELECT CONVERT(int, process_physical_memory_low) FROM sys.dm_os_process_memory)
FROM sys.dm_os_sys_memory`;

// Conditions that stop the build, in the order they are reported. Data rather
// than a chain of ifs so a new signal is one row.
const STOP_CONDITIONS = [
  {
    reason: (h) => `only ${h.availableGb} GB of OS memory free (floor is ${MIN_AVAILABLE_GB} GB)`,
    when: (h) => h.availableGb < MIN_AVAILABLE_GB,
  },
  {
    reason: (h) => `the server reports "${h.state}"`,
    when: (h) => /low/i.test(h.state),
  },
  {
    reason: (h) => `SQL Server signalled physical memory low`,
    when: (h) => h.processMemoryLow,
  },
  {
    reason: (h) => `${h.waitingGrants} queries are queued waiting for a memory grant`,
    when: (h) => h.waitingGrants > MAX_WAITING_GRANTS,
  },
];

function parseHealth(rows) {
  const row = rows[0];
  if (!row || row.length < 4) return null;
  const [availableGb, state, waitingGrants, processMemoryLow] = row.map((c) => c.trim());
  return {
    availableGb: Number(availableGb) || 0,
    state,
    waitingGrants: Number(waitingGrants) || 0,
    processMemoryLow: processMemoryLow === "1",
  };
}

// Returns { ok, reason } for a parsed health reading. A null reading means the
// account cannot see the DMVs; that is reported as unknown rather than healthy,
// so a caller can decide whether its stage is safe to run blind.
function classify(health) {
  if (!health) return { ok: false, known: false, reason: "server memory state could not be read" };
  const hit = STOP_CONDITIONS.find((c) => c.when(health));
  return hit ? { ok: false, known: true, reason: hit.reason(health) } : { ok: true, known: true, reason: "" };
}

async function checkHealth(target, deps = {}) {
  const run = deps.run ?? sqlcmd.run;
  let rows;
  try {
    rows = await run({ host: target.host, database: target.physical, query: HEALTH_QUERY }, deps);
  } catch {
    rows = [];
  }
  const health = parseHealth(rows);
  return { ...classify(health), health };
}

// Throws unless the server has room. `stage` names the caller in the message.
async function assertHealthy(target, stage, deps = {}) {
  const result = await checkHealth(target, deps);
  if (!result.ok) throw new Error(`db-index: halting before ${stage} on ${target.host} — ${result.reason}`);
  return result.health;
}

module.exports = {
  MIN_AVAILABLE_GB,
  MAX_WAITING_GRANTS,
  HEALTH_QUERY,
  STOP_CONDITIONS,
  parseHealth,
  classify,
  checkHealth,
  assertHealthy,
};
