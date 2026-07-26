// Package alerts keeps a bounded in-memory feed of operational events and
// notifies a subscriber (the SSE hub) about new entries.
package alerts

import (
	"sync"
	"time"
)

type Level string

const (
	Info     Level = "info"
	Warning  Level = "warning"
	Critical Level = "critical"
	Party    Level = "party" // block candidates deserve their own level
)

type Alert struct {
	Time  time.Time `json:"time"`
	Level Level     `json:"level"`
	Type  string    `json:"type"`
	Miner string    `json:"miner,omitempty"`
	Msg   string    `json:"msg"`
}

const keep = 200

type Feed struct {
	mu     sync.Mutex
	items  []Alert
	notify func(Alert)
}

func NewFeed() *Feed { return &Feed{} }

// OnAlert registers a callback invoked (without the feed lock held) for
// every new alert.
func (f *Feed) OnAlert(fn func(Alert)) { f.notify = fn }

func (f *Feed) Add(level Level, typ, miner, msg string) {
	a := Alert{Time: time.Now(), Level: level, Type: typ, Miner: miner, Msg: msg}
	f.mu.Lock()
	f.items = append(f.items, a)
	if len(f.items) > keep {
		f.items = f.items[len(f.items)-keep:]
	}
	notify := f.notify
	f.mu.Unlock()
	if notify != nil {
		notify(a)
	}
}

// List returns alerts newest-first.
func (f *Feed) List() []Alert {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Alert, len(f.items))
	for i, a := range f.items {
		out[len(f.items)-1-i] = a
	}
	return out
}
