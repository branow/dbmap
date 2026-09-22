package redact_test

import (
	"testing"

	"github.com/branow/dbmap/internal/redact"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		column string
		class  redact.Class
	}{
		{column: "Email", class: redact.ClassEmail},
		{column: "e_mail_address", class: redact.ClassEmail},
		{column: "HomePhone", class: redact.ClassPhone},
		{column: "MobileNumber", class: redact.ClassPhone},
		{column: "FaxLine", class: redact.ClassPhone},
		{column: "SSN", class: redact.ClassGovernment},
		{column: "TaxId", class: redact.ClassGovernment},
		{column: "DateOfBirth", class: redact.ClassBirth},
		{column: "dob", class: redact.ClassBirth},
		{column: "PasswordHash", class: redact.ClassPassword},
		{column: "ApiToken", class: redact.ClassPassword},
		{column: "FirstName", class: redact.ClassName},
		{column: "last_name", class: redact.ClassName},
		{column: "AddressLine1", class: redact.ClassAddress},
		{column: "PostalCode", class: redact.ClassAddress},
		{column: "CardNumber", class: redact.ClassFinancial},
		{column: "IBAN", class: redact.ClassFinancial},
		{column: "Latitude", class: redact.ClassLocation},
		{column: "SignatureImage", class: redact.ClassBiometric},

		{column: "OrderId"},
		{column: "CreatedAt"},
		{column: "TotalAmount"},
		{column: "StatusCode"},
		{column: "Quantity"},
		{column: "IsActive"},
		{column: "UnitPrice"},
		{column: ""},
	}

	for _, c := range cases {
		t.Run(c.column, func(t *testing.T) {
			class, ok := redact.Classify(c.column)
			if ok != (c.class != "") {
				t.Fatalf("matched=%v, want %v", ok, c.class != "")
			}
			if class != c.class {
				t.Errorf("class: got %q, want %q", class, c.class)
			}
			if redact.IsPII(c.column) != ok {
				t.Error("IsPII disagrees with Classify")
			}
		})
	}
}

// Every row must be reachable: a pattern that can never match is a rule the
// next reader will trust and that does nothing.
func TestEveryColumnRuleIsReachable(t *testing.T) {
	seen := make(map[redact.Class]bool)
	for _, column := range []string{
		"Email", "Phone", "SSN", "BirthDate", "Password",
		"FirstName", "Street", "IBAN", "Longitude", "Avatar",
	} {
		class, ok := redact.Classify(column)
		if !ok {
			t.Errorf("%q matched no rule", column)
			continue
		}
		seen[class] = true
	}
	for _, rule := range redact.Columns {
		if !seen[rule.Class] {
			t.Errorf("no sample column reaches class %q", rule.Class)
		}
	}
}
