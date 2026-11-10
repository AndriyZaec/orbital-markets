package orders

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/rpc"
)

type diagnosticBackend struct {
	Backend
	client *rpc.Client
}

func (b diagnosticBackend) SimulateAndSend(ctx context.Context, packet []byte, height, slot uint64) (string, error) {
	if _, err := b.Backend.SimulateAndSend(ctx, packet, height, slot); err != nil {
		return "", err
	}
	return b.client.SimulateAndSend(ctx, packet, height, slot)
}

func TestRejectedOrderRetainsDiagnosticAcrossRecreationWithoutPublicLeak(t *testing.T) {
	for _, rpcError := range []bool{false, true} {
		t.Run(map[bool]string{false: "simulation result", true: "RPC error data"}[rpcError], func(t *testing.T) {
			s, b, q, signed := testService(t, nil)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					ID     uint64 `json:"id"`
					Method string `json:"method"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
				switch request.Method {
				case "getBlockHeight":
					response["result"] = 100
				case "simulateTransaction":
					evidence := map[string]any{"err": map[string]any{"InstructionError": []any{2, map[string]any{"Custom": 6001}}}, "logs": []string{"Program log: MON program rejected at instruction 2"}, "unitsConsumed": 12345}
					if rpcError {
						response["error"] = map[string]any{"code": -32002, "message": "secret-api-key provider message", "data": evidence}
					} else {
						response["result"] = map[string]any{"context": map[string]any{"slot": 130}, "value": evidence}
					}
				default:
					t.Error("unexpected RPC call", request.Method)
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			client, err := rpc.New(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			s.backend = diagnosticBackend{Backend: b, client: client}
			for range 2 {
				result, err := s.Submit(context.Background(), q, signed, admit)
				if err != nil || result.Accepted {
					t.Fatal(result, err)
				}
				a, err := s.store.Get(context.Background(), q.Account, q.ID)
				if err != nil || a.State != "failed" || !strings.Contains(a.Error, "MON program rejected at instruction 2") {
					t.Fatalf("private rejection discarded: state=%s reason=%s err=%v", a.State, a.Error, err)
				}
				for _, want := range []string{"6001", "12345", "MON program"} {
					if !strings.Contains(result.PrivateDiagnostic, want) {
						t.Errorf("private result missing %q: %s", want, result.PrivateDiagnostic)
					}
				}
				if strings.Contains(a.Error, "secret-api-key") {
					t.Fatal("provider message leaked into journal")
				}
				public, err := json.Marshal(result)
				if err != nil || strings.Contains(string(public), "MON program") {
					t.Fatalf("diagnostic leaked: %s err=%v", public, err)
				}
				s = NewService(s.store, s.backend, nil)
			}
			if b.sends.Load() != 1 {
				t.Fatal("rejected order was resent", b.sends.Load())
			}
		})
	}
}
