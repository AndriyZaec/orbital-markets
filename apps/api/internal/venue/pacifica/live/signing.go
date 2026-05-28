package live

import (
	"encoding/json"
	"fmt"
	"sort"
)

// BuildSigningMessage constructs the canonical signing payload for Pacifica.
//
// Pacifica signing protocol (from official Python SDK):
//
//  1. Build a header: {"timestamp": ms, "expiry_window": ms, "type": "create_market_order"}
//  2. Build a data payload: {"symbol", "reduce_only", "amount", "side", "slippage_percent", "client_order_id"}
//  3. Merge into: {header fields..., "data": {payload fields...}}
//  4. Sort ALL keys recursively (alphabetical)
//  5. Compact JSON with no spaces: json.Marshal equivalent of separators=(",",":")
//  6. UTF-8 encode the resulting string
//  7. Sign the bytes with Solana signMessage (ed25519)
//  8. Base58-encode the signature
//
// The "account" and "signature" fields are NOT part of the signed message.
// The "type" field IS part of the signed message (in the header).
func BuildSigningMessage(
	actionType string,
	timestamp int64,
	expiryWindow int64,
	data map[string]any,
) ([]byte, error) {
	msg := map[string]any{
		"timestamp":     timestamp,
		"expiry_window": expiryWindow,
		"type":          actionType,
		"data":          data,
	}

	sorted := sortKeysRecursive(msg)

	// Compact JSON — Go's json.Marshal already uses compact format (no spaces)
	bytes, err := json.Marshal(sorted)
	if err != nil {
		return nil, fmt.Errorf("marshal signing message: %w", err)
	}

	return bytes, nil
}

// BuildMarketOrderSigningData constructs the "data" portion of the signing message
// for a create_market_order action.
func BuildMarketOrderSigningData(
	symbol string,
	side string,
	amount string,
	reduceOnly bool,
	slippagePct string,
	clientOrderID string,
) map[string]any {
	data := map[string]any{
		"symbol":           symbol,
		"side":             side,
		"amount":           amount,
		"reduce_only":      reduceOnly,
		"slippage_percent": slippagePct,
	}
	if clientOrderID != "" {
		data["client_order_id"] = clientOrderID
	}
	return data
}

// sortKeysRecursive returns a new structure with all map keys sorted alphabetically,
// recursively through nested maps. This matches Pacifica's sort_json_keys() in Python SDK.
//
// Go's json.Marshal does NOT sort map keys by default when using map[string]any,
// so we use an orderedMap wrapper to guarantee key order.
func sortKeysRecursive(v any) any {
	switch val := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		om := &orderedMap{
			keys:   keys,
			values: make(map[string]any, len(keys)),
		}
		for _, k := range keys {
			om.values[k] = sortKeysRecursive(val[k])
		}
		return om

	case []any:
		result := make([]any, len(val))
		for i, item := range val {
			result[i] = sortKeysRecursive(item)
		}
		return result

	default:
		return val
	}
}

// orderedMap serializes to JSON with keys in insertion order.
type orderedMap struct {
	keys   []string
	values map[string]any
}

func (o *orderedMap) MarshalJSON() ([]byte, error) {
	buf := []byte{'{'}
	for i, k := range o.keys {
		if i > 0 {
			buf = append(buf, ',')
		}
		keyBytes, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf = append(buf, keyBytes...)
		buf = append(buf, ':')
		valBytes, err := json.Marshal(o.values[k])
		if err != nil {
			return nil, err
		}
		buf = append(buf, valBytes...)
	}
	buf = append(buf, '}')
	return buf, nil
}
