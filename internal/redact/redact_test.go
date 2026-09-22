package redact_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/redact"
)

// ordinary is a procedure body of the kind the corpus is almost entirely made
// of: joins, a cursor-free loop, a dynamic statement, no secret anywhere. It is
// the anti-false-positive fixture. A redactor that fires on this teaches every
// reader to ignore its counts, which would cost more than it saves.
const ordinary = `CREATE PROCEDURE dbo.OrderSummaryGet
	@CustomerId int,
	@Since datetime = NULL
AS
BEGIN
	SET NOCOUNT ON;
	DECLARE @Total decimal(18,2) = 0;
	DECLARE @Statement nvarchar(max);

	IF @Since IS NULL SET @Since = DATEADD(day, -30, GETDATE());

	SELECT
		o.OrderId,
		o.Status,
		CASE WHEN o.Total > 1000 THEN 'large' ELSE 'small' END AS Bucket
	FROM dbo.Orders AS o
		INNER JOIN dbo.OrderLines AS l ON l.OrderId = o.OrderId
		LEFT JOIN dbo.OrderStatuses AS s ON s.Code = o.Status
	WHERE o.CustomerId = @CustomerId
		AND o.CreatedAt >= @Since
	GROUP BY o.OrderId, o.Status, o.Total
	HAVING SUM(l.Quantity) > 0
	ORDER BY o.OrderId DESC;

	SET @Statement = N'SELECT TOP (10) * FROM dbo.Orders';
	EXEC sp_executesql @Statement;

	SELECT @Total = SUM(l.Quantity * l.UnitPrice) FROM dbo.OrderLines AS l;
	RETURN @@ROWCOUNT;
END`

func TestText(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		want   string
		counts redact.Counts
	}{
		{
			name: "ordinary sql is left completely untouched",
			body: ordinary,
			want: ordinary,
		},
		{
			name:   "password keeps the key and drops the value",
			body:   "EXEC dbo.LinkedLogin @user = 'svc', Password = 'hunter2';",
			want:   "EXEC dbo.LinkedLogin @user = 'svc', Password = <redacted>;",
			counts: redact.Counts{redact.ClassPassword: 1},
		},
		{
			name:   "prefixed and abbreviated password parameters",
			body:   "EXEC sp_addlinkedsrvlogin @rmtpassword=Secret1, @pwd = Secret2",
			want:   "EXEC sp_addlinkedsrvlogin @rmtpassword=<redacted>, @pwd = <redacted>",
			counts: redact.Counts{redact.ClassPassword: 2},
		},
		{
			name: "connection string parts are dropped per key",
			body: "SET @conn = 'Data Source=host.example.internal;Initial Catalog=AppCore;'",
			want: "SET @conn = 'Data Source=<redacted>;Initial Catalog=<redacted>;'",
			counts: redact.Counts{
				redact.ClassConnection: 2,
			},
		},
		{
			name:   "api key",
			body:   "SET @h = 'x-api-key=ak_live_9f3a2b1c'",
			want:   "SET @h = 'x-api-key=<redacted>'",
			counts: redact.Counts{redact.ClassAPIKey: 1},
		},
		{
			name:   "bearer token",
			body:   "SET @auth = 'Bearer eyJhbGciOi.JIUzI1NiIs.InR5cCI6'",
			want:   "SET @auth = 'Bearer <redacted>'",
			counts: redact.Counts{redact.ClassBearer: 1},
		},
		{
			name: "address in a mail send call",
			body: "EXEC msdb.dbo.sp_send_dbmail @recipients = 'ops@example.internal'," +
				" @subject = 'nightly';",
			want: "EXEC msdb.dbo.sp_send_dbmail @recipients = '<email>'," +
				" @subject = 'nightly';",
			counts: redact.Counts{redact.ClassEmail: 1},
		},
		{
			name:   "every address in a distribution list is counted",
			body:   "@recipients = 'a@example.internal;b@example.internal;c@example.internal'",
			want:   "@recipients = '<email>;<email>;<email>'",
			counts: redact.Counts{redact.ClassEmail: 3},
		},
		{
			name: "counts are reported per class rather than as one number",
			body: "Password = p1; api_key = k1; Bearer abcdefghijkl; ops@example.internal",
			want: "Password = <redacted>; api_key = <redacted>; Bearer <redacted>; <email>",
			counts: redact.Counts{
				redact.ClassPassword: 1,
				redact.ClassAPIKey:   1,
				redact.ClassBearer:   1,
				redact.ClassEmail:    1,
			},
		},
		{
			name:   "a credential written as an address counts as a credential",
			body:   "password = admin@example.internal",
			want:   "password = <redacted>",
			counts: redact.Counts{redact.ClassPassword: 1},
		},
		{
			name: "a name that merely contains a key is not a key",
			body: "SELECT PasswordHash, TokenId FROM dbo.Accounts",
			want: "SELECT PasswordHash, TokenId FROM dbo.Accounts",
		},
		{
			name: "empty body",
			body: "",
			want: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := redact.Text(c.body)
			if got.String() != c.want {
				t.Errorf("text:\n got %q\nwant %q", got.String(), c.want)
			}
			if !same(got.Counts(), c.counts) {
				t.Errorf("counts: got %v, want %v", got.Counts(), c.counts)
			}
		})
	}
}

