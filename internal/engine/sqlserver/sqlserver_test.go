package sqlserver

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

// healthy is the reading the fake server gives unless a test says otherwise:
// 12 GB free, the server content, nothing queued.
var healthy = [][]string{{"12", "Available physical memory is high", "0", "0"}}

// server returns a connection that answers the health query, so a test about a
// stage is about that stage rather than about the health check in front of it.
func server() *enginetest.Conn {
	return enginetest.New().On("dm_os_sys_memory", healthy)
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

// The contract, asserted over the whole query surface: nothing this engine can
// build is anything but a read.
func TestEveryGeneratedQueryIsReadOnly(t *testing.T) {
	for name, query := range everyQuery() {
		t.Run(name, func(t *testing.T) {
			if err := engine.AssertReadOnly(query); err != nil {
				t.Fatalf("the engine builds a query the gate refuses: %v\n%s", err, query)
			}
		})
	}
}

// The grant cap is what stands between a metadata build and an unreachable
// server, so it must be on every statement that leaves, not on the ones an
// author remembered to guard.
func TestEveryStatementCarriesTheGrantCap(t *testing.T) {
	e := New()
	conn := server()
	ctx := context.Background()

	if _, err := e.Manifest(ctx, conn); err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	if _, err := e.Structure(ctx, conn); err != nil {
		t.Fatalf("Structure: %v", err)
	}
	if _, err := e.Modules(ctx, conn, []string{"app.OrderGet"}); err != nil {
		t.Fatalf("Modules: %v", err)
	}
	table := engine.Table{Schema: "app", Name: "Orders", Columns: []string{"ID"}}
	if _, err := e.Sample(ctx, conn, table, 25); err != nil {
		t.Fatalf("Sample: %v", err)
	}

	if len(conn.Statements()) == 0 {
		t.Fatal("no statement reached the connection")
	}
	for i, statement := range conn.Statements() {
		if !strings.Contains(statement, Option) {
			t.Errorf("statement %d reached the server without the cap:\n%s", i, statement)
		}
	}
}

// The guard belongs to the statement, so a trailing semicolon must not be left
// in front of it turning the OPTION clause into a syntax error.
func TestTheGuardReplacesATrailingSemicolon(t *testing.T) {
	if got := withOption("SELECT 1;"); got != "SELECT 1\n"+Option {
		t.Errorf("withOption(%q) = %q", "SELECT 1;", got)
	}
	if got := withOption("SELECT 1"); got != "SELECT 1\n"+Option {
		t.Errorf("withOption(%q) = %q", "SELECT 1", got)
	}
}

// The measured estate holds 606 tables over 563 GB, the largest at 616,802,364
// rows. A row count taken by scanning them is the exact query shape that took
// an instance down, so the manifest must read stored page totals and nothing
// else must ever be allowed to creep in.
func TestRowCountsNeverComeFromAScan(t *testing.T) {
	query := manifestQuery()

	if !strings.Contains(query, "sys.dm_db_partition_stats") {
		t.Error("row counts must come from stored page totals")
	}
	for _, banned := range []string{"sp_spaceused", "COUNT(*) FROM sys.objects"} {
		if strings.Contains(query, banned) {
			t.Errorf("the manifest reaches for %q", banned)
		}
	}
	// The one COUNT(*) that is allowed counts triggers in a catalog view, which
	// holds one narrow row per trigger and no data pages at all.
	counts := regexp.MustCompile(`COUNT\(\*\) AS n FROM (\S+)`).FindAllStringSubmatch(query, -1)
	if len(counts) != 1 || counts[0][1] != "sys.triggers" {
		t.Errorf("COUNT(*) appears over %v, only sys.triggers is allowed", counts)
	}
	if strings.Count(query, "COUNT(*)") != 1 {
		t.Errorf("the manifest holds %d COUNT(*) calls, want 1", strings.Count(query, "COUNT(*)"))
	}
}

// A sort would rank the whole table before returning a row; without one the
// engine stops after n. That difference is what makes a 616M-row table as cheap
// to sample as an 11-row lookup.
func TestSampleTakesTopWithNoOrderBy(t *testing.T) {
	e := New()
	query := e.sampleQuery(
		engine.Table{Schema: "app", Name: "Ledger", Columns: []string{"ID", "Amount"}}, 25)

	if !strings.Contains(query, "SELECT TOP (25)") {
		t.Errorf("sample does not cap its rows:\n%s", query)
	}
	if regexp.MustCompile(`(?i)\border\s+by\b`).MatchString(query) {
		t.Errorf("sample sorts the table:\n%s", query)
	}
	if strings.Contains(query, "*") {
		t.Errorf("sample selects a star rather than its planned projection:\n%s", query)
	}
	if !strings.Contains(query, "LEFT(CAST([ID] AS nvarchar(200)), 200)") {
		t.Errorf("sample does not cap a cell:\n%s", query)
	}
}

// A table whose every column was withheld is worth nothing to a describer, so
// no query is opened for it at all.
func TestSampleNeverQueriesAnEmptyProjection(t *testing.T) {
	conn := server()

	sample, err := New().Sample(
		context.Background(), conn, engine.Table{Schema: "app", Name: "People"}, 25)
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if len(sample.Rows) != 0 {
		t.Error("a table with no projection produced rows")
	}
	for _, statement := range conn.Statements() {
		if strings.Contains(statement, "app") {
			t.Errorf("a table with no projection was queried:\n%s", statement)
		}
	}
}

// cdc holds 534 auto-generated Change Data Capture functions across the
// measured estate — two per tracked table, carrying nothing a reader of that
// table lacks. Excluding the schema is 40% of the describe workload.
func TestTheCdcSchemaIsExcluded(t *testing.T) {
	if len(ExcludedSchemas) != 1 || ExcludedSchemas[0] != "cdc" {
		t.Fatalf("ExcludedSchemas = %v, want [cdc]", ExcludedSchemas)
	}
	for name, query := range everyQuery() {
		switch name {
		case "health", "sample", "modules":
			continue
		}
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(query, "NOT IN ('cdc')") {
				t.Errorf("query does not exclude cdc:\n%s", query)
			}
		})
	}
}

