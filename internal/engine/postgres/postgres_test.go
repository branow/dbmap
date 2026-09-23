package postgres

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/engine"
	"github.com/branow/dbmap/internal/engine/enginetest"
)

// healthy is the reading the fake server gives unless a test says otherwise.
var healthy = [][]string{{"primary", "0"}}

// server returns a connection that answers the health query, so a test about a
// stage is about that stage rather than about the health check in front of it.
func server() *enginetest.Conn {
	return enginetest.New().On("pg_is_in_recovery", healthy)
}

// every query this engine can build, so the safety assertions below cover the
// whole surface rather than the queries a test author remembered.
func everyQuery() map[string]string {
	e := New()
	queries := map[string]string{
		"manifest": manifestQuery(),
		"health":   HealthQuery(),
		"modules":  modulesQuery(3),
		"sample": e.sampleQuery(
			engine.Table{Schema: "app", Name: "Orders", Columns: []string{"ID", "Status"}}, 25),
	}
	for _, source := range sources {
		queries["structure/"+source.Name] = source.SQL()
	}
	return queries
}

func TestEveryGeneratedQueryIsReadOnly(t *testing.T) {
	for name, query := range everyQuery() {
		t.Run(name, func(t *testing.T) {
			if err := enginetest.ReadOnly(query); err != nil {
				t.Fatalf("the engine builds a query that is not a read: %v\n%s", err, query)
			}
		})
	}
}

// This engine's whole resource cap is the session, so every statement must
// arrive inside one. A read-only transaction is a promise the server keeps,
// which is strictly stronger than the regex in front of it — but the regex
// stays, because two independent guarantees fail independently.
func TestEveryStatementRunsInsideTheGuardedSession(t *testing.T) {
	e := New()
	conn := server()
	ctx := context.Background()

	if _, err := e.Manifest(ctx, conn); err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	if _, err := e.Structure(ctx, conn); err != nil {
		t.Fatalf("Structure: %v", err)
	}
	if _, err := e.Modules(ctx, conn, []string{"app.order_get"}); err != nil {
		t.Fatalf("Modules: %v", err)
	}
	table := engine.Table{Schema: "app", Name: "orders", Columns: []string{"id"}}
	if _, err := e.Sample(ctx, conn, table, 25); err != nil {
		t.Fatalf("Sample: %v", err)
	}

	if len(conn.Calls()) == 0 {
		t.Fatal("no statement reached the connection")
	}
	want := []string{"statement_timeout", "work_mem", "max_parallel_workers_per_gather = 0"}
	for i, call := range conn.Calls() {
		if !call.Session.ReadOnly {
			t.Errorf("statement %d ran outside a read-only transaction", i)
		}
		settings := strings.Join(call.Session.Set, "\n")
		for _, setting := range want {
			if !strings.Contains(settings, setting) {
				t.Errorf("statement %d ran without %s", i, setting)
			}
		}
	}
}

// Every setting is session LOCAL, so it cannot leak onto the next borrower of a
// pooled connection.
func TestEverySettingIsSessionLocal(t *testing.T) {
	for _, setting := range New().Guard().Session.Set {
		if !strings.HasPrefix(setting, "SET LOCAL ") {
			t.Errorf("%q outlives its transaction", setting)
		}
	}
	if New().Guard().Statement != nil {
		t.Error("this engine's guard is the session; a statement rewrite would be a second one")
	}
}

// count(*) over a Postgres table reads every page of it. reltuples is the
// planner's own estimate, already maintained, and it decides sample depth and
// nothing else.
func TestRowCountsNeverComeFromAScan(t *testing.T) {
	query := manifestQuery()

	if !strings.Contains(query, "c.reltuples") {
		t.Error("row counts must come from the planner's estimate")
	}
	if !strings.Contains(query, "pg_catalog.pg_total_relation_size") {
		t.Error("sizes must come from stored page counts")
	}
	// The one count(*) that is allowed counts triggers in a catalog table.
	counts := regexp.MustCompile(`count\(\*\) FROM (\S+)`).FindAllStringSubmatch(query, -1)
	if len(counts) != 1 || counts[0][1] != "pg_catalog.pg_trigger" {
		t.Errorf("count(*) appears over %v, only pg_trigger is allowed", counts)
	}
	if n := strings.Count(strings.ToLower(query), "count(*)"); n != 1 {
		t.Errorf("the manifest holds %d count(*) calls, want 1", n)
	}
}

