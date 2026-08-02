// Package merchant turns NodeOS into a point of sale: a merchant enters an
// amount in euro, the customer scans a QR, the payment is tracked and booked.
//
// Settlement sits behind a Provider interface with two intended shapes:
//
//	keep — the merchant receives and KEEPS bitcoin, self-custody through
//	       their own node and BTCPay. No regulated party is involved, so
//	       NodeOS is a plain software vendor. This is what ships today.
//	eur  — a licensed partner (MiCA/EMI) converts to euro and pays out via
//	       SEPA. NodeOS never touches the funds; the merchant is the
//	       partner's customer. Not wired up yet — the interface exists so
//	       adding it later does not touch the UI.
package merchant

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Status string

const (
	StatusPending Status = "pending" // waiting for the customer
	StatusPaid    Status = "paid"    // seen, not final yet (0-conf / htlc)
	StatusSettled Status = "settled" // final
	StatusExpired Status = "expired"
	StatusFailed  Status = "failed"
)

type Invoice struct {
	ID         string     `json:"id"`
	ExternalID string     `json:"external_id,omitempty"`
	Reference  string     `json:"reference,omitempty"`
	AmountEUR  float64    `json:"amount_eur"`
	RateEUR    float64    `json:"rate_eur"`  // EUR per BTC at creation
	AmountSats int64      `json:"amount_sats"`
	Status     Status     `json:"status"`
	PaymentURI string     `json:"payment_uri,omitempty"` // BIP21, what the QR encodes
	CheckoutURL string    `json:"checkout_url,omitempty"`
	Mode       string     `json:"mode"` // keep | eur
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	PaidAt     *time.Time `json:"paid_at,omitempty"`
}

func (i *Invoice) Open() bool {
	return i.Status == StatusPending || i.Status == StatusPaid
}

// Provider is the settlement backend.
type Provider interface {
	ID() string
	// Ready reports whether the provider is configured and reachable; the
	// string explains what is missing when it is not.
	Ready(ctx context.Context) (bool, string)
	CreateInvoice(ctx context.Context, amountEUR float64, reference string) (*Invoice, error)
	// Refresh updates status fields of an existing invoice in place.
	Refresh(ctx context.Context, inv *Invoice) error
}

// Settings is the merchant configuration (persisted).
type Settings struct {
	Enabled    bool   `json:"enabled"`
	Business   string `json:"business"`
	VATID      string `json:"vat_id"`
	Mode       string `json:"mode"` // keep | eur
	ExpiryMins int    `json:"expiry_mins"`
	BTCPayURL  string `json:"btcpay_url"`
	BTCPayKey  string `json:"btcpay_key"`
	BTCPayStore string `json:"btcpay_store"`
}

func (s Settings) withDefaults() Settings {
	if s.Mode == "" {
		s.Mode = "keep"
	}
	if s.ExpiryMins <= 0 {
		s.ExpiryMins = 15
	}
	return s
}

const maxInvoices = 5000

// Manager owns settings, the invoice log and the active provider.
type Manager struct {
	dir  string
	demo bool

	mu       sync.Mutex
	settings Settings
	invoices []*Invoice // newest last
	provider Provider
	onChange func(*Invoice) // fired when an invoice reaches a terminal state
}

func New(dataDir string, demo bool) *Manager {
	m := &Manager{dir: dataDir, demo: demo}
	m.load()
	m.settings = m.settings.withDefaults()
	m.rebuildProvider()
	return m
}

func (m *Manager) OnChange(fn func(*Invoice)) { m.onChange = fn }

func (m *Manager) path() string { return filepath.Join(m.dir, "merchant.json") }

type persisted struct {
	Settings Settings   `json:"settings"`
	Invoices []*Invoice `json:"invoices"`
}

func (m *Manager) load() {
	b, err := os.ReadFile(m.path())
	if err != nil {
		return
	}
	var p persisted
	if json.Unmarshal(b, &p) == nil {
		m.settings = p.Settings
		m.invoices = p.Invoices
	}
}

