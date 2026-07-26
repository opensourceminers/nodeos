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

	// richer detail for the node page
	PeersOut       int     `json:"peers_out"`
	Uptime         int64   `json:"uptime_s"`
	TipTime        int64   `json:"tip_time"`
	TipHash        string  `json:"tip_hash,omitempty"`
	MempoolUsage   int64   `json:"mempool_usage"`
	MempoolMax     int64   `json:"mempool_max"`
	MempoolMinFee  float64 `json:"mempool_min_fee"`
	PruneTargetB   uint64  `json:"prune_target_b"`
	PruneHeight    int64   `json:"prune_height"`
	BytesRecv      int64   `json:"bytes_recv"`
	BytesSent      int64   `json:"bytes_sent"`
	Warnings       string  `json:"warnings,omitempty"`
	ProtocolVer    int64   `json:"protocol_version,omitempty"`
}

// Peer is the condensed peer view for the node page.
type Peer struct {
	ID         int64   `json:"id"`
	Addr       string  `json:"addr"`
	Network    string  `json:"network"`
	Subver     string  `json:"subver"`
	Inbound    bool    `json:"inbound"`
	PingMS     float64 `json:"ping_ms"`
	BytesSent  int64   `json:"bytes_sent"`
	BytesRecv  int64   `json:"bytes_recv"`
	ConnectedS int64   `json:"connected_s"`
	Height     int64   `json:"height"`
	Relay      bool    `json:"relay"`
}

// flexWarnings copes with bitcoind's warnings field, a string in older
// versions and an array of strings since v29.
type flexWarnings string

func (f *flexWarnings) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = flexWarnings(s)
		return nil
	}
	var arr []string
	if err := json.Unmarshal(b, &arr); err == nil {
		*f = flexWarnings(strings.Join(arr, " · "))
		return nil
	}
	*f = ""
	return nil
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
		Chain                string       `json:"chain"`
		Blocks               int64        `json:"blocks"`
		Headers              int64        `json:"headers"`
		VerificationProgress float64      `json:"verificationprogress"`
		IBD                  bool         `json:"initialblockdownload"`
		Pruned               bool         `json:"pruned"`
		SizeOnDisk           uint64       `json:"size_on_disk"`
		Difficulty           float64      `json:"difficulty"`
		Time                 int64        `json:"time"`
		BestBlockHash        string       `json:"bestblockhash"`
		PruneTargetSize      uint64       `json:"prune_target_size"`
		PruneHeight          int64        `json:"pruneheight"`
		Warnings             flexWarnings `json:"warnings"`
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
	st.TipTime = bci.Time
	st.TipHash = bci.BestBlockHash
	st.PruneTargetB = bci.PruneTargetSize
	st.PruneHeight = bci.PruneHeight
	st.Warnings = string(bci.Warnings)

	var ni struct {
		Subversion  string       `json:"subversion"`
		Version     int64        `json:"protocolversion"`
		Connections int          `json:"connections"`
		ConnIn      int          `json:"connections_in"`
		ConnOut     int          `json:"connections_out"`
		Warnings    flexWarnings `json:"warnings"`
	}
	if err := c.call(ctx, "getnetworkinfo", nil, &ni); err == nil {
		st.Subversion = strings.Trim(ni.Subversion, "/")
		st.Connections = ni.Connections
		st.ConnectionsIn = ni.ConnIn
		st.PeersOut = ni.ConnOut
		st.ProtocolVer = ni.Version
		if st.Warnings == "" {
			st.Warnings = string(ni.Warnings)
		}
	}

	var mi struct {
		Size       int64   `json:"size"`
		Bytes      int64   `json:"bytes"`
		Usage      int64   `json:"usage"`
		MaxMempool int64   `json:"maxmempool"`
		MinFee     float64 `json:"mempoolminfee"`
	}
	if err := c.call(ctx, "getmempoolinfo", nil, &mi); err == nil {
		st.MempoolTxs = mi.Size
		st.MempoolBytes = mi.Bytes
		st.MempoolUsage = mi.Usage
		st.MempoolMax = mi.MaxMempool
		st.MempoolMinFee = mi.MinFee
	}

	var uptime int64
	if err := c.call(ctx, "uptime", nil, &uptime); err == nil {
		st.Uptime = uptime
	}

	var nt struct {
		Recv int64 `json:"totalbytesrecv"`
		Sent int64 `json:"totalbytessent"`
	}
	if err := c.call(ctx, "getnettotals", nil, &nt); err == nil {
		st.BytesRecv = nt.Recv
		st.BytesSent = nt.Sent
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

// Peers fetches the current peer list. Queried on demand — it is far too
// large and volatile for the status snapshot pushed over SSE.
func (c *Client) Peers(ctx context.Context) ([]Peer, error) {
	var raw []struct {
		ID       int64   `json:"id"`
		Addr     string  `json:"addr"`
		Network  string  `json:"network"`
		Subver   string  `json:"subver"`
		Inbound  bool    `json:"inbound"`
		PingTime float64 `json:"pingtime"`
		BytesS   int64   `json:"bytessent"`
		BytesR   int64   `json:"bytesrecv"`
		ConnTime int64   `json:"conntime"`
		Height   int64   `json:"synced_blocks"`
		Relay    bool    `json:"relaytxes"`
	}
	if err := c.call(ctx, "getpeerinfo", nil, &raw); err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	out := make([]Peer, 0, len(raw))
	for _, p := range raw {
		out = append(out, Peer{
			ID: p.ID, Addr: p.Addr, Network: p.Network,
			Subver:  strings.Trim(p.Subver, "/"),
			Inbound: p.Inbound, PingMS: p.PingTime * 1000,
			BytesSent: p.BytesS, BytesRecv: p.BytesR,
			ConnectedS: now - p.ConnTime, Height: p.Height, Relay: p.Relay,
		})
	}
	return out, nil
}
