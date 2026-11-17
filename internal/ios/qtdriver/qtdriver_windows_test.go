package qtdriver

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The catalog and its signature need no administrator rights; only trusting the
// certificate and installing the driver do.
func TestQTDriverCatalogIsSigned(t *testing.T) {
	dir := t.TempDir()
	inf := driverINF
	if e := os.WriteFile(filepath.Join(dir, "qt-driver.inf"), inf, 0600); e != nil {
		t.Fatal(e)
	}
	if e := qtCatalog(dir); e != nil {
		t.Fatal(e)
	}
	catalog := filepath.Join(dir, "qt-driver.cat")
	unsigned, _ := os.ReadFile(catalog)
	key, cert, _, e := qtCertificate()
	if e != nil {
		t.Fatal(e)
	}
	if e = signCatalog(catalog, key, cert); e != nil {
		t.Fatal(e)
	}
	signed, _ := os.ReadFile(catalog)
	if len(signed) <= len(unsigned) || !bytes.Contains(signed, cert.Raw) {
		t.Fatalf("catalog not signed with the certificate (%d -> %d bytes)", len(unsigned), len(signed))
	}
}