func TestSampleLimitsWithNoOrderBy(t *testing.T) {
	query := New().sampleQuery(
		engine.Table{Schema: "app", Name: "ledger", Columns: []string{"id", "amount"}}, 25)

	if !strings.Contains(query, "LIMIT 25") {
		t.Errorf("sample does not cap its rows:\n%s", query)
	}
	if regexp.MustCompile(`(?i)\border\s+by\b`).MatchString(query) {
		t.Errorf("sample sorts the table:\n%s", query)
	}
	if strings.Contains(query, "*") {
		t.Errorf("sample selects a star rather than its planned projection:\n%s", query)
	}
	if !strings.Contains(query, `left(cast("id" AS text), 200)`) {
		t.Errorf("sample does not cap a cell:\n%s", query)
	}
}

func TestSampleNeverQueriesAnEmptyProjection(t *testing.T) {
	conn := server()

	sample, err := New().Sample(
		context.Background(), conn, engine.Table{Schema: "app", Name: "people"}, 25)
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if len(sample.Rows) != 0 {
		t.Error("a table with no projection produced rows")
	}
	for _, statement := range conn.Statements() {
		if strings.Contains(statement, "people") {
			t.Errorf("a table with no projection was queried:\n%s", statement)
		}
	}
}

// Postgres keeps no modify_date, so nothing here may invent one. An object with
// no signal can never be proven untouched, which is what makes the content
// fingerprint do all the gating on this engine — with no stage above naming it.
func TestNoObjectCarriesAModifySignal(t *testing.T) {
	objects := parseManifest([][]string{
		{"app", "orders", "rel:r", "1100", "16", "2"},
		{"app", "order_list", "rel:v", "0", "0", "0"},
		{"app", "order_get", "pro:f", "0", "0", "0"},
	})

	if len(objects) != 3 {
		t.Fatalf("parsed %d objects, want 3", len(objects))
	}
	for _, object := range objects {
		if object.Modified.Present() {
			t.Errorf("%s carries a modify signal Postgres cannot supply", object.Key())
		}
	}
	if objects[0].Rows != 1100 || objects[0].KB != 16 || objects[0].Triggers != 2 {
		t.Errorf("counts parsed wrong: %+v", objects[0])
	}
}

// relkind and prokind both spell one of their codes 'p'. The tag is what keeps
// a partitioned table from being read as a stored procedure.
func TestCatalogCodesMapOntoCatalogKinds(t *testing.T) {
	cases := map[string]catalog.Kind{
		"rel:r": catalog.Table,
		"rel:p": catalog.Table,
		"rel:v": catalog.View,
		"rel:m": catalog.View,
		"pro:f": catalog.Function,
		"pro:p": catalog.Procedure,
	}
	if len(cases) != len(Codes) {
		t.Fatalf("the code table holds %d rows, the test covers %d", len(Codes), len(cases))
	}
	for code, want := range cases {
		got, ok := kindOf(code)
		if !ok {
			t.Errorf("code %q is not covered", code)
			continue
		}
		if got != want {
			t.Errorf("code %q maps to %q, want %q", code, got, want)
		}
	}
	if _, ok := kindOf("p"); ok {
		t.Error("an untagged code is ambiguous and must not resolve")
	}
	if _, ok := kindOf("rel:i"); ok {
		t.Error("an index is not an indexed object")
	}
}

// Triggers are counted on the parent and never described, so no code may map to
// a trigger kind and every kind the table produces must be catalog vocabulary.
func TestTriggersAreCountedNeverDescribed(t *testing.T) {
	for _, row := range Codes {
		if row.Kind == "trigger" {
			t.Fatalf("code %q maps to a trigger kind", row.Code)
		}
		if _, ok := catalog.Lookup(row.Kind); !ok {
			t.Errorf("code %q maps to %q, which is not a catalog kind", row.Code, row.Kind)
		}
	}
	if !strings.Contains(manifestQuery(), "pg_catalog.pg_trigger") {
		t.Error("the manifest must count triggers on the parent")
	}
}

// Postgres' own catalog is not what a reader of this index is looking for, and
// a temporary schema is not there to be read at all.
func TestSystemSchemasAreExcluded(t *testing.T) {
	for name, query := range everyQuery() {
		switch name {
		case "health", "sample":
			continue
		}
		t.Run(name, func(t *testing.T) {
			for _, schema := range ExcludedSchemas {
				if !strings.Contains(query, "'"+schema+"'") {
					t.Errorf("query does not exclude %s:\n%s", schema, query)
				}
			}
			if !strings.Contains(query, `NOT LIKE 'pg\_temp\_%'`) {
				t.Errorf("query does not exclude temporary schemas:\n%s", query)
			}
		})
	}
}

