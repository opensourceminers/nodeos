package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"nodeos/internal/alerts"
)

// Firmware rollout: NodeOS flashes ONE device first, waits for it to come
// back and actually hash, and only then continues with the rest. A canary
// that fails stops the rollout — the whole point is that a bad image costs
// one miner, not the fleet.

// Timings are variables so tests can run a rollout in milliseconds.
var (
	// how long a device may take to reboot and report hashrate again
	verifyTimeout  = 5 * time.Minute
	verifyInterval = 10 * time.Second
	// pause between devices once the canary proved the image
	rolloutStagger = 15 * time.Second

	// downloadFirmware is swapped out in tests; production always fetches
	// over HTTPS from the release URL validated in StartRollout.
	downloadFirmware = download
)

type RolloutDevice struct {
	Host   string `json:"host"`
	Label  string `json:"label"`
	State  string `json:"state"` // pending | flashing | verifying | ok | failed | skipped
	Detail string `json:"detail,omitempty"`
}

type RolloutStatus struct {
	Running   bool            `json:"running"`
	Firmware  string          `json:"firmware"`
	SHA256    string          `json:"sha256"`
	Canary    string          `json:"canary,omitempty"`
	Devices   []RolloutDevice `json:"devices"`
	Message   string          `json:"message,omitempty"`
	StartedAt time.Time       `json:"started_at,omitzero"`
	Done      bool            `json:"done"`
	OK        bool            `json:"ok"`
}

type rollout struct {
	mu     sync.Mutex
	status RolloutStatus
	cancel context.CancelFunc
}

func (r *rollout) snapshot() RolloutStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := r.status
	cp.Devices = append([]RolloutDevice(nil), r.status.Devices...)
	return cp
}

func (r *rollout) set(fn func(*RolloutStatus)) {
	r.mu.Lock()
	fn(&r.status)
	r.mu.Unlock()
}

func (r *rollout) setDevice(host, state, detail string) {
	r.mu.Lock()
	for i := range r.status.Devices {
		if r.status.Devices[i].Host == host {
			r.status.Devices[i].State = state
			r.status.Devices[i].Detail = detail
		}
	}
	r.mu.Unlock()
}

// RolloutStatus exposes the current (or last) rollout to the API.
func (m *Manager) RolloutStatus() RolloutStatus { return m.rollout.snapshot() }

// CancelRollout stops after the device currently being flashed.
func (m *Manager) CancelRollout() bool {
	m.rollout.mu.Lock()
	defer m.rollout.mu.Unlock()
	if !m.rollout.status.Running || m.rollout.cancel == nil {
		return false
	}
	m.rollout.cancel()
	return true
}

// FirmwareAsset is one downloadable file of an ESP-Miner release.
type FirmwareAsset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
	// Kind is "firmware" (esp-miner.bin), "www" (www.bin) or "factory"
	// (full-flash image — NOT usable over OTA).
	Kind string `json:"kind"`
}

type FirmwareRelease struct {
	Tag       string          `json:"tag"`
	Name      string          `json:"name"`
	Published string          `json:"published"`
	Notes     string          `json:"notes,omitempty"`
	Assets    []FirmwareAsset `json:"assets"`
}

// Releases lists ESP-Miner firmware releases from GitHub. Public repo, no
// token needed; failures are surfaced rather than cached away.
func Releases(ctx context.Context, repo string) ([]FirmwareRelease, error) {
	return releasesFrom(ctx, "https://api.github.com/repos/"+repo+"/releases?per_page=10")
}

// releasesFrom does the fetching and classification; split out so tests can
// point it at a local server instead of GitHub.
func releasesFrom(ctx context.Context, apiURL string) ([]FirmwareRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API: HTTP %d", resp.StatusCode)
	}
	var raw []struct {
		TagName     string `json:"tag_name"`
		Name        string `json:"name"`
		PublishedAt string `json:"published_at"`
		Body        string `json:"body"`
		Prerelease  bool   `json:"prerelease"`
		Assets      []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	var out []FirmwareRelease
	for _, r := range raw {
		if r.Prerelease {
			continue
		}
		rel := FirmwareRelease{Tag: r.TagName, Name: r.Name, Published: r.PublishedAt}
		if len(r.Body) > 1200 {
			r.Body = r.Body[:1200] + "…"
		}
		rel.Notes = r.Body
		for _, a := range r.Assets {
			kind := ""
			switch {
			case strings.Contains(a.Name, "factory"):
				kind = "factory"
			case strings.HasPrefix(a.Name, "www"):
				kind = "www"
			case strings.HasSuffix(a.Name, ".bin"):
				kind = "firmware"
			}
			if kind == "" {
				continue
			}
			rel.Assets = append(rel.Assets, FirmwareAsset{Name: a.Name, URL: a.URL, Size: a.Size, Kind: kind})
		}
		if len(rel.Assets) > 0 {
			out = append(out, rel)
		}
	}
	return out, nil
}

