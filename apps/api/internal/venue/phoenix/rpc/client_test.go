package rpc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mr-tron/base58"
)

type testRequest struct {
	Version string            `json:"jsonrpc"`
	ID      uint64            `json:"id"`
	Method  string            `json:"method"`
	Params  []json.RawMessage `json:"params"`
}

func testClient(t *testing.T, handle func(testRequest) any) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("invalid RPC transport")
		}
		var request testRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.Version != "2.0" || request.ID == 0 {
			t.Error("invalid RPC envelope")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": handle(request)})
	}))
	t.Cleanup(server.Close)
	c, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func testValue(value any) any {
	return map[string]any{"context": map[string]uint64{"slot": 100}, "value": value}
}

func TestReadContracts(t *testing.T) {
	hash := base58.Encode(make([]byte, 32))
	sig := base58.Encode(make([]byte, 64))
	var ids = make(map[uint64]bool)
	c := testClient(t, func(r testRequest) any {
		if ids[r.ID] {
			t.Error("reused RPC request ID")
		}
		ids[r.ID] = true
		switch r.Method {
		case "getLatestBlockhash":
			if string(r.Params[0]) != `{"commitment":"confirmed"}` {
				t.Errorf("blockhash params=%s", r.Params)
			}
			return testValue(map[string]any{"blockhash": hash, "lastValidBlockHeight": 150})
		case "getBlockHeight":
			if string(r.Params[0]) != `{"commitment":"confirmed","minContextSlot":100}` {
				t.Errorf("height params=%s", r.Params)
			}
			return 125
		case "getBalance":
			if string(r.Params[0]) != fmt.Sprintf("%q", hash) {
				t.Error("wrong balance address")
			}
			return testValue(uint64(0))
		case "getSignatureStatuses":
			if string(r.Params[0]) != fmt.Sprintf(`[%q]`, sig) || string(r.Params[1]) != `{"searchTransactionHistory":true}` {
				t.Errorf("status params=%s", r.Params)
			}
			return testValue([]any{nil})
		default:
			t.Errorf("unexpected method %s", r.Method)
			return nil
		}
	})
	ctx := context.Background()
	blockhash, err := c.LatestBlockhash(ctx)
	if err != nil || blockhash.Hash != hash || blockhash.LastValidBlockHeight != 150 || blockhash.Slot != 100 {
		t.Fatalf("blockhash=%+v err=%v", blockhash, err)
	}
	if height, err := c.BlockHeight(ctx, 100); err != nil || height != 125 {
		t.Fatalf("height=%d err=%v", height, err)
	}
	if balance, err := c.Balance(ctx, hash); err != nil || balance != 0 {
		t.Fatalf("balance=%d err=%v", balance, err)
	}
	if status, err := c.SignatureStatus(ctx, sig); err != nil || status != nil {
		t.Fatalf("unknown status=%+v err=%v", status, err)
	}
}

func TestSignatureStatusesPreserveChainFailure(t *testing.T) {
	for _, state := range []string{"processed", "confirmed", "finalized"} {
		t.Run(state, func(t *testing.T) {
			c := testClient(t, func(testRequest) any {
				return testValue([]any{map[string]any{"slot": 99, "err": map[string]any{"InstructionError": []any{0, "InvalidArgument"}}, "confirmationStatus": state}})
			})
			status, err := c.SignatureStatus(context.Background(), base58.Encode(make([]byte, 64)))
			if err != nil || status == nil || status.ConfirmationStatus != state || !strings.Contains(string(status.Err), "InstructionError") {
				t.Fatalf("status=%+v err=%v", status, err)
			}
		})
	}
}

