// Package update checks GitHub releases for a newer nodeosd and stages the
// binary for the root helper to install. Verification is the release's
// SHA256SUMS asset for now; release signing is on the roadmap.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"nodeos/internal/admin"
)

type Checker struct {
	Repo    string // "owner/name"
	Current string
	DataDir string
	Admin   *admin.Client

	// apiBase is the GitHub API root; tests point it at a local server.
	apiBase string
	http    *http.Client
}

func New(repo, current, dataDir string, adm *admin.Client) *Checker {
	return &Checker{
		Repo: repo, Current: current, DataDir: dataDir, Admin: adm,
		apiBase: "https://api.github.com",
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

type Info struct {
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Newer     bool   `json:"newer"`
	Notes     string `json:"notes,omitempty"`
	AssetName string `json:"asset_name,omitempty"`
	assetURL  string
	sumsURL   string
}

func (c *Checker) assetName() string {
	return "nodeosd-linux-" + runtime.GOARCH
}

// Check queries the latest GitHub release. Works only once the repo (or at
// least its releases) is public — a 404 comes back as a friendly error.
func (c *Checker) Check(ctx context.Context) (Info, error) {
	info := Info{Current: c.Current}
	url := c.apiBase + "/repos/" + c.Repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return info, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.http.Do(req)
	if err != nil {
		return info, fmt.Errorf("reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return info, fmt.Errorf("no releases found for %s (repo private or no release published yet)", c.Repo)
	}
	if resp.StatusCode != 200 {
		return info, fmt.Errorf("GitHub API: HTTP %d", resp.StatusCode)
	}
	var rel struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return info, err
	}
	info.Latest = strings.TrimPrefix(rel.TagName, "v")
	info.Newer = VersionNewer(info.Latest, c.Current)
	if len(rel.Body) > 2000 {
		rel.Body = rel.Body[:2000] + "…"
	}
	info.Notes = rel.Body
	for _, a := range rel.Assets {
		switch a.Name {
		case c.assetName():
			info.AssetName = a.Name
			info.assetURL = a.URL
		case "SHA256SUMS":
			info.sumsURL = a.URL
		}
	}
	return info, nil
}

// Apply downloads and checksum-verifies the release binary into the staging
// path, then asks the root helper to install it and restart the service.
func (c *Checker) Apply(ctx context.Context) (*admin.Job, error) {
	info, err := c.Check(ctx)
	if err != nil {
		return nil, err
	}
	if !info.Newer {
		return nil, fmt.Errorf("already up to date (v%s)", c.Current)
	}
	if info.assetURL == "" {
		return nil, fmt.Errorf("release v%s has no %s asset", info.Latest, c.assetName())
	}
	if info.sumsURL == "" {
		return nil, fmt.Errorf("release v%s has no SHA256SUMS asset", info.Latest)
	}

	sums, err := c.fetch(ctx, info.sumsURL, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("download SHA256SUMS: %w", err)
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == info.AssetName {
			want = strings.ToLower(f[0])
		}
	}
	if want == "" {
		return nil, fmt.Errorf("SHA256SUMS has no entry for %s", info.AssetName)
	}

	bin, err := c.fetch(ctx, info.assetURL, 200<<20)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", info.AssetName, err)
	}
	got := sha256.Sum256(bin)
	if hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("checksum mismatch — aborting update")
	}

	staged := filepath.Join(c.DataDir, "staged")
	if err := os.MkdirAll(staged, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(staged, "nodeosd"), bin, 0o755); err != nil {
		return nil, err
	}
	// helper validates the staged path server-side and restarts nodeosd
	return c.Admin.Start("self-update", info.Latest)
}

func (c *Checker) fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// VersionNewer reports whether a is a strictly newer dotted version than b.
func VersionNewer(a, b string) bool {
	pa, pb := parts(a), parts(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		va, vb := 0, 0
		if i < len(pa) {
			va = pa[i]
		}
		if i < len(pb) {
			vb = pb[i]
		}
		if va != vb {
			return va > vb
		}
	}
	return false
}

func parts(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	var out []int
	for _, p := range strings.Split(v, ".") {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			break
		}
		out = append(out, n)
	}
	return out
}
