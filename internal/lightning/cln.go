// Package lightning talks to Core Lightning through its clnrest plugin
// (localhost only). Authentication is a rune minted by the root helper and
// stored group-readable; nodeosd never touches the RPC socket itself.
package lightning

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type Client struct {
	baseURL  string
	runeFile string
	http     *http.Client
}

func NewClient(baseURL, runeFile string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		runeFile: runeFile,
		http:     &http.Client{Timeout: 15 * time.Second},
	}
}

// Connected reports whether a rune is available (the panel's gate).
func (c *Client) Connected() bool {
	_, err := os.Stat(c.runeFile)
	return err == nil
}

func (c *Client) call(ctx context.Context, method string, params map[string]any, out any) error {
	runeB, err := os.ReadFile(c.runeFile)
	if err != nil {
		return fmt.Errorf("no access rune yet — connect the Lightning panel first")
	}
	if params == nil {
		params = map[string]any{}
	}
	body, _ := json.Marshal(params)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Rune", strings.TrimSpace(string(runeB)))
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("clnrest unreachable: %w", err)
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	// clnrest answers POSTs with 201, not 200 — accept the whole 2xx class
	if resp.StatusCode/100 != 2 {
		var e struct {
			Message string `json:"message"`
			Error   any    `json:"error"`
		}
		_ = dec.Decode(&e)
		if e.Message == "" && e.Error != nil {
			return fmt.Errorf("cln: %v", e.Error)
		}
		if e.Message == "" {
			return fmt.Errorf("cln: HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("cln: %s", e.Message)
	}
	if out != nil {
		return dec.Decode(out)
	}
	return nil
}

// ---------- aggregated status for the panel ----------

type Channel struct {
	PeerID     string `json:"peer_id"`
	ShortID    string `json:"short_id,omitempty"`
	State      string `json:"state"`
	TotalMsat  int64  `json:"total_msat"`
	OursMsat   int64  `json:"ours_msat"`
	Spendable  int64  `json:"spendable_msat"`
	Receivable int64  `json:"receivable_msat"`
	Connected  bool   `json:"connected"`
}

type Info struct {
	Available     bool   `json:"available"`
	Connected     bool   `json:"connected"` // rune present
	Error         string `json:"error,omitempty"`
	ID            string `json:"id,omitempty"`
	Alias         string `json:"alias,omitempty"`
	Color         string `json:"color,omitempty"`
	BlockHeight   int64  `json:"blockheight,omitempty"`
	SyncWarning   string `json:"sync_warning,omitempty"`
	NumPeers      int    `json:"num_peers"`
	OnchainMsat   int64  `json:"onchain_msat"`
	OnchainUnconf int64  `json:"onchain_unconf_msat"`
	ChanSpendable int64  `json:"chan_spendable_msat"`
	ChanReceivable int64 `json:"chan_receivable_msat"`
	Channels      []Channel `json:"channels"`
}

// amountMsat tolerates CLN's two encodings: plain integer msat (modern) and
// the legacy "1234msat" string.
type amountMsat int64

func (a *amountMsat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), "\"")
	s = strings.TrimSuffix(s, "msat")
	var v int64
	_, err := fmt.Sscan(s, &v)
	if err != nil {
		*a = 0
		return nil
	}
	*a = amountMsat(v)
	return nil
}

