package helper

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestStartPerformsCompatibleHealthHandshake(t *testing.T) {
	client, err := startClient(context.Background(), fakeHelperCommand(t, "valid"), time.Second)
	if err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(client.Close)

	health, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if health.HelperVersion != "0.1.0" || health.PhoenixRiseVersion != PhoenixRiseVersion {
		t.Fatalf("unexpected health result: %+v", health)
	}
}

func TestStartRejectsIncompatibleProtocol(t *testing.T) {
	_, err := startClient(context.Background(), fakeHelperCommand(t, "wrong_protocol"), time.Second)
	if err == nil {
		t.Fatal("expected incompatible protocol error")
	}
}

func TestStartKillsHelperAfterTimeout(t *testing.T) {
	started := time.Now()
	_, err := startClient(context.Background(), fakeHelperCommand(t, "hang"), 30*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("timeout took too long: %v", elapsed)
	}
}

func TestStartRejectsOversizedResponse(t *testing.T) {
	_, err := startClient(context.Background(), fakeHelperCommand(t, "oversized_health"), time.Second)
	if err == nil {
		t.Fatal("expected oversized response error")
	}
}

func TestBuildOrderDecodesAndValidatesResult(t *testing.T) {
	client, err := startClient(context.Background(), fakeHelperCommand(t, "valid"), time.Second)
	if err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(client.Close)

	request := testBuildOrderRequest()
	result, err := client.BuildOrder(context.Background(), request)
	if err != nil {
		t.Fatalf("build order: %v", err)
	}
	if result.MessageBase64 != base64.StdEncoding.EncodeToString([]byte("message")) {
		t.Fatalf("unexpected message: %q", result.MessageBase64)
	}
	if len(result.RequiredSigners) != 2 || result.RequiredSigners[1].Role != "position_authority" {
		t.Fatalf("unexpected signers: %+v", result.RequiredSigners)
	}
}

func TestBuildOrderReturnsTypedHelperError(t *testing.T) {
	client, err := startClient(context.Background(), fakeHelperCommand(t, "order_error"), time.Second)
	if err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(client.Close)

	_, err = client.BuildOrder(context.Background(), testBuildOrderRequest())
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) {
		t.Fatalf("expected ProtocolError, got %v", err)
	}
	if protocolErr.Code != "invalid_order_request" {
		t.Fatalf("unexpected error code: %q", protocolErr.Code)
	}
}

func TestBuildOrderRejectsMismatchedRequestID(t *testing.T) {
	client, err := startClient(context.Background(), fakeHelperCommand(t, "wrong_request_id"), time.Second)
	if err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(client.Close)

	_, err = client.BuildOrder(context.Background(), testBuildOrderRequest())
	if err == nil {
		t.Fatal("expected request ID mismatch")
	}
	if _, err := client.Health(context.Background()); err == nil {
		t.Fatal("expected client to remain stopped after protocol desynchronization")
	}
}

func TestBuildOrderRejectsOversizedRequestWithoutStoppingHelper(t *testing.T) {
	client, err := startClient(context.Background(), fakeHelperCommand(t, "valid"), time.Second)
	if err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(client.Close)

	request := testBuildOrderRequest()
	request.Accounts.GlobalTraderIndex = []string{strings.Repeat("x", maxRequestBytes)}
	if _, err := client.BuildOrder(context.Background(), request); err == nil {
		t.Fatal("expected oversized request error")
	}
	if _, err := client.Health(context.Background()); err != nil {
		t.Fatalf("helper should remain usable after local request rejection: %v", err)
	}
}