func TestSimulateAndSendContracts(t *testing.T) {
	// Synthetic packet tests byte-preserving transport. Actual signature and
	// instruction validation is performed by RPC sigVerify/preflight, not this mock.
	packet := make([]byte, 140)
	packet[0], packet[1], packet[65] = 2, 42, 43
	wantSignature := base58.Encode(packet[1:65])
	for _, tc := range []struct {
		name       string
		simulation any
		heights    []int
		send       any
		want       error
		ambiguous  bool
	}{
		{"accepted", testValue(map[string]any{"err": nil}), []int{150, 150}, wantSignature, nil, false},
		{"expired before simulation", nil, []int{151}, nil, ErrExpired, false},
		{"expired after simulation", testValue(map[string]any{"err": nil}), []int{149, 151}, nil, ErrExpired, false},
		{"simulation failure", testValue(map[string]any{"err": "BlockhashNotFound"}), []int{149}, nil, ErrSimulation, false},
		{"missing simulation err", testValue(map[string]any{}), []int{149}, nil, ErrInvalidResponse, false},
		{"null simulation", nil, []int{149}, nil, ErrInvalidResponse, false},
		{"wrong send signature", testValue(map[string]any{"err": nil}), []int{149, 149}, "wrong", ErrInvalidResponse, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var methods []string
			heightIndex := 0
			c := testClient(t, func(r testRequest) any {
				methods = append(methods, r.Method)
				switch r.Method {
				case "getBlockHeight":
					if heightIndex >= len(tc.heights) {
						t.Error("extra height request")
						return 0
					}
					v := tc.heights[heightIndex]
					heightIndex++
					return v
				case "simulateTransaction", "sendTransaction":
					var encoded string
					_ = json.Unmarshal(r.Params[0], &encoded)
					if encoded != base64.StdEncoding.EncodeToString(packet) {
						t.Error("transaction bytes changed")
					}
					var config map[string]any
					_ = json.Unmarshal(r.Params[1], &config)
					if config["encoding"] != "base64" || config["minContextSlot"] != float64(100) {
						t.Error("wrong transaction config")
					}
					if r.Method == "simulateTransaction" {
						if config["sigVerify"] != true || config["replaceRecentBlockhash"] != false || config["commitment"] != "confirmed" {
							t.Error("simulation altered signing semantics")
						}
						return tc.simulation
					}
					if config["skipPreflight"] != false || config["maxRetries"] != float64(0) || config["preflightCommitment"] != "confirmed" {
						t.Error("unsafe send configuration")
					}
					return tc.send
				default:
					t.Error("unexpected method")
					return nil
				}
			})
			signature, err := c.SimulateAndSend(context.Background(), packet, 150, 100)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want=%v", err, tc.want)
			}
			var uncertain *AmbiguousSendError
			if errors.As(err, &uncertain) != tc.ambiguous {
				t.Fatalf("ambiguous error=%v", err)
			}
			wantMethods := []string{"getBlockHeight"}
			if tc.name != "expired before simulation" {
				wantMethods = append(wantMethods, "simulateTransaction")
			}
			if len(tc.heights) == 2 {
				wantMethods = append(wantMethods, "getBlockHeight")
			}
			if tc.send != nil {
				wantMethods = append(wantMethods, "sendTransaction")
			}
			if !reflect.DeepEqual(methods, wantMethods) {
				t.Fatalf("methods=%v want=%v", methods, wantMethods)
			}
			if tc.want == nil || tc.ambiguous {
				if signature != wantSignature {
					t.Fatal("lost locally known signature")
				}
			} else if signature != "" {
				t.Fatal("pre-send failure reported submission")
			}
		})
	}
}

func TestInvalidResponsesFailClosed(t *testing.T) {
	for _, result := range []any{nil, map[string]any{}, testValue(nil), testValue(map[string]any{"blockhash": "bad", "lastValidBlockHeight": 1})} {
		c := testClient(t, func(testRequest) any { return result })
		if _, err := c.LatestBlockhash(context.Background()); !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("result=%v err=%v", result, err)
		}
	}
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":999,"result":5}`,
		`{"jsonrpc":"1.0","id":1,"result":5}`,
		`{"jsonrpc":"2.0","id":1,"result":null}`,
		`{"jsonrpc":"2.0","id":1,"result":-1}`,
		`{"jsonrpc":"2.0","id":1,"result":1.5}`,
		`{"jsonrpc":"2.0","id":1,"result":5} trailing`,
		strings.Repeat(" ", maxResponseBytes+1),
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		c, _ := New(server.URL)
		_, err := c.BlockHeight(context.Background(), 0)
		server.Close()
		if !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("invalid response err=%v", err)
		}
	}
}

