package health

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFakeProc(t *testing.T, root string) {
	t.Helper()
	mk := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("proc/loadavg", "0.42 0.30 0.18 2/345 6789\n")
	mk("proc/uptime", "12345.67 23456.78\n")
	mk("proc/cpuinfo", "processor\t: 0\nmodel name\t: x\n\nprocessor\t: 1\nmodel name\t: x\n")
	mk("proc/meminfo", "MemTotal:        4000000 kB\nMemFree:          500000 kB\nMemAvailable:    2000000 kB\n")
	mk("sys/class/thermal/thermal_zone0/temp", "48500\n")
	mk("sys/class/thermal/thermal_zone1/temp", "61250\n")
}

func TestCollectParsers(t *testing.T) {
	root := t.TempDir()
	data := t.TempDir()
	writeFakeProc(t, root)

	smartJSON := `{"generated_unix": 1700000000, "disks": [
	  {"device":{"name":"/dev/nvme0"},"model_name":"TestDisk 1TB",
	   "smart_status":{"passed":true},"temperature":{"current":41},
	   "power_on_time":{"hours":1234},
	   "nvme_smart_health_information_log":{"percentage_used":3}}]}`
	if err := os.MkdirAll(filepath.Join(data, "health"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "health", "smart.json"), []byte(smartJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	s := Collect(root, data)
	if s.Load1 != 0.42 || s.Load15 != 0.18 {
		t.Errorf("loadavg parsed wrong: %+v", s)
	}
	if s.CPUCount != 2 {
		t.Errorf("cpu count = %d, want 2", s.CPUCount)
	}
	if s.MemTotalB != 4000000*1024 || s.MemAvailB != 2000000*1024 {
		t.Errorf("meminfo parsed wrong: total %d avail %d", s.MemTotalB, s.MemAvailB)
	}
	if s.UptimeS != 12345 {
		t.Errorf("uptime = %d, want 12345", s.UptimeS)
	}
	if s.CPUTempC != 61.25 {
		t.Errorf("cpu temp = %v, want hottest zone 61.25", s.CPUTempC)
	}
	if len(s.Smart) != 1 {
		t.Fatalf("smart disks = %d, want 1", len(s.Smart))
	}
	sd := s.Smart[0]
	if sd.Device != "/dev/nvme0" || sd.Model != "TestDisk 1TB" || sd.Passed == nil || !*sd.Passed ||
		sd.TempC != 41 || sd.PowerOnHours != 1234 || sd.WearPct != 3 {
		t.Errorf("smart parsed wrong: %+v", sd)
	}
	if len(s.Disks) == 0 {
		t.Error("no disks collected (statfs on / should always work)")
	}
}

func TestCollectToleratesMissingSources(t *testing.T) {
	s := Collect(t.TempDir(), t.TempDir())
	if s.Load1 != 0 || s.Smart != nil {
		t.Errorf("expected zero values on missing sources, got %+v", s)
	}
}
