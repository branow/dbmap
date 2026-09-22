package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// spy records what actually reached the connection, which is the only way to
// prove the query path applied the guard rather than trusting that it did.
type spy struct {
	sessions   []Session
	statements []string
	rows       *fakeRows
	err        error
}

func (s *spy) Query(_ context.Context, session Session, statement string, _ ...any) (Rows, error) {
	s.sessions = append(s.sessions, session)
	s.statements = append(s.statements, statement)
	if s.err != nil {
		return nil, s.err
	}
	if s.rows == nil {
		return &fakeRows{columns: []string{"a"}}, nil
	}
	return s.rows, nil
}

type fakeRows struct {
	columns []string
	data    [][]any
	at      int
	err     error
}

func (r *fakeRows) Columns() ([]string, error) { return r.columns, nil }
func (r *fakeRows) Next() bool                 { r.at++; return r.at <= len(r.data) }
func (r *fakeRows) Err() error                 { return r.err }
func (r *fakeRows) Close() error               { return nil }

// Scan mirrors what collect asks for: a sql.NullString per column, so a NULL
// arrives as an invalid value rather than as an empty string the test could not
// tell apart from a real one.
func (r *fakeRows) Scan(dest ...any) error {
	for i, cell := range r.data[r.at-1] {
		p, ok := dest[i].(*sql.NullString)
		if !ok {
			return fmt.Errorf("collect asked for %T, not *sql.NullString", dest[i])
		}
		switch v := cell.(type) {
		case nil:
			*p = sql.NullString{}
		case string:
			*p = sql.NullString{String: v, Valid: true}
		case []byte:
			*p = sql.NullString{String: string(v), Valid: true}
		default:
			*p = sql.NullString{String: fmt.Sprint(v), Valid: true}
		}
	}
	return nil
}

// sqlserverish and postgresish stand in for the two guard shapes without
// importing either engine.
var (
	sqlserverish = Guard{
		Statement: func(sql string) string {
			return sql + "\nOPTION (MAXDOP 1, MAX_GRANT_PERCENT = 1)"
		},
	}
	postgresish = Guard{
		Session: Session{ReadOnly: true, Set: []string{"SET LOCAL statement_timeout = '120s'"}},
	}
)

// No statement may reach a server without its engine's cap on it. Enforcing
// that centrally is the point of Query — an engine author cannot forget it,
// because they never apply it themselves.
func TestQueryAppliesTheGuardToEveryStatement(t *testing.T) {
	conn := &spy{}

	if _, err := Query(context.Background(), conn, sqlserverish, "SELECT 1"); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(conn.statements) != 1 {
		t.Fatalf("sent %d statements, want 1", len(conn.statements))
	}
	if !strings.Contains(conn.statements[0], "MAX_GRANT_PERCENT = 1") {
		t.Fatalf("statement reached the server without its cap: %q", conn.statements[0])
	}
}

// An engine whose whole guarantee is the session must still get that session
// arranged around the statement.
func TestQueryCarriesTheSessionGuard(t *testing.T) {
	conn := &spy{}

	if _, err := Query(context.Background(), conn, postgresish, "SELECT 1"); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !conn.sessions[0].ReadOnly {
		t.Fatal("statement ran outside a read-only transaction")
	}
	if len(conn.sessions[0].Set) == 0 {
		t.Fatal("session-local settings were dropped")
	}
	// A session-only guard must not silently mangle the statement.
	if conn.statements[0] != "SELECT 1" {
		t.Fatalf("statement = %q, want it unchanged", conn.statements[0])
	}
}

// The gate runs before the connection is touched. A refused statement must not
// reach a server to be judged there.
func TestQueryRefusesBeforeUsingTheConnection(t *testing.T) {
	conn := &spy{}

	_, err := Query(context.Background(), conn, sqlserverish, "DROP TABLE dbo.Orders")

	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("error is %T, want *RefusedError", err)
	}
	if len(conn.statements) != 0 {
		t.Fatalf("a refused statement reached the connection: %q", conn.statements)
	}
}

func TestQueryReadsCellsAsStringsAndNullAsEmpty(t *testing.T) {
	conn := &spy{rows: &fakeRows{
		columns: []string{"name", "rows", "modified"},
		data: [][]any{
			{"dbo.Orders", int64(42), nil},
			{[]byte("dbo.Customers"), int64(0), "2026-01-02"},
		},
	}}

	got, err := Query(context.Background(), conn, sqlserverish, "SELECT 1")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	want := [][]string{
		{"dbo.Orders", "42", ""},
		{"dbo.Customers", "0", "2026-01-02"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Errorf("row %d cell %d = %q, want %q", i, j, got[i][j], want[i][j])
			}
		}
	}
}

func TestBatchSplitsWithoutLosingOrDuplicating(t *testing.T) {
	cases := []struct {
		name  string
		keys  int
		size  int
		want  []int
		total int
	}{
		{"exact multiple", 80, 40, []int{40, 40}, 80},
		{"remainder", 85, 40, []int{40, 40, 5}, 85},
		{"fewer than one batch", 7, 40, []int{7}, 7},
		{"empty", 0, 40, nil, 0},
		{"single", 1, 40, []int{1}, 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			keys := make([]string, c.keys)
			for i := range keys {
				keys[i] = string(rune('a' + i%26))
			}

			batches := Batch(keys, c.size)
			if len(batches) != len(c.want) {
				t.Fatalf("got %d batches, want %d", len(batches), len(c.want))
			}
			total := 0
			for i, b := range batches {
				if len(b) != c.want[i] {
					t.Errorf("batch %d holds %d, want %d", i, len(b), c.want[i])
				}
				if len(b) > c.size {
					t.Errorf("batch %d exceeds the size cap", i)
				}
				total += len(b)
			}
			if total != c.total {
				t.Errorf("batches hold %d keys in total, want %d", total, c.total)
			}
		})
	}
}

// Placeholders and Args must agree, or a batched fetch binds the wrong number
// of parameters and fails at the server rather than here.
func TestPlaceholdersMatchArgs(t *testing.T) {
	keys := []string{"a", "b", "c"}

	if n := strings.Count(Placeholders("@p", len(keys)), ","); n != len(keys)-1 {
		t.Fatalf("placeholder list does not hold %d entries: %q", len(keys), Placeholders("@p", len(keys)))
	}
	if got := len(Args(keys)); got != len(keys) {
		t.Fatalf("Args returned %d, want %d", got, len(keys))
	}
	if Placeholders("@p", 0) != "" {
		t.Fatalf("empty placeholder list = %q, want empty", Placeholders("@p", 0))
	}
}