func TestClientObservesIdleHelperExit(t *testing.T) {
	client, err := startClient(context.Background(), fakeHelperCommand(t, "exit_after_health"), time.Second)
	if err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(client.Close)

	deadline := time.Now().Add(time.Second)
	for {
		_, err = client.Health(context.Background())
		if err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper exit was not observed")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestQueuedCallHonorsCallerDeadline(t *testing.T) {
	client, err := startClient(context.Background(), fakeHelperCommand(t, "slow_order"), time.Second)
	if err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(client.Close)

	firstDone := make(chan error, 1)
	go func() {
		_, err := client.BuildOrder(context.Background(), testBuildOrderRequest())
		firstDone <- err
	}()
	time.Sleep(30 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = client.Health(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected queued call deadline, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > 150*time.Millisecond {
		t.Fatalf("queued call ignored deadline: %v", elapsed)
	}
	if err := <-firstDone; err != nil {
		t.Fatalf("first call: %v", err)
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("ORBITAL_FAKE_PHOENIX_HELPER") != "1" {
		return
	}

	mode := os.Getenv("ORBITAL_FAKE_PHOENIX_HELPER_MODE")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ProtocolVersion uint32          `json:"protocol_version"`
			RequestID       string          `json:"request_id"`
			Operation       string          `json:"operation"`
			Payload         json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(2)
		}

		if request.Operation == "health" {
			if mode == "hang" {
				time.Sleep(time.Hour)
			}
			version := ProtocolVersion
			if mode == "wrong_protocol" {
				version++
			}
			healthResult := map[string]any{
				"helper_version":       "0.1.0",
				"phoenix_rise_version": PhoenixRiseVersion,
			}
			if mode == "oversized_health" {
				healthResult["padding"] = strings.Repeat("x", maxResponseBytes)
			}
			writeFakeResponse(map[string]any{
				"protocol_version": version,
				"request_id":       request.RequestID,
				"ok":               true,
				"result":           healthResult,
			})
			if mode == "exit_after_health" {
				return
			}
			continue
		}

		if mode == "order_error" {
			writeFakeResponse(map[string]any{
				"protocol_version": ProtocolVersion,
				"request_id":       request.RequestID,
				"ok":               false,
				"error": map[string]any{
					"code":    "invalid_order_request",
					"message": "rejected fixture",
				},
			})
			continue
		}

		var order BuildOrderRequest
		if err := json.Unmarshal(request.Payload, &order); err != nil {
			os.Exit(3)
		}
		if mode == "slow_order" {
			time.Sleep(300 * time.Millisecond)
		}
		message := []byte("message")
		digest := sha256.Sum256(message)
		requestID := request.RequestID
		if mode == "wrong_request_id" {
			requestID = "wrong"
		}
		result := map[string]any{
			"message_base64":          base64.StdEncoding.EncodeToString(message),
			"message_sha256":          hex.EncodeToString(digest[:]),
			"required_signers":        []RequiredSigner{{Index: 0, Pubkey: order.FeePayer, Role: "fee_payer"}, {Index: 1, Pubkey: order.PositionAuthority, Role: "position_authority"}},
			"recent_blockhash":        order.RecentBlockhash,
			"last_valid_block_height": order.LastValidBlockHeight,
			"metadata_generation":     order.MetadataGeneration,
			"trader_account":          "trader",
			"trader_pda_index":        0,
			"subaccount_index":        0,
			"trader_bump":             1,
			"manifest":                fakeOrderManifest(order),
		}
		writeFakeResponse(map[string]any{
			"protocol_version": ProtocolVersion,
			"request_id":       requestID,
			"ok":               true,
			"result":           result,
		})
	}
}

func fakeOrderManifest(order BuildOrderRequest) OrderManifest {
	manifest := OrderManifest{
		Format:             "solana_legacy_message",
		PhoenixProgramID:   PhoenixProgramID,
		Routing:            "flight",
		IntentAction:       order.Action,
		OwnerAuthority:     order.OwnerAuthority,
		PositionAuthority:  order.PositionAuthority,
		FeePayer:           order.FeePayer,
		TraderAccount:      "trader",
		RequestedSymbol:    order.Symbol,
		Orderbook:          order.Accounts.Orderbook,
		MetadataGeneration: order.MetadataGeneration,
		Side:               order.Side,
		NumBaseLots:        order.NumBaseLots,
		PriceLimitTicks:    order.PriceLimitTicks,
		ReduceOnly:         order.ReduceOnly,
		ClientOrderID:      order.ClientOrderID,
		LastValidSlot:      order.LastValidSlot,
		AccountKeys:        []AccountManifest{{Index: 0, Pubkey: order.FeePayer, Signer: true, Writable: true}},
		Instructions:       []InstructionManifest{{ProgramID: FlightProgramID}},
	}
	manifest.Flight = &FlightManifest{
		FlightProgramID:          FlightProgramID,
		BuilderAuthority:         order.BuilderAuthority,
		BuilderTraderAccount:     "builder-trader",
		BuilderState:             "builder-state",
		ExpectedRegisteredFeeBPS: ExpectedFlightFeeBPS,
	}
	return manifest
}

func fakeHelperCommand(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(),
		"ORBITAL_FAKE_PHOENIX_HELPER=1",
		"ORBITAL_FAKE_PHOENIX_HELPER_MODE="+mode,
	)
	return cmd
}

func writeFakeResponse(response any) {
	data, err := json.Marshal(response)
	if err != nil {
		os.Exit(4)
	}
	if _, err := fmt.Fprintf(os.Stdout, "%s\n", data); err != nil {
		os.Exit(5)
	}
}

func testBuildOrderRequest() BuildOrderRequest {
	return BuildOrderRequest{
		Action:               ActionOpen,
		OwnerAuthority:       "owner",
		PositionAuthority:    "position",
		FeePayer:             "payer",
		BuilderAuthority:     "builder",
		Symbol:               "SOL-PERP",
		Side:                 SideBid,
		NumBaseLots:          1,
		PriceLimitTicks:      2,
		ClientOrderID:        3,
		LastValidSlot:        4,
		RecentBlockhash:      "blockhash",
		LastValidBlockHeight: 5,
		MetadataGeneration:   6,
		Accounts: MarketAccounts{
			PerpAssetMap:       "asset-map",
			Orderbook:          "orderbook",
			SplineCollection:   "splines",
			GlobalTraderIndex:  []string{"index"},
			ActiveTraderBuffer: []string{"buffer"},
		},
	}
}