// same compares two tallies, treating an absent class and a zero count as the
// same thing: a class that never fired has nothing to report either way.
func same(got, want redact.Counts) bool {
	if got.Total() != want.Total() {
		return false
	}
	for class, n := range want {
		if got[class] != n {
			return false
		}
	}
	for class, n := range got {
		if want[class] != n {
			return false
		}
	}
	return true
}

// The baseline DESIGN.md measured: a whole corpus of ordinary SQL must produce
// nothing at all, so a single hit in a build summary means something.
func TestOrdinarySQLProducesNoCounts(t *testing.T) {
	got := redact.Text(ordinary)
	if got.Counts().Total() != 0 {
		t.Fatalf("ordinary SQL fired %d rules: %v", got.Counts().Total(), got.Counts())
	}
}

func TestSecretValueNeverSurvives(t *testing.T) {
	secrets := []string{"hunter2", "ak_live_9f3a2b1c", "eyJhbGciOi.JIUzI1NiIs.InR5cCI6"}
	body := "Password = 'hunter2'; api_key = ak_live_9f3a2b1c;" +
		" SET @a = 'Bearer eyJhbGciOi.JIUzI1NiIs.InR5cCI6'"

	got := redact.Text(body).String()
	for _, secret := range secrets {
		if strings.Contains(got, secret) {
			t.Errorf("secret %q survived redaction: %q", secret, got)
		}
	}
}

func TestCounts(t *testing.T) {
	cases := []struct {
		name  string
		left  redact.Counts
		right redact.Counts
		want  redact.Counts
		total int
	}{
		{name: "both empty", want: nil},
		{
			name:  "disjoint classes",
			left:  redact.Counts{redact.ClassEmail: 2},
			right: redact.Counts{redact.ClassPassword: 1},
			want:  redact.Counts{redact.ClassEmail: 2, redact.ClassPassword: 1},
			total: 3,
		},
		{
			name:  "same class sums",
			left:  redact.Counts{redact.ClassEmail: 5},
			right: redact.Counts{redact.ClassEmail: 2},
			want:  redact.Counts{redact.ClassEmail: 7},
			total: 7,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.left.Add(c.right)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("add: got %v, want %v", got, c.want)
			}
			if got.Total() != c.total {
				t.Errorf("total: got %d, want %d", got.Total(), c.total)
			}
			if len(c.left) != 0 && c.left.Total()+c.right.Total() != c.total {
				t.Error("add mutated an operand")
			}
		})
	}
}

// The zero Body is the only Body a caller can build without the redactor, and
// it holds nothing. That is what lets the cache demand this type.
func TestZeroBodyIsEmpty(t *testing.T) {
	var body redact.Body
	if body.String() != "" || body.Counts() != nil {
		t.Fatalf("zero body is not empty: %q %v", body.String(), body.Counts())
	}
}