func TestQuoteEscapesTheTerminator(t *testing.T) {
	cases := map[string]string{
		"orders":      `"orders"`,
		`odd"name`:    `"odd""name"`,
		"with space":  `"with space"`,
		"MixedCase":   `"MixedCase"`,
		"drop table;": `"drop table;"`,
	}
	for in, want := range cases {
		if got := New().Quote(in); got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
}

// format_type does not always put the width at the end, so lifting it out is
// not the same as chopping off a suffix.
func TestSplitType(t *testing.T) {
	cases := []struct {
		rendered string
		kind     string
		length   string
	}{
		{"integer", "integer", ""},
		{"character varying(50)", "character varying", "(50)"},
		{"numeric(18,2)", "numeric", "(18,2)"},
		{"timestamp(3) without time zone", "timestamp without time zone", "(3)"},
		{"integer[]", "integer[]", ""},
		{"text", "text", ""},
	}
	for _, c := range cases {
		kind, length := splitType(c.rendered)
		if kind != c.kind || length != c.length {
			t.Errorf("splitType(%q) = (%q, %q), want (%q, %q)",
				c.rendered, kind, length, c.kind, c.length)
		}
	}
}

func TestStructureFoldsEverySourceOntoOneObject(t *testing.T) {
	conn := server().
		On("a.attgenerated", [][]string{
			{"app", "orders", "id", "integer", "0", "1", "0"},
			{"app", "orders", "code", "character varying(20)", "1", "0", "0"},
		}).
		On("i.indnkeyatts", [][]string{
			{"app", "orders", "orders_pkey", "1", "1", "id"},
			{"app", "orders", "orders_code_ix", "0", "0", "code"},
		}).
		On("k.contype", [][]string{
			{"app", "orders", "customer_id", "app", "customers", "id"},
		}).
		On("p.proargmodes", [][]string{
			{"app", "order_get", catalog.Returns, "integer", "0", "0"},
			{"app", "order_get", "p_id", "integer", "0", "1"},
			{"app", "order_get", "p_out", "text", "1", "2"},
		})

	structures, err := New().Structure(context.Background(), conn)
	if err != nil {
		t.Fatalf("Structure: %v", err)
	}

	orders := structures["app.orders"]
	if len(orders.Columns) != 2 {
		t.Fatalf("app.orders has %d columns", len(orders.Columns))
	}
	if !orders.Columns[0].Identity || orders.Columns[0].Nullable {
		t.Errorf("column 0 = %+v", orders.Columns[0])
	}
	if orders.Columns[1].Type != "character varying" || orders.Columns[1].Length != "(20)" {
		t.Errorf("column 1 = %+v", orders.Columns[1])
	}
	if strings.Join(orders.PrimaryKey, ",") != "id" {
		t.Errorf("primary key = %v", orders.PrimaryKey)
	}
	if len(orders.Indexes) != 1 || orders.Indexes[0].Name != "orders_code_ix" {
		t.Errorf("indexes = %+v", orders.Indexes)
	}
	if len(orders.ForeignKeys) != 1 ||
		orders.ForeignKeys[0].References != "app.customers.id" {
		t.Errorf("foreign keys = %+v", orders.ForeignKeys)
	}

	get := structures["app.order_get"]
	if len(get.Parameters) != 3 {
		t.Fatalf("app.order_get has %d parameters", len(get.Parameters))
	}
	if get.Parameters[0].Name != catalog.Returns {
		t.Errorf("the return row must be named %q: %+v", catalog.Returns, get.Parameters[0])
	}
	if !get.Parameters[2].Output {
		t.Errorf("an OUT parameter lost its direction: %+v", get.Parameters[2])
	}
}

func TestModulesReadBodiesAsOrdinaryRows(t *testing.T) {
	conn := server().On("pg_get_functiondef", [][]string{
		{"app.order_get", "CREATE FUNCTION app.order_get()\nAS $$\n\tSELECT\t1\n$$"},
		{"app.empty", ""},
	})

	bodies, err := New().Modules(context.Background(), conn, []string{"app.order_get", "app.empty"})
	if err != nil {
		t.Fatalf("Modules: %v", err)
	}
	if len(bodies) != 1 {
		t.Fatalf("got %d bodies, want 1: %v", len(bodies), bodies)
	}
	if !strings.Contains(bodies["app.order_get"].String(), "\t") {
		t.Error("a body containing a tab came back mangled")
	}
}

// Redaction on arrival is a property of both engines, not a SQL Server habit.
func TestModulesRedactOnArrival(t *testing.T) {
	raw := "CREATE FUNCTION app.notify() RETURNS void AS $$\n" +
		"  PERFORM send('ops@example.internal');\n$$ LANGUAGE plpgsql"
	conn := server().On("pg_get_functiondef", [][]string{{"app.notify", raw}})

	bodies, err := New().Modules(context.Background(), conn, []string{"app.notify"})
	if err != nil {
		t.Fatalf("Modules: %v", err)
	}

	body := bodies["app.notify"]
	if strings.Contains(body.String(), "ops@example.internal") {
		t.Fatalf("an address reached the caller unredacted: %s", body)
	}
	if body.Counts().Total() == 0 {
		t.Error("what was stripped must be reported, not silently swallowed")
	}
}

func TestModulesBindKeysAsParameters(t *testing.T) {
	conn := server()
	keys := []string{"app.a", "app.b"}

	if _, err := New().Modules(context.Background(), conn, keys); err != nil {
		t.Fatalf("Modules: %v", err)
	}

	var fetch enginetest.Call
	for _, call := range conn.Calls() {
		if strings.Contains(call.Statement, "pg_get_functiondef") {
			fetch = call
		}
	}
	if len(fetch.Args) != len(keys) {
		t.Fatalf("bound %d arguments for %d keys", len(fetch.Args), len(keys))
	}
	for _, key := range keys {
		if strings.Contains(fetch.Statement, key) {
			t.Errorf("key %q was concatenated into the statement", key)
		}
	}
}

// A reading that claimed zero bytes free would halt every healthy Postgres
// server, so an engine that cannot see memory says so rather than guessing.
func TestAReadingWithoutMemoryIsStillHealthy(t *testing.T) {
	health, err := New().Health(context.Background(), server())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !health.OK || !health.Known {
		t.Fatalf("health = %+v, want a known healthy reading", health)
	}
	if health.Reading.MemoryVisible {
		t.Error("this engine cannot see operating-system memory and must not claim to")
	}
}

// The condition that does apply must still fire.
func TestQueuedSessionsStopTheBuild(t *testing.T) {
	conn := enginetest.New().On("pg_is_in_recovery", [][]string{{"primary", "9"}})

	_, err := New().Manifest(context.Background(), conn)

	var unhealthy *engine.UnhealthyError
	if !errors.As(err, &unhealthy) {
		t.Fatalf("error is %T, want *engine.UnhealthyError", err)
	}
	if !strings.Contains(unhealthy.Reason, "9 queries are queued") {
		t.Errorf("reason = %q", unhealthy.Reason)
	}
	for _, statement := range conn.Statements() {
		if strings.Contains(statement, "pg_class") {
			t.Error("the manifest query was sent after the health check failed")
		}
	}
}

func TestADeniedHealthViewIsUnknownNotHealthy(t *testing.T) {
	conn := enginetest.New().
		Fail("pg_is_in_recovery", errors.New("permission denied for view pg_stat_activity"))

	health, err := New().Health(context.Background(), conn)
	if err != nil {
		t.Fatalf("Health returned an error rather than a verdict: %v", err)
	}
	if health.OK {
		t.Error("an unreadable reading classified as healthy")
	}
	if health.Known {
		t.Error("an unreadable reading classified as known")
	}
}

func TestParseHealth(t *testing.T) {
	cases := []struct {
		name string
		rows [][]string
		nil  bool
	}{
		{"a full reading", [][]string{{"primary", "0"}}, false},
		{"a standby", [][]string{{"standby", "3"}}, false},
		{"no rows at all", nil, true},
		{"a short row", [][]string{{"primary"}}, true},
		{"a count that is not a number", [][]string{{"primary", ""}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseHealth(c.rows)
			if c.nil && got != nil {
				t.Fatalf("want nil, got %+v", got)
			}
			if !c.nil && got == nil {
				t.Fatal("want a reading, got nil")
			}
		})
	}
}

func TestHealthQueryCostsNothingToAnswer(t *testing.T) {
	query := HealthQuery()
	for _, banned := range []string{"ORDER BY", "GROUP BY", "DISTINCT"} {
		if regexp.MustCompile(`(?i)` + banned).MatchString(query) {
			t.Errorf("the health query itself reaches for %s", banned)
		}
	}
}
