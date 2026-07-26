// Package node talks JSON-RPC to a local (or remote) bitcoind and condenses
// the answers into one status object for the UI.
package node

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"nodeos/internal/config"
)

type Client struct {
	cfg  config.Bitcoind
	http *http.Client

	mu     sync.Mutex
	status Status
}

type Status struct {
	Available bool    `json:"available"`
	Error     string  `json:"error,omitempty"`
	Chain     string  `json:"chain,omitempty"`
	Blocks    int64   `json:"blocks"`
	Headers   int64   `json:"headers"`
	Progress  float64 `json:"progress"` // 0..1 verification progress
	IBD       bool    `json:"ibd"`
	Pruned    bool    `json:"pruned"`
	SizeOnDisk uint64 `json:"size_on_disk"`
	Difficulty float64 `json:"difficulty"`
	NetworkHashPS float64 `json:"network_hashps"`
	Connections   int     `json:"connections"`
	ConnectionsIn int     `json:"connections_in"`
	Subversion    string  `json:"subversion,omitempty"`
	MempoolTxs    int64   `json:"mempool_txs"`
	MempoolBytes  int64   `json:"mempool_bytes"`
	CheckedAt     time.Time `json:"checked_at"`
}

func NewClient(cfg config.Bitcoind) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: 10 * time.Second}}
}

func (c *Client) credentials() (user, pass string, err error) {
	if c.cfg.RPCUser != "" {
		return c.cfg.RPCUser, c.cfg.RPCPass, nil
	}
	if c.cfg.CookieFile == "" {
		return "", "", fmt.Errorf("no rpc credentials configured")
	}
	b, err := os.ReadFile(c.cfg.CookieFile)
	if err != nil {
		return "", "", fmt.Errorf("read cookie: %w", err)
	}
	parts := strings.SplitN(strings.TrimSpace(string(b)), ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("malformed cookie file")
	}
	return parts[0], parts[1], nil
}

func (c *Client) call(ctx context.Context, method string, params []any, out any) error {
	user, pass, err := c.credentials()
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "1.0", "id": "nodeos", "method": method, "params": params,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.RPCURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.SetBasicAuth(user, pass)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var rpcResp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return fmt.Errorf("%s: HTTP %d, bad body: %w", method, resp.StatusCode, err)
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("%s: rpc error %d: %s", method, rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if out != nil {
		return json.Unmarshal(rpcResp.Result, out)
	}
	return nil
}

// Refresh queries bitcoind and caches the result; safe to call periodically.
func (c *Client) Refresh(ctx context.Context) Status {
	st := Status{CheckedAt: time.Now()}

	var bci struct {
		Chain                string  `json:"chain"`
		Blocks               int64   `json:"blocks"`
		Headers              int64   `json:"headers"`
		VerificationProgress float64 `json:"verificationprogress"`
		IBD                  bool    `json:"initialblockdownload"`
		Pruned               bool    `json:"pruned"`
		SizeOnDisk           uint64  `json:"size_on_disk"`
		Difficulty           float64 `json:"difficulty"`
	}
	if err := c.call(ctx, "getblockchaininfo", nil, &bci); err != nil {
		st.Error = err.Error()
		c.setStatus(st)
		return st
	}
	st.Available = true
	st.Chain = bci.Chain
	st.Blocks = bci.Blocks
	st.Headers = bci.Headers
	st.Progress = bci.VerificationProgress
	st.IBD = bci.IBD
	st.Pruned = bci.Pruned
	st.SizeOnDisk = bci.SizeOnDisk
	st.Difficulty = bci.Difficulty

	var ni struct {
		Subversion  string `json:"subversion"`
		Connections int    `json:"connections"`
		ConnIn      int    `json:"connections_in"`
	}
	if err := c.call(ctx, "getnetworkinfo", nil, &ni); err == nil {
		st.Subversion = strings.Trim(ni.Subversion, "/")
		st.Connections = ni.Connections
		st.ConnectionsIn = ni.ConnIn
	}

	var mi struct {
		Size  int64 `json:"size"`
		Bytes int64 `json:"bytes"`
	}
	if err := c.call(ctx, "getmempoolinfo", nil, &mi); err == nil {
		st.MempoolTxs = mi.Size
		st.MempoolBytes = mi.Bytes
	}

	var gmi struct {
		NetworkHashPS float64 `json:"networkhashps"`
	}
	if err := c.call(ctx, "getmininginfo", nil, &gmi); err == nil {
		st.NetworkHashPS = gmi.NetworkHashPS
	}

	c.setStatus(st)
	return st
}

func (c *Client) setStatus(st Status) {
	c.mu.Lock()
	c.status = st
	c.mu.Unlock()
}

// Status returns the last cached status without touching bitcoind.
func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}