func (m *Manager) saveLocked() {
	inv := m.invoices
	if len(inv) > maxInvoices {
		inv = inv[len(inv)-maxInvoices:]
		m.invoices = inv
	}
	b, err := json.MarshalIndent(persisted{Settings: m.settings, Invoices: inv}, "", "  ")
	if err != nil {
		return
	}
	tmp := m.path() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		os.Rename(tmp, m.path())
	}
}

// rebuildProvider picks the provider for the current settings.
func (m *Manager) rebuildProvider() {
	switch {
	case m.demo:
		m.provider = newMockProvider()
	case m.settings.Mode == "eur":
		m.provider = notConfigured{reason: "instant-euro settlement needs a licensed partner — not connected yet"}
	case m.settings.BTCPayURL != "" && m.settings.BTCPayKey != "" && m.settings.BTCPayStore != "":
		m.provider = newBTCPay(m.settings.BTCPayURL, m.settings.BTCPayKey, m.settings.BTCPayStore, m.settings.ExpiryMins)
	default:
		m.provider = notConfigured{reason: "connect BTCPay Server to accept payments"}
	}
}

func (m *Manager) Settings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings
}

func (m *Manager) SetSettings(s Settings) error {
	s = s.withDefaults()
	if s.Mode != "keep" && s.Mode != "eur" {
		return fmt.Errorf("mode must be \"keep\" or \"eur\"")
	}
	s.BTCPayURL = strings.TrimRight(strings.TrimSpace(s.BTCPayURL), "/")
	if s.BTCPayURL != "" && !strings.HasPrefix(s.BTCPayURL, "http") {
		return fmt.Errorf("BTCPay URL must start with http:// or https://")
	}
	m.mu.Lock()
	// keep the stored key when the UI sends the masked placeholder back
	if s.BTCPayKey == "" || s.BTCPayKey == maskedKey {
		s.BTCPayKey = m.settings.BTCPayKey
	}
	m.settings = s
	m.rebuildProvider()
	m.saveLocked()
	m.mu.Unlock()
	return nil
}

const maskedKey = "••••••••"

// PublicSettings hides the API key from the UI payload.
func (m *Manager) PublicSettings() Settings {
	s := m.Settings()
	if s.BTCPayKey != "" {
		s.BTCPayKey = maskedKey
	}
	return s
}

func (m *Manager) ProviderStatus(ctx context.Context) (string, bool, string) {
	m.mu.Lock()
	p := m.provider
	m.mu.Unlock()
	ok, reason := p.Ready(ctx)
	return p.ID(), ok, reason
}

// Create issues a new invoice for an amount in euro.
func (m *Manager) Create(ctx context.Context, amountEUR float64, reference string) (*Invoice, error) {
	if amountEUR <= 0 {
		return nil, fmt.Errorf("amount must be greater than zero")
	}
	if amountEUR > 1_000_000 {
		return nil, fmt.Errorf("amount looks implausible")
	}
	if len(reference) > 64 {
		return nil, fmt.Errorf("reference too long")
	}
	m.mu.Lock()
	p, mode := m.provider, m.settings.Mode
	m.mu.Unlock()

	inv, err := p.CreateInvoice(ctx, amountEUR, reference)
	if err != nil {
		return nil, err
	}
	inv.Mode = mode
	inv.Reference = reference

	m.mu.Lock()
	m.invoices = append(m.invoices, inv)
	m.saveLocked()
	m.mu.Unlock()
	return inv, nil
}

func (m *Manager) Get(id string) *Invoice {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, inv := range m.invoices {
		if inv.ID == id {
			cp := *inv
			return &cp
		}
	}
	return nil
}

