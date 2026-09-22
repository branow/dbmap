"use strict";
// Where the database index may be built, as data.
//
// Physical database names differ per environment and follow no single rule
// (AppCore appends a digit, SyncStore is renamed outright), so every name is
// spelled out rather than derived. `prod`, `report` and `monitoring` are absent
// on purpose: resolving one throws, which is the only place the build decides
// what it may touch.

const ENVIRONMENTS = {
  dev: { host: "dev-sqlag.example.internal" },
  test: { host: "test-sqlag.example.internal" },
  stage: { host: "stage-sqlag.example.internal" },
  "data-dev": { host: "datadev-sql.example.internal" },
};

const DATABASES = [
  {
    logical: "AppCore",
    physical: { dev: "AppCore1", test: "AppCore7", stage: "AppCore", "data-dev": "AppCore" },
  },
  {
    logical: "SyncStore",
    physical: { dev: "Mirror1", test: "Mirror7", stage: "SyncStore", "data-dev": "SyncStore" },
  },
  {
    logical: "SideStore",
    physical: {
      dev: "SideStore1",
      test: "SideStore7",
      stage: "SideStore",
      "data-dev": "SideStore",
    },
  },
];

// Every { env, host, logical, physical } the build should visit for `env`.
// `only` narrows to a single logical database by name.
function resolveTargets(env, only) {
  const environment = ENVIRONMENTS[env];
  if (!environment) throw new Error(`db-index: refusing environment "${env}" (allowed: ${names(ENVIRONMENTS)})`);

  const selected = only ? DATABASES.filter((d) => d.logical === only) : DATABASES;
  if (selected.length === 0) throw new Error(`db-index: unknown database "${only}" (known: ${databaseNames()})`);

  return selected.map((d) => ({
    env,
    host: environment.host,
    logical: d.logical,
    physical: d.physical[env],
  }));
}

function names(obj) {
  return Object.keys(obj).join(", ");
}

function databaseNames() {
  return DATABASES.map((d) => d.logical).join(", ");
}

module.exports = { ENVIRONMENTS, DATABASES, resolveTargets };
