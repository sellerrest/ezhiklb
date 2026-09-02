package agent

import (
	"crypto/x509"
	"encoding/pem"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadOrCreateEnrollmentGeneratesOnFirstBoot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "enroll")
	enrollment, created, err := LoadOrCreateEnrollment(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected first LoadOrCreateEnrollment call to report created=true")
	}
	if len(enrollment.APIKey) < 32 {
		t.Fatalf("API key looks too short: %d chars", len(enrollment.APIKey))
	}

	block, _ := pem.Decode([]byte(enrollment.CertPEM))
	if block == nil {
		t.Fatal("CertPEM did not contain a PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(cert.NotAfter) < 5*365*24*time.Hour {
		t.Fatalf("certificate expires too soon for a long-lived pinned identity: %v", cert.NotAfter)
	}
}

func TestLoadOrCreateEnrollmentIsStableAcrossRestarts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "enroll")
	first, _, err := LoadOrCreateEnrollment(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, created, err := LoadOrCreateEnrollment(dir)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("second call should reuse the existing identity, not regenerate it")
	}
	if first.APIKey != second.APIKey {
		t.Fatal("API key changed across restarts — this would break every panel that already pinned it")
	}
	if first.CertPEM != second.CertPEM {
		t.Fatal("certificate changed across restarts — this would break panel TLS pinning (TOFU)")
	}
}

func TestConnectionBlockContainsPasteableFields(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "enroll")
	enrollment, _, err := LoadOrCreateEnrollment(dir)
	if err != nil {
		t.Fatal(err)
	}
	block := enrollment.ConnectionBlock("203.0.113.5", 62050)
	for _, want := range []string{"203.0.113.5", "62050", enrollment.APIKey, "BEGIN CERTIFICATE"} {
		if !strings.Contains(block, want) {
			t.Fatalf("connection block missing %q:\n%s", want, block)
		}
	}
}