// List returns the newest invoices first.
func (m *Manager) List(limit int) []*Invoice {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Invoice, 0, len(m.invoices))
	for i := len(m.invoices) - 1; i >= 0; i-- {
		out = append(out, m.invoices[i])
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

type Stats struct {
	Today    float64 `json:"today_eur"`
	Month    float64 `json:"month_eur"`
	Total    float64 `json:"total_eur"`
	CountAll int     `json:"count"`
	Open     int     `json:"open"`
}

func (m *Manager) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	y, mo, d := now.Date()
	dayStart := time.Date(y, mo, d, 0, 0, 0, 0, now.Location())
	monthStart := time.Date(y, mo, 1, 0, 0, 0, 0, now.Location())
	var s Stats
	for _, inv := range m.invoices {
		if inv.Open() {
			s.Open++
		}
		if inv.Status != StatusSettled && inv.Status != StatusPaid {
			continue
		}
		s.CountAll++
		s.Total += inv.AmountEUR
		when := inv.CreatedAt
		if inv.PaidAt != nil {
			when = *inv.PaidAt
		}
		if when.After(monthStart) {
			s.Month += inv.AmountEUR
		}
		if when.After(dayStart) {
			s.Today += inv.AmountEUR
		}
	}
	return s
}

// Poll refreshes every open invoice; run from a ticker.
func (m *Manager) Poll(ctx context.Context) {
	m.mu.Lock()
	p := m.provider
	var open []*Invoice
	for _, inv := range m.invoices {
		if inv.Open() {
			open = append(open, inv)
		}
	}
	m.mu.Unlock()

	changed := false
	for _, inv := range open {
		before := inv.Status
		cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		err := p.Refresh(cctx, inv)
		cancel()
		if err != nil {
			continue
		}
		if inv.Status != before {
			changed = true
			if m.onChange != nil {
				cp := *inv
				m.onChange(&cp)
			}
		}
	}
	if changed {
		m.mu.Lock()
		m.saveLocked()
		m.mu.Unlock()
	}
}

func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Poll(ctx)
		}
	}
}

// ExportCSV writes the payment log in a shape an accountant can import:
// one row per payment with the euro amount and the rate that applied.
func (m *Manager) ExportCSV(from, to time.Time) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]*Invoice, 0, len(m.invoices))
	for _, inv := range m.invoices {
		when := inv.CreatedAt
		if inv.PaidAt != nil {
			when = *inv.PaidAt
		}
		if (!from.IsZero() && when.Before(from)) || (!to.IsZero() && when.After(to)) {
			continue
		}
		rows = append(rows, inv)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt.Before(rows[j].CreatedAt) })

	var b strings.Builder
	b.WriteString("Datum;Uhrzeit;Beleg;Referenz;Betrag EUR;Betrag BTC;Kurs EUR/BTC;Status;Zahlungsart;Transaktion\n")
	for _, inv := range rows {
		when := inv.CreatedAt
		if inv.PaidAt != nil {
			when = *inv.PaidAt
		}
		btc := float64(inv.AmountSats) / 1e8
		mode := "Bitcoin (self-custody)"
		if inv.Mode == "eur" {
			mode = "Bitcoin, Auszahlung EUR"
		}
		fmt.Fprintf(&b, "%s;%s;%s;%s;%s;%s;%s;%s;%s;%s\n",
			when.Format("02.01.2006"), when.Format("15:04:05"),
			csvEsc(inv.ID), csvEsc(inv.Reference),
			de(inv.AmountEUR, 2), de(btc, 8), de(inv.RateEUR, 2),
			inv.Status, mode, csvEsc(inv.ExternalID))
	}
	return b.String()
}

// de formats a number the way German accounting tools expect it.
func de(v float64, dec int) string {
	s := fmt.Sprintf("%.*f", dec, v)
	return strings.Replace(s, ".", ",", 1)
}

func csvEsc(s string) string {
	s = strings.ReplaceAll(s, ";", ",")
	return strings.ReplaceAll(s, "\n", " ")
}

// notConfigured stands in whenever no usable provider is set up.
type notConfigured struct{ reason string }

func (n notConfigured) ID() string { return "none" }
func (n notConfigured) Ready(context.Context) (bool, string) { return false, n.reason }
func (n notConfigured) CreateInvoice(context.Context, float64, string) (*Invoice, error) {
	return nil, fmt.Errorf("%s", n.reason)
}
func (n notConfigured) Refresh(context.Context, *Invoice) error { return nil }
