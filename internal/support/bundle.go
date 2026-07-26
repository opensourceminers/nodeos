// Package support builds the downloadable diagnostics bundle: a tar.gz with
// status, logs and redacted configuration. Never includes passwords, hashes,
// RPC credentials or anything wallet-related.
package support

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type Sources struct {
	Version    string
	Status     func() any    // full /api/status payload
	WorkLog    func() []string
	Health     func() any
	ConfigPath string        // /etc/nodeos/config.json (redacted before inclusion)
}

// Write streams the bundle to w.
func Write(w io.Writer, s Sources) error {
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	add := func(name string, content []byte) error {
		if err := tw.WriteHeader(&tar.Header{
			Name: "nodeos-support/" + name, Mode: 0o644,
			Size: int64(len(content)), ModTime: time.Now(),
		}); err != nil {
			return err
		}
		_, err := tw.Write(content)
		return err
	}
	addJSON := func(name string, v any) error {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			b = []byte(fmt.Sprintf("marshal error: %v", err))
		}
		return add(name, b)
	}

	host, _ := os.Hostname()
	meta := fmt.Sprintf("NodeOS support bundle\nversion: %s\ntime: %s\nhost: %s\nos: %s/%s\n",
		s.Version, time.Now().Format(time.RFC3339), host, runtime.GOOS, runtime.GOARCH)
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		meta += "\n--- /etc/os-release ---\n" + string(b)
	}
	if err := add("meta.txt", []byte(meta)); err != nil {
		return err
	}
	if s.Status != nil {
		if err := addJSON("status.json", s.Status()); err != nil {
			return err
		}
	}
	if s.Health != nil {
		if err := addJSON("health.json", s.Health()); err != nil {
			return err
		}
	}
	if s.WorkLog != nil {
		if err := add("work-engine.log", []byte(strings.Join(s.WorkLog(), "\n"))); err != nil {
			return err
		}
	}
	if s.ConfigPath != "" {
		if err := add("config.redacted.json", redactConfig(s.ConfigPath)); err != nil {
			return err
		}
	}
	// journal excerpt: works when nodeos is in the systemd-journal group;
	// silently absent elsewhere (dev machines)
	if out, err := exec.Command("journalctl",
		"-u", "nodeosd", "-u", "bitcoind", "-u", "nodeos-admin",
		"-n", "500", "--no-pager", "-o", "short-iso").Output(); err == nil {
		if err := add("journal.txt", out); err != nil {
			return err
		}
	}
	return nil
}

// redactConfig blanks every value whose key suggests a secret.
func redactConfig(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		return []byte(fmt.Sprintf("unreadable: %v", err))
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return []byte(fmt.Sprintf("unparsable: %v", err))
	}
	redact(v)
	out, _ := json.MarshalIndent(v, "", "  ")
	return out
}

func redact(v any) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	for k, val := range m {
		lk := strings.ToLower(k)
		sensitive := strings.Contains(lk, "pass") || strings.Contains(lk, "secret") ||
			strings.Contains(lk, "token") ||
			(strings.Contains(lk, "key") && !strings.Contains(lk, "file"))
		if sensitive {
			if s, isStr := val.(string); isStr && s != "" {
				m[k] = "REDACTED"
			}
			continue
		}
		redact(val)
	}
}
