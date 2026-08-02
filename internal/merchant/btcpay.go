package merchant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// btcpayProvider is the self-custody settlement path: BTCPay Server creates
// the invoice, prices it in euro, watches the chain and Lightning, and the
// funds land in the merchant's own wallet. NodeOS never holds a key.
type btcpayProvider struct {
	base    string
	key     string
	store   string
	expiry  int
	http    *http.Client
}

func newBTCPay(base, key, store string, expiryMins int) *btcpayProvider {
	return &btcpayProvider{
		base: strings.TrimRight(base, "/"), key: key, store: store, expiry: expiryMins,
		http: &http.Client{Timeout: 15 * time.Second},
	}
}

func (b *btcpayProvider) ID() string { return "btcpay" }

func (b *btcpayProvider) do(ctx context.Context, method, path string, body, out any) error {
	var rdr *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, b.base+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+b.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.http.Do(req)
	if err != nil {
		return fmt.Errorf("BTCPay unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return fmt.Errorf("BTCPay rejected the API key")
	}
	if resp.StatusCode/100 != 2 {
		var e []struct {
			Message string `json:"message"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		if len(e) > 0 && e[0].Message != "" {
			return fmt.Errorf("BTCPay: %s", e[0].Message)
		}
		return fmt.Errorf("BTCPay: HTTP %d", resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (b *btcpayProvider) Ready(ctx context.Context) (bool, string) {
	var store struct {
		Name string `json:"name"`
	}
	if err := b.do(ctx, http.MethodGet, "/api/v1/stores/"+b.store, nil, &store); err != nil {
		return false, err.Error()
	}
	return true, "connected to store " + store.Name
}

// btcpayInvoice is the subset of the Greenfield invoice we use.
type btcpayInvoice struct {
	ID         string  `json:"id"`
	Status     string  `json:"status"`
	CheckoutLink string `json:"checkoutLink"`
	Amount     string  `json:"amount"`
	Currency   string  `json:"currency"`
	CreatedTime int64  `json:"createdTime"`
	ExpirationTime int64 `json:"expirationTime"`
	Rate       string  `json:"rate"`
}

func (b *btcpayProvider) CreateInvoice(ctx context.Context, amountEUR float64, reference string) (*Invoice, error) {
	req := map[string]any{
		"amount":   fmt.Sprintf("%.2f", amountEUR),
		"currency": "EUR",
		"metadata": map[string]any{"orderId": reference, "posData": "NodeOS"},
		"checkout": map[string]any{
			"expirationMinutes": b.expiry,
			"redirectAutomatically": false,
		},
	}
	var bi btcpayInvoice
	if err := b.do(ctx, http.MethodPost, "/api/v1/stores/"+b.store+"/invoices", req, &bi); err != nil {
		return nil, err
	}
	inv := &Invoice{
		ID:          bi.ID,
		ExternalID:  bi.ID,
		AmountEUR:   amountEUR,
		Status:      mapStatus(bi.Status),
		CheckoutURL: bi.CheckoutLink,
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(time.Duration(b.expiry) * time.Minute),
	}
	if bi.ExpirationTime > 0 {
		inv.ExpiresAt = time.Unix(bi.ExpirationTime, 0)
	}
	b.fillPayment(ctx, inv)
	return inv, nil
}

// fillPayment pulls the payment URI (BIP21, unified with Lightning when the
// store offers it) and the sat amount for the QR.
func (b *btcpayProvider) fillPayment(ctx context.Context, inv *Invoice) {
	var pms []struct {
		PaymentMethod string `json:"paymentMethod"`
		Destination   string `json:"destination"`
		Amount        string `json:"amount"`
		Rate          string `json:"rate"`
		PaymentLink   string `json:"paymentLink"`
		Activated     bool   `json:"activated"`
	}
	if err := b.do(ctx, http.MethodGet,
		"/api/v1/stores/"+b.store+"/invoices/"+inv.ExternalID+"/payment-methods", nil, &pms); err != nil {
		return
	}
	var onchain, lightning string
	for _, pm := range pms {
		if !pm.Activated {
			continue
		}
		if inv.RateEUR == 0 {
			if r, err := strconv.ParseFloat(pm.Rate, 64); err == nil {
				inv.RateEUR = r
			}
		}
		if inv.AmountSats == 0 {
			if a, err := strconv.ParseFloat(pm.Amount, 64); err == nil {
				inv.AmountSats = int64(a * 1e8)
			}
		}
		switch {
		case strings.Contains(strings.ToUpper(pm.PaymentMethod), "LIGHTNING") ||
			strings.HasPrefix(strings.ToLower(pm.Destination), "ln"):
			lightning = pm.Destination
		default:
			if pm.PaymentLink != "" {
				onchain = pm.PaymentLink
			} else if pm.Destination != "" {
				onchain = "bitcoin:" + pm.Destination
			}
		}
	}
	// unified BIP21: on-chain address with the BOLT11 invoice attached, so a
	// single QR serves both wallet types
	switch {
	case onchain != "" && lightning != "":
		sep := "?"
		if strings.Contains(onchain, "?") {
			sep = "&"
		}
		inv.PaymentURI = onchain + sep + "lightning=" + strings.ToUpper(lightning)
	case onchain != "":
		inv.PaymentURI = onchain
	case lightning != "":
		inv.PaymentURI = "lightning:" + strings.ToUpper(lightning)
	}
}

func (b *btcpayProvider) Refresh(ctx context.Context, inv *Invoice) error {
	var bi btcpayInvoice
	if err := b.do(ctx, http.MethodGet,
		"/api/v1/stores/"+b.store+"/invoices/"+inv.ExternalID, nil, &bi); err != nil {
		return err
	}
	st := mapStatus(bi.Status)
	if st != inv.Status {
		inv.Status = st
		if (st == StatusPaid || st == StatusSettled) && inv.PaidAt == nil {
			now := time.Now()
			inv.PaidAt = &now
		}
	}
	return nil
}

// mapStatus translates BTCPay's invoice states to ours.
func mapStatus(s string) Status {
	switch strings.ToLower(s) {
	case "new":
		return StatusPending
	case "processing":
		return StatusPaid
	case "settled", "complete", "confirmed":
		return StatusSettled
	case "expired":
		return StatusExpired
	case "invalid":
		return StatusFailed
	default:
		return StatusPending
	}
}
