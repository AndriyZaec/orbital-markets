package rpc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mr-tron/base58"
)

func TestAccountingTransactionBindsSignatureSlotLegacyPacketAndMetadata(t *testing.T) {
	packet := append([]byte{2}, bytes.Repeat([]byte{7}, 64)...)
	packet = append(packet, make([]byte, 65)...)
	signature := base58.Encode(packet[1:65])
	for _, kind := range []string{"valid", "wrong_signature", "stale", "versioned", "failed", "missing_inner", "loaded"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					ID     uint64            `json:"id"`
					Method string            `json:"method"`
					Params []json.RawMessage `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				if request.Method != "getTransaction" {
					t.Error(request.Method)
				}
				var options map[string]any
				_ = json.Unmarshal(request.Params[1], &options)
				if options["encoding"] != "base64" || options["commitment"] != "confirmed" {
					t.Error(options)
				}
				data := append([]byte(nil), packet...)
				slot := 130
				meta := map[string]any{"err": nil, "fee": 10000, "innerInstructions": []any{map[string]any{"index": 0, "instructions": []any{}}}}
				result := map[string]any{"slot": slot, "blockTime": 1700000000, "version": "legacy", "meta": meta}
				switch kind {
				case "wrong_signature":
					data[1] ^= 1
				case "stale":
					result["slot"] = 119
				case "versioned":
					result["version"] = 0
				case "failed":
					meta["err"] = map[string]any{"InstructionError": 1}
				case "missing_inner":
					delete(meta, "innerInstructions")
				case "loaded":
					meta["loadedAddresses"] = map[string]any{"writable": []string{"unexpected"}, "readonly": []string{}}
				}
				result["transaction"] = []any{base64.StdEncoding.EncodeToString(data), "base64"}
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
			}))
			defer server.Close()
			client, err := New(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := client.Transaction(context.Background(), signature, 120)
			if kind == "valid" {
				if err != nil || tx.Slot != 130 || tx.FeeLamports != 10000 || tx.PacketBase64 != base64.StdEncoding.EncodeToString(packet) {
					t.Fatal(tx, err)
				}
			} else if err == nil {
				t.Fatal("unbound accounting transaction accepted", kind)
			}
		})
	}
}
