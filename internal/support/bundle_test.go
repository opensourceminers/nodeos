package support

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundleContentsAndRedaction(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := `{
	  "listen": ":80",
	  "bitcoind": {"rpc_url": "http://127.0.0.1:8332", "rpc_user": "u", "rpc_pass": "SUPERSECRET", "cookie_file": "/var/lib/bitcoind/.cookie"},
	  "tls": {"cert_file": "/var/lib/nodeos/tls/cert.pem", "key_file": "/var/lib/nodeos/tls/key.pem"}
	}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	err := Write(&buf, Sources{
		Version:    "0.0-test",
		Status:     func() any { return map[string]any{"fleet": map[string]int{"count": 2}} },
		Health:     func() any { return map[string]float64{"load1": 0.5} },
		WorkLog:    func() []string { return []string{"line1", "line2"} },
		ConfigPath: cfgPath,
	})
	if err != nil {
		t.Fatal(err)
	}

	gz, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	files := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		files[hdr.Name] = string(b)
	}

	for _, want := range []string{"meta.txt", "status.json", "health.json", "work-engine.log", "config.redacted.json"} {
		if _, ok := files["nodeos-support/"+want]; !ok {
			t.Errorf("bundle missing %s (have %v)", want, keys(files))
		}
	}
	red := files["nodeos-support/config.redacted.json"]
	if strings.Contains(red, "SUPERSECRET") {
		t.Fatal("rpc_pass leaked into the bundle")
	}
	if !strings.Contains(red, "REDACTED") {
		t.Error("expected REDACTED marker in config")
	}
	// paths (cookie_file, key_file) are not secrets and must survive
	if !strings.Contains(red, "/var/lib/bitcoind/.cookie") || !strings.Contains(red, "key.pem") {
		t.Errorf("file paths should not be redacted:\n%s", red)
	}
	if !strings.Contains(files["nodeos-support/work-engine.log"], "line2") {
		t.Error("work log content missing")
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
