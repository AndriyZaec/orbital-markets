package hyperliquid

import (
	"fmt"
	"sync"
)

// AssetMap provides a thread-safe symbol -> Hyperliquid asset index mapping.
// The asset index is the position of the symbol in the Hyperliquid universe array,
// which the exchange requires in order payloads (the "a" field).
type AssetMap struct {
	mu      sync.RWMutex
	indices map[string]int // symbol -> universe array index
	symbols []string       // index -> symbol (for reverse lookup / debugging)
}

func NewAssetMap() *AssetMap {
	return &AssetMap{
		indices: make(map[string]int),
	}
}

// Update replaces the entire mapping from a fresh universe fetch.
// Called by the Adapter after each successful metadata poll.
func (m *AssetMap) Update(universe []metaAsset) {
	m.mu.Lock()
	defer m.mu.Unlock()

	indices := make(map[string]int, len(universe))
	symbols := make([]string, len(universe))
	for i, asset := range universe {
		indices[asset.Name] = i
		symbols[i] = asset.Name
	}
	m.indices = indices
	m.symbols = symbols
}

// AssetIndex returns the Hyperliquid universe index for the given symbol.
// Returns (index, true) if found, (0, false) if the symbol is unknown.
func (m *AssetMap) AssetIndex(symbol string) (int, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	idx, ok := m.indices[symbol]
	return idx, ok
}

// Len returns the number of mapped symbols.
func (m *AssetMap) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.indices)
}

// Symbols returns a copy of all known symbols in index order.
func (m *AssetMap) Symbols() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, len(m.symbols))
	copy(out, m.symbols)
	return out
}

// MustAssetIndex returns the asset index or panics. Use only in tests.
func (m *AssetMap) MustAssetIndex(symbol string) int {
	idx, ok := m.AssetIndex(symbol)
	if !ok {
		panic(fmt.Sprintf("unknown hyperliquid asset: %s", symbol))
	}
	return idx
}
