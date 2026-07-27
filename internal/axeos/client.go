// Package axeos is a client for the ESP-Miner / AxeOS REST API spoken by
// Bitaxe, NerdAxe, NerdQAxe and derivative open-source miners.
package axeos

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// FlexString tolerates firmware versions that emit a field as either a JSON
// string or a number (bestDiff has changed types across ESP-Miner releases).
type FlexString string

func (f *FlexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*f = ""
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = FlexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		*f = ""
		return nil
	}
	*f = FlexString(n.String())
	return nil
}

// FlexBool tolerates 0/1 integers as well as true/false.
type FlexBool bool

func (f *FlexBool) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch s {
	case "true", "1":
		*f = true
	default:
		*f = false
	}
	return nil
}

// Info mirrors GET /api/system/info. Every field is optional; firmware
// variants across the device zoo omit or rename fields freely.
type Info struct {
	Power             float64    `json:"power"`
	Voltage           float64    `json:"voltage"`
	Current           float64    `json:"current"`
	Temp              float64    `json:"temp"`
	VRTemp            float64    `json:"vrTemp"`
	HashRate          float64    `json:"hashRate"` // GH/s
	ExpectedHashrate  float64    `json:"expectedHashrate"`
	BestDiff          FlexString `json:"bestDiff"`
	BestSessionDiff   FlexString `json:"bestSessionDiff"`
	IsUsingFallback   FlexBool   `json:"isUsingFallbackStratum"`
	StratumURL        string     `json:"stratumURL"`
	StratumPort       int        `json:"stratumPort"`
	StratumUser       string     `json:"stratumUser"`
	FallbackURL       string     `json:"fallbackStratumURL"`
	FallbackPort      int        `json:"fallbackStratumPort"`
	FallbackUser      string     `json:"fallbackStratumUser"`
	SharesAccepted    int64      `json:"sharesAccepted"`
	SharesRejected    int64      `json:"sharesRejected"`
	UptimeSeconds     int64      `json:"uptimeSeconds"`
	ASICModel         string     `json:"ASICModel"`
	ASICCount         int        `json:"asicCount"`
	SmallCoreCount    int        `json:"smallCoreCount"`
	Hostname          string     `json:"hostname"`
	MacAddr           string     `json:"macAddr"`
	SSID              string     `json:"ssid"`
	WifiStatus        string     `json:"wifiStatus"`
	Frequency         float64    `json:"frequency"`
	CoreVoltage       float64    `json:"coreVoltage"`
	CoreVoltageActual float64    `json:"coreVoltageActual"`
	FanSpeed          float64    `json:"fanspeed"`
	FanRPM            float64    `json:"fanrpm"`
	AutoFanSpeed      FlexBool   `json:"autofanspeed"`
	OverheatMode      FlexBool   `json:"overheat_mode"`
	Version           string     `json:"version"`
	BoardVersion      string     `json:"boardVersion"`
	RunningPartition  string     `json:"runningPartition"`
	FreeHeap          int64      `json:"freeHeap"`
}

// IsMiner reports whether the response plausibly came from an ESP-Miner
// device rather than some other web server that answered on port 80.
func (i *Info) IsMiner() bool {
	return i.ASICModel != "" || i.SmallCoreCount > 0 || (i.HashRate > 0 && i.StratumURL != "")
}

// ParseDiff converts an AxeOS difficulty string like "3.29M" or "412k" to a
// float. Returns 0 when unparseable.
func ParseDiff(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	mult := 1.0
	switch s[len(s)-1] {
	case 'k', 'K':
		mult, s = 1e3, s[:len(s)-1]
	case 'M':
		mult, s = 1e6, s[:len(s)-1]
	case 'G':
		mult, s = 1e9, s[:len(s)-1]
	case 'T':
		mult, s = 1e12, s[:len(s)-1]
	case 'P':
		mult, s = 1e15, s[:len(s)-1]
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v * mult
}

type Client struct {
	http *http.Client
}

func NewClient(timeout time.Duration) *Client {
	return &Client{http: &http.Client{Timeout: timeout}}
}

func (c *Client) url(host, path string) string {
	if !strings.Contains(host, ":") {
		host += ":80"
	}
	return "http://" + host + path
}

func (c *Client) GetInfo(ctx context.Context, host string) (*Info, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url(host, "/api/system/info"), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", host, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var info Info
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("%s: bad JSON: %w", host, err)
	}
	return &info, nil
}

// PatchSystem updates device settings. AxeOS applies stratum changes only
// after a restart; callers that change pool settings should Restart after.
func (c *Client) PatchSystem(ctx context.Context, host string, fields map[string]any) error {
	b, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, c.url(host, "/api/system"), bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s: HTTP %d", host, resp.StatusCode)
	}
	return nil
}

// OTA uploads a firmware image. path is "/api/system/OTA" for the miner
// firmware and "/api/system/OTAWWW" for the web interface; the device
// reboots itself when the flash completes.
//
// This is the one call in NodeOS that can brick hardware, so it is wrapped
// in a long timeout and never retried automatically.
func (c *Client) OTA(ctx context.Context, host, path string, image []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(host, path), bytes.NewReader(image))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = int64(len(image))
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s: HTTP %d: %s", host, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *Client) Restart(ctx context.Context, host string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(host, "/api/system/restart"), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s: HTTP %d", host, resp.StatusCode)
	}
	return nil
}
