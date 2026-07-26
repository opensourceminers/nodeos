package tlscert

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"
)

func TestEnsureGeneratesValidCert(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath, err := Ensure(dir, []string{"nodeos.local", "192.168.1.50"})
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(b)
	if block == nil {
		t.Fatal("no PEM block in cert file")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}

	wantDNS := map[string]bool{"nodeos.local": false, "localhost": false}
	for _, d := range cert.DNSNames {
		if _, ok := wantDNS[d]; ok {
			wantDNS[d] = true
		}
	}
	for d, found := range wantDNS {
		if !found {
			t.Errorf("SAN %q missing (have %v)", d, cert.DNSNames)
		}
	}
	foundIP := false
	for _, ip := range cert.IPAddresses {
		if ip.String() == "192.168.1.50" {
			foundIP = true
		}
	}
	if !foundIP {
		t.Errorf("IP SAN 192.168.1.50 missing (have %v)", cert.IPAddresses)
	}

	// key file must be private
	fi, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("key perms = %o, want 600", fi.Mode().Perm())
	}

	// loadable as a TLS config
	if _, err := Config(certPath, keyPath); err != nil {
		t.Fatal(err)
	}

	// second call reuses, does not regenerate
	c2, _, err := Ensure(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(c2)
	if string(b2) != string(b) {
		t.Error("certificate was regenerated on second Ensure call")
	}
}
