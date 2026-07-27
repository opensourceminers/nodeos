package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"nodeos/internal/alerts"
	"nodeos/internal/config"
	"nodeos/internal/store"
)

// fakeMiner is a minimal AxeOS device: it answers /api/system/info and
// accepts an OTA upload. behaviour after the flash is scripted per test.
type fakeMiner struct {
	mu       sync.Mutex
	name     string
	flashed  bool
	uptime   int64
	hashRate float64
	// afterFlash decides what the device does once flashed:
	//   "ok"      – goes offline briefly, comes back hashing with low uptime
	//   "dead"    – never answers again
	//   "nohash"  – answers, but hashRate stays 0 (bricked update)
	afterFlash  string
	offlineLeft int
	otaCalls    int
	url         string
	srv         *http.Server
}

func startFakeMiner(t *testing.T, name, afterFlash string) *fakeMiner {
	t.Helper()
	f := &fakeMiner{name: name, uptime: 7200, hashRate: 500, afterFlash: afterFlash}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.url = ln.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/system/info", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.flashed {
			switch f.afterFlash {
			case "dead":
				http.Error(w, "rebooting", http.StatusServiceUnavailable)
				return
			case "ok":
				if f.offlineLeft > 0 {
					f.offlineLeft--
					http.Error(w, "rebooting", http.StatusServiceUnavailable)
					return
				}
				f.uptime = 20 // freshly booted
				f.hashRate = 480
			case "nohash":
				f.uptime = 20
				f.hashRate = 0
			}
		}
		json.NewEncoder(w).Encode(map[string]any{
			"hostname": f.name, "ASICModel": "BM1370",
			"hashRate": f.hashRate, "uptimeSeconds": f.uptime,
			"temp": 55.0, "power": 15.0, "stratumURL": "public-pool.io", "stratumPort": 21496,
		})
	})
	otaHandler := func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.otaCalls++
		f.flashed = true
		f.offlineLeft = 1
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}
	mux.HandleFunc("POST /api/system/OTA", otaHandler)
	mux.HandleFunc("POST /api/system/OTAWWW", otaHandler)

	f.srv = &http.Server{Handler: mux}
	go f.srv.Serve(ln)
	t.Cleanup(func() { f.srv.Close() })
	return f
}

func (f *fakeMiner) flashCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.otaCalls
}

// testFleet wires a Manager to the given fake miners and polls them online.
func testFleet(t *testing.T, miners ...*fakeMiner) (*Manager, *alerts.Feed) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	feed := alerts.NewFeed()
	m := NewManager(cfg, st, feed)
	for _, f := range miners {
		if _, err := m.AddMiner(f.url, "manual", f.name); err != nil {
			t.Fatal(err)
		}
	}
	m.pollAll(context.Background())
	for _, mv := range m.Miners(0) {
		if !mv.Online {
			t.Fatalf("fake miner %s did not come online", mv.Host)
		}
	}
	return m, feed
}

func fastRollouts(t *testing.T) {
	t.Helper()
	oldTimeout, oldInterval, oldStagger, oldDL := verifyTimeout, verifyInterval, rolloutStagger, downloadFirmware
	verifyTimeout = 2 * time.Second
	verifyInterval = 20 * time.Millisecond
	rolloutStagger = 10 * time.Millisecond
	downloadFirmware = func(ctx context.Context, url string) ([]byte, error) {
		return make([]byte, 128*1024), nil // plausible ESP-Miner image size
	}
	t.Cleanup(func() {
		verifyTimeout, verifyInterval, rolloutStagger, downloadFirmware = oldTimeout, oldInterval, oldStagger, oldDL
	})
}

const testRepo = "bitaxeorg/ESP-Miner"

var testURL = "https://github.com/" + testRepo + "/releases/download/v2.14.1/esp-miner.bin"

