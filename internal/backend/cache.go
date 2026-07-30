package backend

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// diskCache is a content-addressed cache: a request is hashed to a key and its
// response JSON is stored at cacheDir/<capability>/<key>.json. Hits are free
// (no network, no spend counter, no concurrency slot), which makes reruns and
// interrupted-run resumes cheap. A tiny in-memory map avoids re-reading hot
// keys within a single process.
type diskCache struct {
	dir string
	mu  sync.RWMutex
	mem map[string][]byte
}

func newDiskCache(dir string) *diskCache {
	return &diskCache{dir: dir, mem: make(map[string][]byte)}
}

// key hashes a capability tag plus arbitrary request parts into a stable hex
// digest. Any change to inputs (model, text, prompt) yields a new key.
func cacheKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0}) // domain separator so ["a","b"] != ["ab",""]
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (c *diskCache) path(capability, key string) string {
	return filepath.Join(c.dir, capability, key+".json")
}

// get returns cached bytes and true on hit.
func (c *diskCache) get(capability, key string) ([]byte, bool) {
	memKey := capability + "/" + key
	c.mu.RLock()
	if b, ok := c.mem[memKey]; ok {
		c.mu.RUnlock()
		return b, true
	}
	c.mu.RUnlock()

	b, err := os.ReadFile(c.path(capability, key))
	if err != nil {
		return nil, false
	}
	c.mu.Lock()
	c.mem[memKey] = b
	c.mu.Unlock()
	return b, true
}

// put stores bytes for a key (best-effort; a write error is non-fatal — the
// value is still returned to the caller, just not cached).
func (c *diskCache) put(capability, key string, b []byte) {
	memKey := capability + "/" + key
	c.mu.Lock()
	c.mem[memKey] = b
	c.mu.Unlock()

	p := c.path(capability, key)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err == nil {
		_ = os.Rename(tmp, p) // atomic-ish; avoids partial files on crash
	}
}

// getJSON unmarshals a cached value into v. Returns true on hit.
func (c *diskCache) getJSON(capability, key string, v any) bool {
	b, ok := c.get(capability, key)
	if !ok {
		return false
	}
	return json.Unmarshal(b, v) == nil
}

// putJSON marshals v and stores it.
func (c *diskCache) putJSON(capability, key string, v any) {
	if b, err := json.Marshal(v); err == nil {
		c.put(capability, key, b)
	}
}