// StartRollout downloads the image once and flashes it to the given hosts,
// canary first. Only assets from the configured GitHub repo are accepted —
// an arbitrary URL would let a compromised UI push arbitrary firmware.
func (m *Manager) StartRollout(url, repo string, hosts []string, www bool) error {
	if !strings.HasPrefix(url, "https://github.com/"+repo+"/releases/download/") {
		return fmt.Errorf("firmware must come from the %s releases", repo)
	}
	m.rollout.mu.Lock()
	if m.rollout.status.Running {
		m.rollout.mu.Unlock()
		return fmt.Errorf("a rollout is already running")
	}
	m.rollout.mu.Unlock()

	// only devices that are online right now
	online := map[string]*Miner{}
	m.mu.RLock()
	for _, mi := range m.miners {
		if mi.Online {
			online[mi.Host] = mi
		}
	}
	m.mu.RUnlock()

	var devices []RolloutDevice
	for _, h := range hosts {
		mi, ok := online[h]
		if !ok {
			continue
		}
		devices = append(devices, RolloutDevice{Host: h, Label: mi.Label(), State: "pending"})
	}
	if len(devices) == 0 {
		return fmt.Errorf("no online miners selected")
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Host < devices[j].Host })

	ctx, cancel := context.WithCancel(context.Background())
	name := url[strings.LastIndex(url, "/")+1:]
	m.rollout.set(func(s *RolloutStatus) {
		*s = RolloutStatus{
			Running: true, Firmware: name, Devices: devices,
			Canary: devices[0].Host, StartedAt: time.Now(),
			Message: "downloading firmware…",
		}
	})
	m.rollout.mu.Lock()
	m.rollout.cancel = cancel
	m.rollout.mu.Unlock()

	go m.runRollout(ctx, url, www)
	return nil
}

func (m *Manager) runRollout(ctx context.Context, url string, www bool) {
	defer func() {
		m.rollout.set(func(s *RolloutStatus) { s.Running = false; s.Done = true })
	}()

	image, err := downloadFirmware(ctx, url)
	if err != nil {
		m.rollout.set(func(s *RolloutStatus) { s.Message = "download failed: " + err.Error() })
		m.feed.Add(alerts.Critical, "firmware_failed", "", "Firmware download failed: "+err.Error())
		return
	}
	sum := sha256.Sum256(image)
	hexsum := hex.EncodeToString(sum[:])
	path := "/api/system/OTA"
	if www {
		path = "/api/system/OTAWWW"
	}
	m.rollout.set(func(s *RolloutStatus) {
		s.SHA256 = hexsum
		s.Message = fmt.Sprintf("flashing canary (%s)…", s.Canary)
	})
	m.feed.Add(alerts.Info, "firmware_start", "",
		fmt.Sprintf("Firmware rollout started: %d device(s), canary first", len(m.rollout.snapshot().Devices)))

	devices := m.rollout.snapshot().Devices
	for i, d := range devices {
		select {
		case <-ctx.Done():
			m.rollout.setDevice(d.Host, "skipped", "cancelled")
			continue
		default:
		}

		m.rollout.setDevice(d.Host, "flashing", "")
		if err := m.client.OTA(ctx, d.Host, path, image); err != nil {
			m.rollout.setDevice(d.Host, "failed", err.Error())
			m.stopRollout(i == 0, d.Label, err.Error())
			return
		}
		m.rollout.setDevice(d.Host, "verifying", "waiting for the device to come back")

		if err := m.verifyBack(ctx, d.Host); err != nil {
			m.rollout.setDevice(d.Host, "failed", err.Error())
			m.stopRollout(i == 0, d.Label, err.Error())
			return
		}
		m.rollout.setDevice(d.Host, "ok", "")

		if i == 0 && len(devices) > 1 {
			m.rollout.set(func(s *RolloutStatus) {
				s.Message = "canary verified — continuing with the fleet"
			})
			m.feed.Add(alerts.Info, "firmware_canary_ok", d.Host,
				fmt.Sprintf("Canary %s is back and hashing — rolling out to the rest", d.Label))
		}
		if i+1 < len(devices) {
			select {
			case <-ctx.Done():
			case <-time.After(rolloutStagger):
			}
		}
	}
	// A cancelled run must never report success: the skipped devices still
	// run the old firmware and the operator has to see that.
	if ctx.Err() != nil {
		skipped := 0
		for _, d := range m.rollout.snapshot().Devices {
			if d.State == "skipped" {
				skipped++
			}
		}
		msg := fmt.Sprintf("Rollout cancelled — %d device(s) still on the old firmware", skipped)
		m.rollout.set(func(s *RolloutStatus) { s.Message = msg })
		m.feed.Add(alerts.Warning, "firmware_cancelled", "", msg)
		return
	}
	m.rollout.set(func(s *RolloutStatus) { s.OK = true; s.Message = "rollout complete" })
	m.feed.Add(alerts.Info, "firmware_done", "", "Firmware rollout finished successfully")
}

func (m *Manager) stopRollout(wasCanary bool, label, reason string) {
	msg := fmt.Sprintf("Firmware rollout stopped at %s: %s", label, reason)
	if wasCanary {
		msg = fmt.Sprintf("Canary %s failed (%s) — fleet untouched", label, reason)
	}
	m.rollout.set(func(s *RolloutStatus) { s.Message = msg })
	m.feed.Add(alerts.Critical, "firmware_failed", "", msg)
}

// verifyBack waits until the device answers again AND reports hashrate — a
// device that boots but does not hash is a failed update.
func (m *Manager) verifyBack(ctx context.Context, host string) error {
	deadline := time.Now().Add(verifyTimeout)
	sawOffline := false
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return fmt.Errorf("cancelled")
		case <-time.After(verifyInterval):
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		info, err := m.client.GetInfo(cctx, host)
		cancel()
		if err != nil {
			sawOffline = true
			continue
		}
		// require a reboot to have happened (short uptime) or hashing to
		// have resumed, so we never mistake "not rebooted yet" for success
		if info.HashRate > 0 && (sawOffline || info.UptimeSeconds < 180) {
			return nil
		}
	}
	return fmt.Errorf("did not come back hashing within %s", verifyTimeout)
}

func download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	// ESP32 images are a few MB; the cap keeps a bad URL from eating RAM
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if len(b) < 64*1024 {
		return nil, fmt.Errorf("suspiciously small image (%d bytes)", len(b))
	}
	return b, nil
}
