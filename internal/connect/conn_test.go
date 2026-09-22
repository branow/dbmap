package connect

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/engine"
)

// The session guard is the Postgres engine's whole resource cap, so it is worth
// proving it survives the adapter rather than assuming it does. That needs a
// real *sql.DB, which needs a driver — so this file registers one that records
// what database/sql asks of it and returns one row. No socket is opened.
func init() { sql.Register("connecttest", &recorder{}) }

// recorder is a database/sql driver that writes down what it was asked to do.
type recorder struct {
	begun     []driver.TxOptions
	executed  []string
	queried   []string
	rollbacks int
	commits   int
	// failOn makes any statement containing this fragment fail, so the
	// clean-up path can be driven.
	failOn string
}

// shared is the recorder behind the registered driver name. A test resets it
// rather than registering a second driver, because a driver name can be
// registered only once per process.
var shared = &recorder{}

func (*recorder) Open(string) (driver.Conn, error) { return &fakeConn{}, nil }

func reset(failOn string) *recorder {
	*shared = recorder{failOn: failOn}
	return shared
}

type fakeConn struct{}

func (*fakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("connecttest: the adapter must not prepare")
}
func (*fakeConn) Close() error              { return nil }
func (*fakeConn) Begin() (driver.Tx, error) { return nil, errors.New("connecttest: use BeginTx") }

func (*fakeConn) BeginTx(_ context.Context, opts driver.TxOptions) (driver.Tx, error) {
	shared.begun = append(shared.begun, opts)
	return &fakeTx{}, nil
}

func (*fakeConn) ExecContext(
	_ context.Context,
	statement string,
	_ []driver.NamedValue,
) (driver.Result, error) {
	shared.executed = append(shared.executed, statement)
	if shared.failOn != "" && strings.Contains(statement, shared.failOn) {
		return nil, errors.New("connecttest: refused")
	}
	return driver.RowsAffected(0), nil
}

func (*fakeConn) QueryContext(
	_ context.Context,
	statement string,
	_ []driver.NamedValue,
) (driver.Rows, error) {
	shared.queried = append(shared.queried, statement)
	return &fakeDriverRows{}, nil
}

type fakeTx struct{}

func (*fakeTx) Commit() error   { shared.commits++; return nil }
func (*fakeTx) Rollback() error { shared.rollbacks++; return nil }

type fakeDriverRows struct{ done bool }

func (*fakeDriverRows) Columns() []string { return []string{"cell"} }
func (*fakeDriverRows) Close() error      { return nil }
func (r *fakeDriverRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = "value"
	return nil
}

func pool(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("connecttest", "")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// A read-only transaction is a promise the SERVER keeps, which is the whole
// reason the Postgres guard is a session rather than a suffix. If the adapter
// dropped it the engine would lose its only real guarantee and keep only a
// regex.
func TestASessionGuardOpensAReadOnlyTransaction(t *testing.T) {
	log := reset("")
	session := engine.Session{
		ReadOnly: true,
		Set: []string{
			"SET LOCAL statement_timeout = '120s'",
			"SET LOCAL work_mem = '16MB'",
			"SET LOCAL max_parallel_workers_per_gather = 0",
		},
	}

	rows, err := conn{db: pool(t)}.Query(context.Background(), session, "SELECT 1")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	if len(log.begun) != 1 || !log.begun[0].ReadOnly {
		t.Fatalf("transactions begun = %+v, want one read-only", log.begun)
	}
	if len(log.executed) != len(session.Set) {
		t.Fatalf("ran %d settings, want %d", len(log.executed), len(session.Set))
	}
	for i, setting := range session.Set {
		if log.executed[i] != setting {
			t.Errorf("setting %d = %q, want %q", i, log.executed[i], setting)
		}
	}
	if len(log.queried) != 1 || log.queried[0] != "SELECT 1" {
		t.Errorf("queries = %v", log.queried)
	}

	if err := rows.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if log.rollbacks != 1 {
		t.Errorf("the transaction was rolled back %d times, want 1", log.rollbacks)
	}
	if log.commits != 0 {
		t.Error("a read-only transaction was committed rather than rolled back")
	}
}

// The settings run BEFORE the statement, or the statement runs uncapped.
func TestSettingsRunBeforeTheStatement(t *testing.T) {
	log := reset("")
	session := engine.Session{ReadOnly: true, Set: []string{"SET LOCAL work_mem = '16MB'"}}

	rows, err := conn{db: pool(t)}.Query(context.Background(), session, "SELECT 1")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer rows.Close()

	if len(log.executed) == 0 || len(log.queried) == 0 {
		t.Fatal("either the settings or the statement never ran")
	}
}

// A setting that will not apply must abort the query rather than let it run
// without its cap.
func TestAFailedSettingRollsBackAndSendsNothing(t *testing.T) {
	log := reset("work_mem")
	session := engine.Session{ReadOnly: true, Set: []string{"SET LOCAL work_mem = '16MB'"}}

	_, err := conn{db: pool(t)}.Query(context.Background(), session, "SELECT 1")

	if err == nil {
		t.Fatal("a statement ran without its session cap")
	}
	if len(log.queried) != 0 {
		t.Errorf("the statement was sent anyway: %v", log.queried)
	}
	if log.rollbacks != 1 {
		t.Errorf("the transaction was rolled back %d times, want 1", log.rollbacks)
	}
}

// SQL Server has no read-only transaction mode; its cap rides on the statement.
// Opening a transaction it never asked for would be a second behaviour to
// reason about for no gain.
func TestAStatementGuardTakesNoTransaction(t *testing.T) {
	log := reset("")

	rows, err := conn{db: pool(t)}.Query(
		context.Background(), engine.Session{}, "SELECT 1\nOPTION (MAXDOP 1)")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer rows.Close()

	if len(log.begun) != 0 {
		t.Errorf("a statement guard opened %d transactions", len(log.begun))
	}
	if len(log.queried) != 1 {
		t.Fatalf("queries = %v", log.queried)
	}
}

// The adapter must produce something engine.Query can drain, or nothing above
// it works.
func TestTheAdapterSatisfiesTheRowsContract(t *testing.T) {
	reset("")

	got, err := engine.Query(
		context.Background(),
		conn{db: pool(t)},
		engine.Guard{Session: engine.Session{ReadOnly: true}},
		"SELECT 1",
	)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 || got[0][0] != "value" {
		t.Fatalf("rows = %v", got)
	}
}
