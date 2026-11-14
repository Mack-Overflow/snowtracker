// Package store keeps current forecasts in memory and moves them to cheap
// storage once they go stale.
package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry is one computed forecast, already serialised.
type Entry struct {
	Key       string
	Body      []byte // JSON
	FetchedAt time.Time
}

// Hot is the in-memory store serving live requests.
type Hot struct {
	mu    sync.RWMutex
	items map[string]Entry
	ttl   time.Duration
}

func NewHot(ttl time.Duration) *Hot {
	return &Hot{items: map[string]Entry{}, ttl: ttl}
}

// Get returns a fresh entry; stale ones are left for Sweep to archive.
func (h *Hot) Get(key string, now time.Time) (Entry, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	e, ok := h.items[key]
	if !ok || now.Sub(e.FetchedAt) > h.ttl {
		return Entry{}, false
	}
	return e, true
}

// Put stores e and returns the entry it replaced, if any, so the caller can
// archive it.
func (h *Hot) Put(e Entry) (Entry, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	old, ok := h.items[e.Key]
	h.items[e.Key] = e
	return old, ok
}

// Sweep removes and returns every stale entry.
func (h *Hot) Sweep(now time.Time) []Entry {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Entry
	for k, e := range h.items {
		if now.Sub(e.FetchedAt) > h.ttl {
			out = append(out, e)
			delete(h.items, k)
		}
	}
	return out
}

// Archiver is cold storage. Disk is the only implementation so far; an S3
// one needs just this method.
type Archiver interface {
	Archive(ctx context.Context, e Entry) error
}

// Disk writes gzip-compressed snapshots under
// Dir/<key>/<yyyy>/<mm>/<dd>/<fetchedAt>.json.gz — the same layout an object
// store would use.
type Disk struct{ Dir string }

func (d Disk) Archive(_ context.Context, e Entry) error {
	t := e.FetchedAt.UTC()
	dir := filepath.Join(d.Dir, e.Key, t.Format("2006"), t.Format("01"), t.Format("02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(e.Body); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, t.Format("20060102T150405Z")+".json.gz"), buf.Bytes(), 0o644)
}
