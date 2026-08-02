package merchant

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"
)

// mockProvider makes the whole point-of-sale flow demoable without BTCPay,
// a node or a single satoshi: it mints realistic-looking payment URIs and
// marks invoices paid a few seconds later. Demo mode only.
type mockProvider struct {
	mu   sync.Mutex
	rng  *rand.Rand
	rate float64 // EUR per BTC, drifts slightly so the UI looks alive
	seq  int
}

func newMockProvider() *mockProvider {
	return &mockProvider{rng: rand.New(rand.NewSource(1312)), rate: 92_450}
}

func (p *mockProvider) ID() string { return "demo" }

func (p *mockProvider) Ready(context.Context) (bool, string) {
	return true, "demo settlement — payments are simulated, no real money moves"
}

const bech32Chars = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

func (p *mockProvider) randB32(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(bech32Chars[p.rng.Intn(len(bech32Chars))])
	}
	return b.String()
}

func (p *mockProvider) CreateInvoice(_ context.Context, amountEUR float64, _ string) (*Invoice, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rate += (p.rng.Float64() - 0.5) * 200
	p.seq++

	sats := int64(amountEUR / p.rate * 1e8)
	addr := "bc1q" + p.randB32(38)
	bolt11 := "lnbc" + fmt.Sprint(sats*10) + "n1p" + p.randB32(180)
	uri := fmt.Sprintf("bitcoin:%s?amount=%.8f&label=NodeOS&lightning=%s",
		strings.ToUpper(addr), float64(sats)/1e8, strings.ToUpper(bolt11))

	now := time.Now()
	return &Invoice{
		ID:         fmt.Sprintf("demo-%d-%d", now.Unix(), p.seq),
		ExternalID: fmt.Sprintf("demo-%d", p.seq),
		AmountEUR:  amountEUR,
		RateEUR:    p.rate,
		AmountSats: sats,
		Status:     StatusPending,
		PaymentURI: uri,
		CreatedAt:  now,
		ExpiresAt:  now.Add(15 * time.Minute),
	}, nil
}

// Refresh walks a demo invoice through pending → paid → settled so the
// point-of-sale screen can be watched end to end.
func (p *mockProvider) Refresh(_ context.Context, inv *Invoice) error {
	age := time.Since(inv.CreatedAt)
	switch {
	case age > 25*time.Second && inv.Status == StatusPaid:
		inv.Status = StatusSettled
	case age > 8*time.Second && inv.Status == StatusPending:
		inv.Status = StatusPaid
		now := time.Now()
		inv.PaidAt = &now
	}
	return nil
}
