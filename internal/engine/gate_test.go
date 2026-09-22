package engine

import (
	"errors"
	"strings"
	"testing"
)

// The gate is the single thing standing between this tool and a server it must
// never write to, so its refusals matter more than its acceptances.
func TestAssertReadOnlyRefusesAnythingItCannotProveIsARead(t *testing.T) {
	cases := []struct {
		name      string
		statement string
		refusal   Refusal
		word      string
	}{
		{"insert", "INSERT INTO dbo.Orders VALUES (1)", NotARead, ""},
		{"update", "UPDATE dbo.Orders SET Total = 0", NotARead, ""},
		{"delete", "DELETE FROM dbo.Orders", NotARead, ""},
		{"drop", "DROP TABLE dbo.Orders", NotARead, ""},
		{"truncate", "TRUNCATE TABLE dbo.Orders", NotARead, ""},
		{"exec", "EXEC dbo.DoSomething", NotARead, ""},
		{"backup", "BACKUP DATABASE AppCore TO DISK = 'x'", NotARead, ""},
		{"dbcc", "DBCC FREEPROCCACHE", NotARead, ""},

		// Fail-closed: not a write, but not provably a read either.
		{"empty", "", NotARead, ""},
		{"whitespace", "   \n\t ", NotARead, ""},
		{"leading comment", "-- harmless\nSELECT 1", NotARead, ""},
		{"declare first", "DECLARE @x int; SELECT @x", NotARead, ""},
		{"set first", "SET NOCOUNT ON; SELECT 1", NotARead, ""},

		// A write smuggled behind a leading read is the case the deny list is
		// for: these all open with SELECT and must still be refused.
		{"select into", "SELECT * INTO dbo.Copy FROM dbo.Orders", DeniedWord, "into"},
		{"trailing insert", "SELECT 1; INSERT INTO dbo.Orders VALUES (1)", DeniedWord, "insert"},
		{"trailing drop", "SELECT 1; DROP TABLE dbo.Orders", DeniedWord, "drop"},
		{"trailing exec", "SELECT 1; EXEC sp_configure", DeniedWord, "exec"},
		{"xp_cmdshell", "SELECT 1; EXEC xp_cmdshell 'dir'", DeniedWord, "exec"},
		{"cte then write", "WITH t AS (SELECT 1 AS a) DELETE FROM dbo.Orders", DeniedWord, "delete"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := AssertReadOnly(c.statement)
			if err == nil {
				t.Fatalf("accepted a statement it must refuse: %q", c.statement)
			}
			var refused *RefusedError
			if !errors.As(err, &refused) {
				t.Fatalf("error is %T, want *RefusedError", err)
			}
			if refused.Refusal != c.refusal {
				t.Errorf("refusal = %q, want %q", refused.Refusal, c.refusal)
			}
			if c.word != "" && refused.Word != c.word {
				t.Errorf("word = %q, want %q", refused.Word, c.word)
			}
		})
	}
}

func TestAssertReadOnlyAcceptsTheQueriesTheBuildActuallySends(t *testing.T) {
	cases := []struct {
		name      string
		statement string
	}{
		{"select", "SELECT name FROM sys.objects"},
		{"cte", "WITH t AS (SELECT 1 AS a) SELECT a FROM t"},
		{"leading whitespace", "\n  SELECT 1"},
		{"lowercase", "select 1"},
		// A denied word inside an identifier is not a denied statement: the
		// word boundary is what keeps ordinary catalog columns usable.
		{"create_date column", "SELECT create_date FROM sys.objects"},
		{"modify_date column", "SELECT modify_date, is_ms_shipped FROM sys.objects"},
		{"updated column", "SELECT last_updated FROM sys.tables"},
		{"deleted_on column", "SELECT deleted_on FROM dbo.Orders"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := AssertReadOnly(c.statement); err != nil {
				t.Fatalf("refused a legitimate read: %v", err)
			}
		})
	}
}

// A refusal must name the problem without becoming a place a caller parses
// prose, and must never be silent about which word tripped it.
func TestRefusedErrorNamesTheWord(t *testing.T) {
	err := AssertReadOnly("SELECT * INTO dbo.Copy FROM dbo.Orders")
	if err == nil {
		t.Fatal("accepted SELECT INTO")
	}
	if !strings.Contains(err.Error(), "into") {
		t.Fatalf("message does not name the word: %q", err.Error())
	}
}

// Every denied word must actually be reachable through the gate. A typo in the
// list would otherwise sit there looking like protection.
func TestEveryDeniedWordIsEnforced(t *testing.T) {
	for _, word := range Denied {
		t.Run(word, func(t *testing.T) {
			err := AssertReadOnly("SELECT 1; " + word + " something")
			var refused *RefusedError
			if !errors.As(err, &refused) || refused.Refusal != DeniedWord {
				t.Fatalf("%q is in Denied but does not refuse a statement containing it: %v", word, err)
			}
		})
	}
}
