package rpc

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSimulationFailureRetainsPrivateProgramEvidenceWithoutSending(t *testing.T) {
	var sends int
	c := testClient(t, func(r testRequest) any {
		switch r.Method {
		case "getBlockHeight":
			return 100
		case "simulateTransaction":
			return testValue(map[string]any{
				"err":           map[string]any{"InstructionError": []any{2, map[string]any{"Custom": 6001}}},
				"logs":          []string{"Program example invoke [1]", "Program log: insufficient margin", "Program log: private-test-key", "Program example failed: custom program error: 0x1771"},
				"unitsConsumed": 12345,
			})
		case "sendTransaction":
			sends++
		}
		return nil
	})
	c.endpoint += "?api-key=private-test-key"
	packet := make([]byte, 140)
	packet[0] = 2
	_, err := c.SimulateAndSend(context.Background(), packet, 150, 100)
	if !errors.Is(err, ErrSimulation) || sends != 0 {
		t.Fatalf("err=%v sends=%d", err, sends)
	}
	var diagnostic interface{ Diagnostic() string }
	if !errors.As(err, &diagnostic) {
		t.Fatalf("simulation details discarded: %v", err)
	}
	for _, want := range []string{"InstructionError", "6001", "insufficient margin", "12345", `"slot":100`} {
		if !strings.Contains(diagnostic.Diagnostic(), want) {
			t.Errorf("private diagnostic missing %q: %s", want, diagnostic.Diagnostic())
		}
	}
	if strings.Contains(err.Error(), "insufficient margin") || strings.Contains(err.Error(), "6001") {
		t.Fatal("public error includes private program evidence")
	}
	if strings.Contains(diagnostic.Diagnostic(), "private-test-key") {
		t.Fatal("RPC credential leaked through program logs")
	}
}