// One measured database holds 258 triggers at close to one per table. They are
// counted on the parent and never described, so no type code may map to a
// trigger kind and no kind the table produces may be outside the catalog
// vocabulary.
func TestTriggersAreCountedNeverDescribed(t *testing.T) {
	for _, row := range Types {
		if row.Kind == "trigger" {
			t.Fatalf("type %q maps to a trigger kind", row.Type)
		}
		if _, ok := catalog.Lookup(row.Kind); !ok {
			t.Errorf("type %q maps to %q, which is not a catalog kind", row.Type, row.Kind)
		}
	}
	if _, ok := kindOf("TR"); ok {
		t.Error("the DML trigger type code is covered by the type table")
	}
	if _, ok := kindOf("TA"); ok {
		t.Error("the CLR trigger type code is covered by the type table")
	}
}

// The type table is the only place a SQL Server code becomes a kind, so every
// code the reference implementation covered must still be covered.
func TestTypeCodesMapOntoCatalogKinds(t *testing.T) {
	cases := map[string]catalog.Kind{
		"U": catalog.Table, "V": catalog.View,
		"P": catalog.Procedure, "PC": catalog.Procedure,
		"FN": catalog.Function, "IF": catalog.Function,
		"TF": catalog.Function, "FT": catalog.Function,
		"SN": catalog.Synonym,
	}
	if len(cases) != len(Types) {
		t.Fatalf("the type table holds %d rows, the test covers %d", len(Types), len(cases))
	}
	for code, want := range cases {
		got, ok := kindOf(code)
		if !ok {
			t.Errorf("type %q is not covered", code)
			continue
		}
		if got != want {
			t.Errorf("type %q maps to %q, want %q", code, got, want)
		}
	}
	if _, ok := kindOf("XX"); ok {
		t.Error("an unknown type code must be reported, not guessed at")
	}
}

