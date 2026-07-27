package fleet

import (
	"context"
	"fmt"
	"strings"

	"nodeos/internal/alerts"
)

const alertInfo = alerts.Info

// Preset is a frequency/voltage pair for one ASIC family. Values follow the
// vendor's own AxeOS presets — NodeOS deliberately offers no free-form
// overclocking UI: a wrong voltage kills hardware, and "eco/standard/turbo"
// covers what fleet owners actually want.
type Preset struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Frequency   float64 `json:"frequency"`
	CoreVoltage float64 `json:"core_voltage"`
	Note        string  `json:"note,omitempty"`
}

// presets maps an ASIC model to its tuning options; the first entry of each
// list is the vendor default.
var presets = map[string][]Preset{
	"BM1366": { // Bitaxe Ultra
		{ID: "eco", Name: "Eco", Frequency: 400, CoreVoltage: 1100, Note: "cooler and quieter, ~15 % less hashrate"},
		{ID: "standard", Name: "Standard", Frequency: 485, CoreVoltage: 1200, Note: "vendor default"},
		{ID: "turbo", Name: "Turbo", Frequency: 575, CoreVoltage: 1250, Note: "needs good cooling"},
	},
	"BM1368": { // Bitaxe Supra
		{ID: "eco", Name: "Eco", Frequency: 425, CoreVoltage: 1100, Note: "cooler and quieter"},
		{ID: "standard", Name: "Standard", Frequency: 490, CoreVoltage: 1166, Note: "vendor default"},
		{ID: "turbo", Name: "Turbo", Frequency: 596, CoreVoltage: 1250, Note: "needs good cooling"},
	},
	"BM1370": { // Bitaxe Gamma / NerdQAxe++
		{ID: "eco", Name: "Eco", Frequency: 490, CoreVoltage: 1100, Note: "cooler and quieter"},
		{ID: "standard", Name: "Standard", Frequency: 525, CoreVoltage: 1150, Note: "vendor default"},
		{ID: "turbo", Name: "Turbo", Frequency: 596, CoreVoltage: 1200, Note: "needs good cooling"},
	},
}

// PresetsFor returns the tuning options for an ASIC model (nil when unknown —
// the UI then hides tuning rather than guessing values).
func PresetsFor(asic string) []Preset {
	return presets[strings.ToUpper(strings.TrimSpace(asic))]
}

// AllPresets is the catalog served to the UI.
func AllPresets() map[string][]Preset { return presets }

// ApplyPreset writes frequency and core voltage to one miner and restarts it
// (AxeOS applies both only on restart).
func (m *Manager) ApplyPreset(ctx context.Context, host, presetID string) error {
	m.mu.RLock()
	miner, ok := m.miners[host]
	var asic string
	if ok && miner.Info != nil {
		asic = miner.Info.ASICModel
	}
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("unknown miner %s", host)
	}
	list := PresetsFor(asic)
	if list == nil {
		return fmt.Errorf("no tuning presets known for %q", asic)
	}
	var p *Preset
	for i := range list {
		if list[i].ID == presetID {
			p = &list[i]
		}
	}
	if p == nil {
		return fmt.Errorf("unknown preset %q for %s", presetID, asic)
	}
	if err := m.PatchMiner(ctx, host, map[string]any{
		"frequency": p.Frequency, "coreVoltage": p.CoreVoltage,
	}); err != nil {
		return err
	}
	if err := m.RestartMiner(ctx, host); err != nil {
		return fmt.Errorf("settings written but restart failed: %w", err)
	}
	m.feed.Add(alertInfo, "tuning_applied", host,
		fmt.Sprintf("%s set to %s (%.0f MHz, %.0f mV)", miner.Label(), p.Name, p.Frequency, p.CoreVoltage))
	return nil
}
