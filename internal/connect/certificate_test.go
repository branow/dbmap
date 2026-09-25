package connect

import (
	"errors"
	"strings"
	"testing"
)

// dbmap verifies the chain by default, so against an internal certificate
// authority this is the first failure a new user meets. The driver's own words
// name the mechanism and no way out, so the failure is classified and carries
// the two choices that actually exist.
func TestACertificateFailureNamesTheWayOut(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"go x509", "x509: certificate signed by unknown authority"},
		{"hostname mismatch", `x509: certificate is valid for db1, not db.example.internal`},
		{"expired", "tls: failed to verify certificate: x509: certificate has expired"},
		{"self signed", "SSL error: self-signed certificate in certificate chain"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := diagnose(errors.New(c.text), "db.example.internal")

			var cert *CertificateError
			if !errors.As(got, &cert) {
				t.Fatalf("diagnose returned %T, want *CertificateError", got)
			}
			if !strings.Contains(got.Error(), "db.example.internal") {
				t.Errorf("the host is not named: %v", got)
			}
			if !strings.Contains(cert.Remedy(), "--trust-server-certificate") {
				t.Errorf("the remedy does not name the flag: %s", cert.Remedy())
			}
			// Dropping encryption would also make the error go away, and must
			// never be the advice this prints.
			if strings.Contains(cert.Remedy(), "encrypt=false") {
				t.Errorf("the remedy offers to turn encryption off: %s", cert.Remedy())
			}
		})
	}
}

// A login the server actively rejected must keep reporting as auth: the two
// failures have different fixes and the certificate rule is loose on purpose.
func TestALoginRejectionIsStillAuth(t *testing.T) {
	got := diagnose(errors.New("login failed for user 'reader'"), "db.example.internal")

	var cert *CertificateError
	if errors.As(got, &cert) {
		t.Fatalf("a rejected login was classified as a certificate failure: %v", got)
	}
	var rejected *RejectedError
	if !errors.As(got, &rejected) {
		t.Fatalf("diagnose returned %T, want *RejectedError", got)
	}
}