func (c *Client) Status(ctx context.Context) Info {
	info := Info{Connected: c.Connected()}
	if !info.Connected {
		return info
	}

	var gi struct {
		ID          string `json:"id"`
		Alias       string `json:"alias"`
		Color       string `json:"color"`
		BlockHeight int64  `json:"blockheight"`
		NumPeers    int    `json:"num_peers"`
		WarnBTC     string `json:"warning_bitcoind_sync"`
		WarnLN      string `json:"warning_lightningd_sync"`
	}
	if err := c.call(ctx, "getinfo", nil, &gi); err != nil {
		info.Error = err.Error()
		return info
	}
	info.Available = true
	info.ID = gi.ID
	info.Alias = gi.Alias
	info.Color = gi.Color
	info.BlockHeight = gi.BlockHeight
	info.NumPeers = gi.NumPeers
	if gi.WarnBTC != "" {
		info.SyncWarning = gi.WarnBTC
	} else if gi.WarnLN != "" {
		info.SyncWarning = gi.WarnLN
	}

	var lf struct {
		Outputs []struct {
			Amount amountMsat `json:"amount_msat"`
			Status string     `json:"status"`
		} `json:"outputs"`
	}
	if err := c.call(ctx, "listfunds", nil, &lf); err == nil {
		for _, o := range lf.Outputs {
			if o.Status == "confirmed" {
				info.OnchainMsat += int64(o.Amount)
			} else {
				info.OnchainUnconf += int64(o.Amount)
			}
		}
	}

	var pc struct {
		Channels []struct {
			PeerID     string     `json:"peer_id"`
			ShortID    string     `json:"short_channel_id"`
			State      string     `json:"state"`
			Total      amountMsat `json:"total_msat"`
			ToUs       amountMsat `json:"to_us_msat"`
			Spendable  amountMsat `json:"spendable_msat"`
			Receivable amountMsat `json:"receivable_msat"`
			Connected  bool       `json:"peer_connected"`
		} `json:"channels"`
	}
	if err := c.call(ctx, "listpeerchannels", nil, &pc); err == nil {
		for _, ch := range pc.Channels {
			info.Channels = append(info.Channels, Channel{
				PeerID: ch.PeerID, ShortID: ch.ShortID, State: ch.State,
				TotalMsat: int64(ch.Total), OursMsat: int64(ch.ToUs),
				Spendable: int64(ch.Spendable), Receivable: int64(ch.Receivable),
				Connected: ch.Connected,
			})
			if ch.State == "CHANNELD_NORMAL" {
				info.ChanSpendable += int64(ch.Spendable)
				info.ChanReceivable += int64(ch.Receivable)
			}
		}
	}
	return info
}

// Quick is the dashboard variant of Status: one getinfo with a tight
// timeout, no funds/channel calls — cheap enough for the SSE tick.
type QuickInfo struct {
	Connected bool   `json:"connected"`
	Available bool   `json:"available"`
	Alias     string `json:"alias,omitempty"`
	Syncing   bool   `json:"syncing,omitempty"`
}

func (c *Client) Quick(ctx context.Context) QuickInfo {
	q := QuickInfo{Connected: c.Connected()}
	if !q.Connected {
		return q
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var gi struct {
		Alias   string `json:"alias"`
		WarnBTC string `json:"warning_bitcoind_sync"`
		WarnLN  string `json:"warning_lightningd_sync"`
	}
	if err := c.call(cctx, "getinfo", nil, &gi); err != nil {
		return q
	}
	q.Available = true
	q.Alias = gi.Alias
	q.Syncing = gi.WarnBTC != "" || gi.WarnLN != ""
	return q
}

// NewAddress returns a fresh bech32 receive address from CLN's wallet.
func (c *Client) NewAddress(ctx context.Context) (string, error) {
	var out struct {
		Bech32 string `json:"bech32"`
		P2TR   string `json:"p2tr"`
	}
	if err := c.call(ctx, "newaddr", map[string]any{"addresstype": "bech32"}, &out); err != nil {
		return "", err
	}
	if out.Bech32 != "" {
		return out.Bech32, nil
	}
	return out.P2TR, nil
}

// Invoice creates a BOLT11 invoice. amountMsat <= 0 means "any amount".
func (c *Client) Invoice(ctx context.Context, amountMsat int64, description string) (string, error) {
	params := map[string]any{
		"label":       fmt.Sprintf("nodeos-%d", time.Now().UnixNano()),
		"description": description,
	}
	if amountMsat > 0 {
		params["amount_msat"] = amountMsat
	} else {
		params["amount_msat"] = "any"
	}
	var out struct {
		Bolt11 string `json:"bolt11"`
	}
	if err := c.call(ctx, "invoice", params, &out); err != nil {
		return "", err
	}
	return out.Bolt11, nil
}