func waitRollout(t *testing.T, m *Manager) RolloutStatus {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if s := m.RolloutStatus(); s.Done {
			return s
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("rollout did not finish: %+v", m.RolloutStatus())
	return RolloutStatus{}
}

func deviceStates(s RolloutStatus) map[string]string {
	out := map[string]string{}
	for _, d := range s.Devices {
		out[d.Label] = d.State
	}
	return out
}

func TestRolloutHappyPath(t *testing.T) {
	fastRollouts(t)
	a := startFakeMiner(t, "axe-a", "ok")
	b := startFakeMiner(t, "axe-b", "ok")
	c := startFakeMiner(t, "axe-c", "ok")
	m, feed := testFleet(t, a, b, c)

	if err := m.StartRollout(testURL, testRepo, []string{a.url, b.url, c.url}, false); err != nil {
		t.Fatal(err)
	}
	s := waitRollout(t, m)

	if !s.OK {
		t.Fatalf("rollout not OK: %s", s.Message)
	}
	for label, state := range deviceStates(s) {
		if state != "ok" {
			t.Errorf("%s state = %q, want ok", label, state)
		}
	}
	for _, f := range []*fakeMiner{a, b, c} {
		if f.flashCount() != 1 {
			t.Errorf("%s flashed %d times, want 1", f.name, f.flashCount())
		}
	}
	if s.SHA256 == "" {
		t.Error("image checksum not recorded")
	}
	var sawCanaryOK, sawDone bool
	for _, al := range feed.List() {
		switch al.Type {
		case "firmware_canary_ok":
			sawCanaryOK = true
		case "firmware_done":
			sawDone = true
		}
	}
	if !sawCanaryOK || !sawDone {
		t.Errorf("expected canary_ok and done alerts, got canary=%v done=%v", sawCanaryOK, sawDone)
	}
}

// The whole point of the canary: a bad image must cost exactly one miner.
func TestRolloutCanaryFailureLeavesFleetUntouched(t *testing.T) {
	fastRollouts(t)
	a := startFakeMiner(t, "axe-a", "dead") // canary bricks
	b := startFakeMiner(t, "axe-b", "ok")
	c := startFakeMiner(t, "axe-c", "ok")
	m, feed := testFleet(t, a, b, c)

	// canary is the lowest host string; force a deterministic order by
	// rolling out only in the order the manager sorts them
	if err := m.StartRollout(testURL, testRepo, []string{a.url, b.url, c.url}, false); err != nil {
		t.Fatal(err)
	}
	s := waitRollout(t, m)

	if s.OK {
		t.Fatal("rollout reported success although a device never came back")
	}
	canaryLabel := ""
	for _, d := range s.Devices {
		if d.Host == s.Canary {
			canaryLabel = d.Label
		}
	}
	states := deviceStates(s)
	if states[canaryLabel] != "failed" {
		t.Errorf("canary %s state = %q, want failed", canaryLabel, states[canaryLabel])
	}
	flashedTotal := a.flashCount() + b.flashCount() + c.flashCount()
	if flashedTotal != 1 {
		t.Errorf("%d devices were flashed, want exactly 1 (the canary)", flashedTotal)
	}
	for label, state := range states {
		if label != canaryLabel && state != "pending" {
			t.Errorf("%s state = %q, want pending (fleet must stay untouched)", label, state)
		}
	}
	var critical string
	for _, al := range feed.List() {
		if al.Type == "firmware_failed" {
			critical = al.Msg
		}
	}
	if !strings.Contains(critical, "fleet untouched") {
		t.Errorf("expected a 'fleet untouched' critical alert, got %q", critical)
	}
}

// A device that boots but does not hash is a failed update, not a success.
func TestRolloutRejectsDeviceThatDoesNotHash(t *testing.T) {
	fastRollouts(t)
	a := startFakeMiner(t, "axe-a", "nohash")
	m, _ := testFleet(t, a)

	if err := m.StartRollout(testURL, testRepo, []string{a.url}, false); err != nil {
		t.Fatal(err)
	}
	s := waitRollout(t, m)
	if s.OK {
		t.Fatal("a device that never resumed hashing was accepted")
	}
	if got := deviceStates(s)["axe-a"]; got != "failed" {
		t.Errorf("state = %q, want failed", got)
	}
}

func TestRolloutRejectsForeignFirmwareURL(t *testing.T) {
	fastRollouts(t)
	a := startFakeMiner(t, "axe-a", "ok")
	m, _ := testFleet(t, a)

	for _, bad := range []string{
		"https://evil.example/esp-miner.bin",
		"http://github.com/" + testRepo + "/releases/download/v1/x.bin",
		"https://github.com/attacker/ESP-Miner/releases/download/v1/x.bin",
		"https://github.com/" + testRepo + ".evil.example/releases/download/v1/x.bin",
	} {
		if err := m.StartRollout(bad, testRepo, []string{a.url}, false); err == nil {
			t.Errorf("accepted firmware from %q", bad)
		}
	}
	if a.flashCount() != 0 {
		t.Error("a device was flashed despite the URL being rejected")
	}
}

func TestRolloutRequiresOnlineMiners(t *testing.T) {
	fastRollouts(t)
	a := startFakeMiner(t, "axe-a", "ok")
	m, _ := testFleet(t, a)

	if err := m.StartRollout(testURL, testRepo, []string{"10.9.9.9"}, false); err == nil {
		t.Error("rollout started with no online miners selected")
	}
}

func TestRolloutRefusesConcurrentRuns(t *testing.T) {
	fastRollouts(t)
	// slow the canary down so the first rollout is still running
	verifyInterval = 300 * time.Millisecond
	a := startFakeMiner(t, "axe-a", "ok")
	b := startFakeMiner(t, "axe-b", "ok")
	m, _ := testFleet(t, a, b)

	if err := m.StartRollout(testURL, testRepo, []string{a.url, b.url}, false); err != nil {
		t.Fatal(err)
	}
	if err := m.StartRollout(testURL, testRepo, []string{a.url}, false); err == nil {
		t.Error("a second rollout started while one was running")
	}
	waitRollout(t, m)
}

func TestCancelRolloutSkipsRemainingDevices(t *testing.T) {
	fastRollouts(t)
	verifyInterval = 200 * time.Millisecond
	rolloutStagger = 500 * time.Millisecond
	a := startFakeMiner(t, "axe-a", "ok")
	b := startFakeMiner(t, "axe-b", "ok")
	c := startFakeMiner(t, "axe-c", "ok")
	m, _ := testFleet(t, a, b, c)

	if err := m.StartRollout(testURL, testRepo, []string{a.url, b.url, c.url}, false); err != nil {
		t.Fatal(err)
	}
	// let the canary finish, then cancel during the stagger
	time.Sleep(700 * time.Millisecond)
	if !m.CancelRollout() {
		t.Fatal("CancelRollout returned false while a rollout was running")
	}
	s := waitRollout(t, m)
	if s.OK {
		t.Error("cancelled rollout reported success")
	}
	skipped := 0
	for _, d := range s.Devices {
		if d.State == "skipped" {
			skipped++
		}
	}
	if skipped == 0 {
		t.Errorf("cancel did not skip any device: %+v", s.Devices)
	}
	if m.CancelRollout() {
		t.Error("CancelRollout returned true although nothing is running")
	}
}

func TestFirmwareReleaseAssetClassification(t *testing.T) {
	// classification mirrors what StartRollout offers the UI; factory images
	// must never be presented as OTA-flashable
	srv := httptestServer(t, `[{
	  "tag_name":"v2.14.1","name":"v2.14.1","published_at":"2026-06-01T00:00:00Z","body":"notes",
	  "assets":[
	    {"name":"esp-miner.bin","browser_download_url":"https://example/esp-miner.bin","size":1000},
	    {"name":"www.bin","browser_download_url":"https://example/www.bin","size":2000},
	    {"name":"esp-miner-factory-204.bin","browser_download_url":"https://example/f.bin","size":3000},
	    {"name":"readme.txt","browser_download_url":"https://example/readme.txt","size":10}
	  ]},
	  {"tag_name":"v2.15.0-rc1","prerelease":true,"assets":[{"name":"esp-miner.bin","browser_download_url":"https://example/rc.bin","size":1}]}]`)

	rels, err := releasesFrom(context.Background(), srv)
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 {
		t.Fatalf("got %d releases, want 1 (prereleases must be skipped)", len(rels))
	}
	kinds := map[string]string{}
	for _, a := range rels[0].Assets {
		kinds[a.Name] = a.Kind
	}
	want := map[string]string{
		"esp-miner.bin":             "firmware",
		"www.bin":                   "www",
		"esp-miner-factory-204.bin": "factory",
	}
	for name, k := range want {
		if kinds[name] != k {
			t.Errorf("%s classified as %q, want %q", name, kinds[name], k)
		}
	}
	if _, ok := kinds["readme.txt"]; ok {
		t.Error("non-image asset was offered as firmware")
	}
}

// httptestServer serves the given body once and returns its URL.
func httptestServer(t *testing.T, body string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return "http://" + ln.Addr().String()
}
