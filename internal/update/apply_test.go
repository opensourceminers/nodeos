package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"nodeos/internal/admin"
)

// fakeGitHub serves a release plus its assets. The binary and the SHA256SUMS
// entry can be made to disagree, which is the case that matters most: a
// tampered download must never reach the box.
type fakeGitHub struct {
	tag       string
	binary    []byte
	sumOverride string // when set, published instead of the real checksum
	omitAsset bool
	omitSums  bool
	status    int
	srv       *httptest.Server
}

func (f *fakeGitHub) start(t *testing.T, repo string) {
	t.Helper()
	mux := http.NewServeMux()
	assetName := "nodeosd-linux-" + runtime.GOARCH

	mux.HandleFunc("/repos/"+repo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if f.status != 0 && f.status != 200 {
			w.WriteHeader(f.status)
			return
		}
		assets := []map[string]any{}
		if !f.omitAsset {
			assets = append(assets, map[string]any{
				"name": assetName, "browser_download_url": f.srv.URL + "/dl/" + assetName,
			})
		}
		if !f.omitSums {
			assets = append(assets, map[string]any{
				"name": "SHA256SUMS", "browser_download_url": f.srv.URL + "/dl/SHA256SUMS",
			})
		}
		json.NewEncoder(w).Encode(map[string]any{
			"tag_name": f.tag, "body": "release notes", "assets": assets,
		})
	})
	mux.HandleFunc("/dl/"+assetName, func(w http.ResponseWriter, r *http.Request) {
		w.Write(f.binary)
	})
	mux.HandleFunc("/dl/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		sum := f.sumOverride
		if sum == "" {
			h := sha256.Sum256(f.binary)
			sum = hex.EncodeToString(h[:])
		}
		fmt.Fprintf(w, "%s  %s\n%s  some-other-file\n", sum, assetName, strings.Repeat("0", 64))
	})

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
}

// newChecker wires a Checker to the fake GitHub and a ready admin helper.
func newChecker(t *testing.T, gh *fakeGitHub, current string) (*Checker, string) {
	t.Helper()
	repo := "opensourceminers/nodeos"
	gh.start(t, repo)

	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "admin"), 0o755); err != nil {
		t.Fatal(err)
	}
	// the helper marker makes admin.Start enqueue instead of refusing
	if err := os.WriteFile(filepath.Join(dataDir, "admin", ".helper-ready"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(repo, current, dataDir, admin.New(dataDir))
	c.apiBase = gh.srv.URL
	return c, dataDir
}

func stagedBinary(dataDir string) string { return filepath.Join(dataDir, "staged", "nodeosd") }

func TestApplyStagesVerifiedBinaryAndQueuesJob(t *testing.T) {
	payload := []byte("#!/fake/nodeosd v0.9.0 binary payload")
	c, dataDir := newChecker(t, &fakeGitHub{tag: "v0.9.0", binary: payload}, "0.4.0")

	job, err := c.Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if job == nil || job.Name != "self-update" {
		t.Fatalf("expected a self-update job, got %+v", job)
	}

	got, err := os.ReadFile(stagedBinary(dataDir))
	if err != nil {
		t.Fatalf("binary was not staged: %v", err)
	}
	if string(got) != string(payload) {
		t.Error("staged binary differs from the downloaded asset")
	}
	fi, err := os.Stat(stagedBinary(dataDir))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("staged binary is not executable (mode %o)", fi.Mode().Perm())
	}

	// the helper must have been asked to install exactly this
	cmds, _ := filepath.Glob(filepath.Join(dataDir, "admin", "*.cmd"))
	if len(cmds) != 1 {
		t.Fatalf("expected 1 queued command, found %d", len(cmds))
	}
	b, _ := os.ReadFile(cmds[0])
	if !strings.HasPrefix(string(b), "self-update\n") {
		t.Errorf("queued command is %q, want a self-update job", string(b))
	}
}

// The critical case: a mismatching checksum must abort before anything is
// staged, so the helper can never install a tampered binary.
func TestApplyRefusesTamperedDownload(t *testing.T) {
	c, dataDir := newChecker(t, &fakeGitHub{
		tag:         "v0.9.0",
		binary:      []byte("malicious payload"),
		sumOverride: strings.Repeat("a", 64), // published sum of a different file
	}, "0.4.0")

	_, err := c.Apply(context.Background())
	if err == nil {
		t.Fatal("Apply accepted a binary whose checksum did not match")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("error should name the checksum, got: %v", err)
	}
	if _, err := os.Stat(stagedBinary(dataDir)); !os.IsNotExist(err) {
		t.Error("a binary was staged despite the checksum mismatch")
	}
	if cmds, _ := filepath.Glob(filepath.Join(dataDir, "admin", "*.cmd")); len(cmds) != 0 {
		t.Error("an install job was queued despite the checksum mismatch")
	}
}

func TestApplyRefusesWhenAlreadyCurrent(t *testing.T) {
	c, dataDir := newChecker(t, &fakeGitHub{tag: "v0.4.0", binary: []byte("payload")}, "0.4.0")

	if _, err := c.Apply(context.Background()); err == nil {
		t.Fatal("Apply ran although the installed version is current")
	}
	if cmds, _ := filepath.Glob(filepath.Join(dataDir, "admin", "*.cmd")); len(cmds) != 0 {
		t.Error("an install job was queued without a newer release")
	}
}

func TestApplyRequiresChecksumsAndArchAsset(t *testing.T) {
	t.Run("no SHA256SUMS", func(t *testing.T) {
		c, dataDir := newChecker(t, &fakeGitHub{tag: "v0.9.0", binary: []byte("payload"), omitSums: true}, "0.4.0")
		_, err := c.Apply(context.Background())
		if err == nil || !strings.Contains(err.Error(), "SHA256SUMS") {
			t.Fatalf("expected a SHA256SUMS error, got %v", err)
		}
		if _, err := os.Stat(stagedBinary(dataDir)); !os.IsNotExist(err) {
			t.Error("binary staged without published checksums")
		}
	})
	t.Run("no binary for this architecture", func(t *testing.T) {
		c, _ := newChecker(t, &fakeGitHub{tag: "v0.9.0", binary: []byte("payload"), omitAsset: true}, "0.4.0")
		_, err := c.Apply(context.Background())
		if err == nil || !strings.Contains(err.Error(), runtime.GOARCH) {
			t.Fatalf("expected an error naming the missing %s asset, got %v", runtime.GOARCH, err)
		}
	})
}

// While the repo is private the API answers 404 — that must read as guidance,
// not as a crash.
func TestCheckExplainsMissingReleases(t *testing.T) {
	c, _ := newChecker(t, &fakeGitHub{tag: "v0.9.0", binary: []byte("x"), status: 404}, "0.4.0")

	_, err := c.Check(context.Background())
	if err == nil {
		t.Fatal("expected an error for a repo without readable releases")
	}
	for _, want := range []string{"opensourceminers/nodeos", "private"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestCheckReportsNewerRelease(t *testing.T) {
	c, _ := newChecker(t, &fakeGitHub{tag: "v0.9.0", binary: []byte("payload")}, "0.4.0")

	info, err := c.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Latest != "0.9.0" || !info.Newer {
		t.Errorf("got latest=%q newer=%v, want 0.9.0/true", info.Latest, info.Newer)
	}
	if info.Current != "0.4.0" {
		t.Errorf("current = %q", info.Current)
	}
	if info.AssetName != "nodeosd-linux-"+runtime.GOARCH {
		t.Errorf("asset for this architecture not resolved: %q", info.AssetName)
	}
}