func TestQuoteEscapesTheTerminator(t *testing.T) {
	cases := map[string]string{
		"Orders":     "[Orders]",
		"Odd]Name":   "[Odd]]Name]",
		"with space": "[with space]",
	}
	for in, want := range cases {
		if got := New().Quote(in); got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseManifest(t *testing.T) {
	rows := [][]string{
		{"app", "Orders", "U", "2026-08-06T00:14:34", "11", "16", "2"},
		{"app", "OrderList", "V", "2026-01-02T00:00:00", "0", "0", "0"},
		{"app", "OrderGet", "P", "2024-07-25T09:00:00", "0", "0", "0"},
		{"app", "Legacy", "TR", "2024-07-25T09:00:00", "0", "0", "0"},
		{"app", "Short", "U", "2026-01-01T00:00:00"},
	}

	objects := parseManifest(rows)

	if len(objects) != 3 {
		t.Fatalf("parsed %d objects, want 3: %+v", len(objects), objects)
	}
	first := objects[0]
	if first.Key() != "app.Orders" || first.Kind != catalog.Table {
		t.Errorf("first object = %+v", first)
	}
	if first.Rows != 11 || first.KB != 16 || first.Triggers != 2 {
		t.Errorf("counts parsed wrong: %+v", first)
	}
	if !first.Modified.Present() {
		t.Error("modify_date must fill the modify signal")
	}
	if first.Modified.String() != "2026-08-06T00:14:34" {
		t.Errorf("signal = %q", first.Modified)
	}
}

// A body arrives as an ordinary column value now. The sentinel line and the raw
// output mode that forced it are gone, so a procedure containing a tab is no
// longer a bug class.
func TestModulesReadBodiesAsOrdinaryRows(t *testing.T) {
	conn := server().On("sys.sql_modules", [][]string{
		{"app.OrderGet", "CREATE PROCEDURE app.OrderGet\nAS\n\tSELECT\t1"},
		{"app.Empty", ""},
	})

	bodies, err := New().Modules(context.Background(), conn, []string{"app.OrderGet", "app.Empty"})
	if err != nil {
		t.Fatalf("Modules: %v", err)
	}
	if len(bodies) != 1 {
		t.Fatalf("got %d bodies, want 1: %v", len(bodies), bodies)
	}
	if !strings.Contains(bodies["app.OrderGet"].String(), "\t") {
		t.Error("a body containing a tab came back mangled")
	}
	for _, statement := range conn.Statements() {
		if strings.Contains(statement, "~~OBJ~~") {
			t.Error("the sentinel hack survived the port")
		}
	}
}

// A body is redacted at the moment it arrives, which is here. The type says so:
// only the redactor produces a redact.Body, so no caller can be handed raw text
// and no caller has to remember to clean it.
func TestModulesRedactOnArrival(t *testing.T) {
	// A mail call carrying a distribution list is the redaction that actually
	// fired in the measured corpus: 7 addresses across 622 procedure bodies,
	// and no credentials at all.
	raw := "CREATE PROCEDURE app.Notify\nAS\n" +
		"EXEC msdb.dbo.sp_send_dbmail @recipients = 'ops@example.internal'"
	conn := server().On("sys.sql_modules", [][]string{{"app.Notify", raw}})

	bodies, err := New().Modules(context.Background(), conn, []string{"app.Notify"})
	if err != nil {
		t.Fatalf("Modules: %v", err)
	}

	body := bodies["app.Notify"]
	if strings.Contains(body.String(), "ops@example.internal") {
		t.Fatalf("an address reached the caller unredacted: %s", body)
	}
	if !strings.Contains(body.String(), "sp_send_dbmail") {
		t.Error("redaction took the surrounding code with it")
	}
	if body.Counts().Total() == 0 {
		t.Error("what was stripped must be reported, not silently swallowed")
	}
}

// Keys ride as parameters, so a name is never concatenated into a catalog
// query.
func TestModulesBindKeysAsParameters(t *testing.T) {
	conn := server()
	keys := []string{"app.A", "app.B"}

	if _, err := New().Modules(context.Background(), conn, keys); err != nil {
		t.Fatalf("Modules: %v", err)
	}

	var fetch enginetest.Call
	for _, call := range conn.Calls() {
		if strings.Contains(call.Statement, "sys.sql_modules") {
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

func TestModulesBatchesAtFortyObjects(t *testing.T) {
	conn := server()
	keys := make([]string, 85)
	for i := range keys {
		keys[i] = "app.Module"
	}

	if _, err := New().Modules(context.Background(), conn, keys); err != nil {
		t.Fatalf("Modules: %v", err)
	}

	fetches := 0
	for _, statement := range conn.Statements() {
		if strings.Contains(statement, "sys.sql_modules") {
			fetches++
		}
	}
	if fetches != 3 {
		t.Errorf("85 keys took %d fetches, want 3 batches of at most 40", fetches)
	}
}

func TestWidthRendersWhatAReaderNeeds(t *testing.T) {
	cases := []struct {
		kind, maxLength, precision, scale, want string
	}{
		{"int", "4", "10", "0", ""},
		{"varchar", "50", "0", "0", "(50)"},
		{"nvarchar", "100", "0", "0", "(50)"},
		{"nvarchar", "-1", "0", "0", "(max)"},
		{"varbinary", "-1", "0", "0", "(max)"},
		{"decimal", "9", "18", "2", "(18,2)"},
		{"numeric", "9", "10", "4", "(10,4)"},
		{"datetime", "8", "0", "0", ""},
	}
	for _, c := range cases {
		got := width(c.kind, c.maxLength, c.precision, c.scale)
		if got != c.want {
			t.Errorf("width(%q, %q) = %q, want %q", c.kind, c.maxLength, got, c.want)
		}
	}
}

func TestFoldIndexesLiftsThePrimaryKeyOut(t *testing.T) {
	key, indexes := foldIndexes([]indexRow{
		{name: "pkOrders", primary: true, unique: true, column: "OrderID"},
		{name: "pkOrders", primary: true, unique: true, column: "LineID"},
		{name: "ixCustomer", unique: false, column: "CustomerID"},
		{name: "ixCustomer", unique: false, column: "ModifiedDate"},
		{name: "ixCode", unique: true, column: "Code"},
	})

	if strings.Join(key, ",") != "OrderID,LineID" {
		t.Errorf("primary key = %v", key)
	}
	if len(indexes) != 2 {
		t.Fatalf("got %d indexes, want 2: %+v", len(indexes), indexes)
	}
	if indexes[0].Name != "ixCustomer" || len(indexes[0].Columns) != 2 {
		t.Errorf("index 0 = %+v", indexes[0])
	}
	if !indexes[1].Unique {
		t.Errorf("uniqueness was dropped: %+v", indexes[1])
	}
}

func TestStructureFoldsEverySourceOntoOneObject(t *testing.T) {
	conn := server().
		On("c.is_computed", [][]string{
			{"app", "Orders", "OrderID", "int", "4", "10", "0", "0", "1", "0"},
			{"app", "Orders", "Code", "nvarchar", "20", "0", "0", "1", "0", "0"},
		}).
		On("ic.key_ordinal", [][]string{
			{"app", "Orders", "pkOrders", "1", "1", "OrderID"},
		}).
		On("sys.foreign_key_columns", [][]string{
			{"app", "Orders", "CustomerID", "app", "Customers", "CustomerID"},
		}).
		On("p.is_output", [][]string{
			{"app", "OrderGet", "", "int", "4", "0"},
			{"app", "OrderGet", "@id", "int", "4", "0"},
		}).
		On("sys.synonyms", [][]string{
			{"app", "Ledger", "other.dbo.Ledger"},
		})

	structures, err := New().Structure(context.Background(), conn)
	if err != nil {
		t.Fatalf("Structure: %v", err)
	}

	orders := structures["app.Orders"]
	if len(orders.Columns) != 2 {
		t.Fatalf("app.Orders has %d columns", len(orders.Columns))
	}
	if orders.Columns[0].Name != "OrderID" || !orders.Columns[0].Identity {
		t.Errorf("column 0 = %+v", orders.Columns[0])
	}
	if orders.Columns[1].Length != "(10)" {
		t.Errorf("nvarchar(20) bytes must read as 10 characters: %+v", orders.Columns[1])
	}
	if !orders.Columns[1].Nullable {
		t.Errorf("nullability was dropped: %+v", orders.Columns[1])
	}
	if strings.Join(orders.PrimaryKey, ",") != "OrderID" {
		t.Errorf("primary key = %v", orders.PrimaryKey)
	}
	if len(orders.ForeignKeys) != 1 || orders.ForeignKeys[0].References != "app.Customers.CustomerID" {
		t.Errorf("foreign keys = %+v", orders.ForeignKeys)
	}

	get := structures["app.OrderGet"]
	if len(get.Parameters) != 2 {
		t.Fatalf("app.OrderGet has %d parameters", len(get.Parameters))
	}
	if get.Parameters[0].Name != catalog.Returns {
		t.Errorf("the unnamed return row must become %q: %+v", catalog.Returns, get.Parameters[0])
	}
	if structures["app.Ledger"].Target != "other.dbo.Ledger" {
		t.Errorf("synonym target = %q", structures["app.Ledger"].Target)
	}
}

// Health is asked before every stage, not once at startup, so a run that begins
// on a healthy server still stops when it stops being one.
func TestAStageHaltsWhenTheServerHasNoRoom(t *testing.T) {
	conn := enginetest.New().
		On("dm_os_sys_memory", [][]string{{"1", "Available physical memory is high", "0", "0"}})

	_, err := New().Manifest(context.Background(), conn)

	var unhealthy *engine.UnhealthyError
	if !errors.As(err, &unhealthy) {
		t.Fatalf("error is %T, want *engine.UnhealthyError", err)
	}
	if !strings.Contains(unhealthy.Reason, "floor is 2 GB") {
		t.Errorf("reason = %q", unhealthy.Reason)
	}
	for _, statement := range conn.Statements() {
		if strings.Contains(statement, "sys.objects") {
			t.Error("the manifest query was sent after the health check failed")
		}
	}
}

// An account that cannot see the DMVs cannot see the floor either. That is
// unknown, and unknown stops the build.
func TestADeniedHealthViewIsUnknownNotHealthy(t *testing.T) {
	conn := enginetest.New().
		Fail("dm_os_sys_memory", errors.New("the user does not have permission"))

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
		{"a full reading", [][]string{{"12", "high", "0", "0"}}, false},
		{"no rows at all", nil, true},
		{"a short row", [][]string{{"12", "high"}}, true},
		{"a memory figure that is not a number", [][]string{{"", "high", "0", "0"}}, true},
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