func TestFailuresDoNotLeakRPCCredentialsOrRetry(t *testing.T) {
	for _, status := range []int{200, 429, 503} {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls++
			w.WriteHeader(status)
			_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32005,"message":"secret-api-key","data":"secret-api-key"}}`)
		}))
		c, _ := New(server.URL + "?api-key=secret-api-key")
		_, err := c.BlockHeight(context.Background(), 0)
		server.Close()
		if err == nil || strings.Contains(err.Error(), "secret-api-key") || calls != 1 {
			t.Fatalf("err=%v calls=%d", err, calls)
		}
		if status == 200 {
			var rpcErr *RPCError
			if !errors.As(err, &rpcErr) || rpcErr.Code != -32005 {
				t.Fatalf("RPC code lost: %v", err)
			}
		}
		_, err = c.BlockHeight(context.Background(), 0)
		if err == nil || strings.Contains(err.Error(), "secret-api-key") {
			t.Fatalf("transport leaked endpoint: %v", err)
		}
	}
}

func TestCancellationAndTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer server.Close()
	c, _ := New(server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.BlockHeight(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	c.http.Timeout = 20 * time.Millisecond
	start := time.Now()
	if _, err := c.BlockHeight(context.Background(), 0); err == nil || time.Since(start) > time.Second {
		t.Fatalf("timeout=%v", err)
	}
}

func TestLostSendResponseRemainsAmbiguousWithoutRetry(t *testing.T) {
	packet := make([]byte, 70)
	packet[0], packet[1] = 1, 42
	wantSignature := base58.Encode(packet[1:65])
	var sends atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request testRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		var result any = 100
		switch request.Method {
		case "simulateTransaction":
			result = testValue(map[string]any{"err": nil})
		case "sendTransaction":
			sends.Add(1)
			// The node may have forwarded the packet before the connection drops.
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer server.Close()
	c, _ := New(server.URL + "?api-key=secret-api-key")
	signature, err := c.SimulateAndSend(context.Background(), packet, 150, 100)
	var uncertain *AmbiguousSendError
	if !errors.As(err, &uncertain) || signature != wantSignature || uncertain.Signature != wantSignature || sends.Load() != 1 {
		t.Fatalf("signature=%s err=%v sends=%d", signature, err, sends.Load())
	}
	if strings.Contains(err.Error(), "secret-api-key") {
		t.Fatal("credential leaked")
	}
}

func TestInvalidInputsDoNotStartRPC(t *testing.T) {
	c := testClient(t, func(testRequest) any { t.Error("invalid input started RPC"); return nil })
	ctx := context.Background()
	if _, err := c.Balance(ctx, "bad"); err == nil {
		t.Fatal("invalid address accepted")
	}
	if _, err := c.SignatureStatus(ctx, "bad"); err == nil {
		t.Fatal("invalid signature accepted")
	}
	for _, packet := range [][]byte{nil, {0}, {1}, {128}, make([]byte, 1233)} {
		if _, err := c.SimulateAndSend(ctx, packet, 150, 100); err == nil {
			t.Fatal("invalid packet accepted")
		}
	}
	packet := make([]byte, 70)
	packet[0] = 1
	if _, err := c.SimulateAndSend(ctx, packet, 0, 100); err == nil {
		t.Fatal("missing expiry accepted")
	}
}

func TestFeeForExactMessageContract(t *testing.T) {
	message := []byte("exact serialized helper message")
	for _, tc := range []struct {
		name    string
		value   any
		slot    uint64
		want    uint64
		wantErr bool
	}{
		{"quoted", uint64(10000), 100, 10000, false},
		{"blockhash unavailable", nil, 100, 0, true},
		{"zero", uint64(0), 100, 0, true},
		{"stale context", uint64(10000), 99, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(r testRequest) any {
				if r.Method != "getFeeForMessage" || string(r.Params[0]) != fmt.Sprintf("%q", base64.StdEncoding.EncodeToString(message)) || string(r.Params[1]) != `{"commitment":"confirmed","minContextSlot":100}` {
					t.Errorf("fee request=%+v", r)
				}
				return map[string]any{"context": map[string]uint64{"slot": tc.slot}, "value": tc.value}
			})
			fee, err := c.FeeForMessage(context.Background(), message, 100)
			if fee != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("fee=%d err=%v", fee, err)
			}
		})
	}
}
