package enginetest

import "testing"

func TestReadOnlyCatchesAWrite(t *testing.T) {
	for _, bad := range []string{
		"DELETE FROM dbo.Orders",
		"SELECT * INTO dbo.Copy FROM dbo.Orders",
		"SELECT 1; DROP TABLE dbo.Orders",
		"UPDATE x SET y = 1",
	} {
		if ReadOnly(bad) == nil {
			t.Errorf("accepted a write: %q", bad)
		}
	}
	for _, good := range []string{
		"SELECT name FROM sys.objects",
		"WITH t AS (SELECT 1 AS a) SELECT a FROM t",
		"SELECT create_date, modify_date FROM sys.objects",
	} {
		if err := ReadOnly(good); err != nil {
			t.Errorf("refused a read: %q (%v)", good, err)
		}
	}
}
