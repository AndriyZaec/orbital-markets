package api

import (
	"encoding/json"
	"net/url"
	"testing"
)

func TestLiveVenueBindingsAcceptCompatibleRequestShapes(t *testing.T) {
	tests := map[string]liveVenueBindingsRequest{
		"legacy only": {
			AccountPacifica: "sol-owner", AccountHyperliquid: "0xAbC",
			AgentPacifica: "sol-agent", AgentHyperliquid: "0xDeF",
		},
		"map only": {
			Accounts: map[string]string{"pacifica": "sol-owner", "hyperliquid": "0xAbC"},
			Agents:   map[string]string{"pacifica": "sol-agent", "hyperliquid": "0xDeF"},
		},
		"partial map with legacy aliases": {
			Accounts: map[string]string{"pacifica": "sol-owner"}, AccountHyperliquid: "0xAbC",
			Agents: map[string]string{"hyperliquid": "0xDeF"}, AgentPacifica: "sol-agent",
		},
		"matching dual write": {
			Accounts:        map[string]string{"pacifica": "sol-owner", "hyperliquid": "0xabc"},
			Agents:          map[string]string{"pacifica": "sol-agent", "hyperliquid": "0xdef"},
			AccountPacifica: "sol-owner", AccountHyperliquid: "0xAbC",
			AgentPacifica: "sol-agent", AgentHyperliquid: "0xDeF",
		},
	}

	for name, request := range tests {
		t.Run(name, func(t *testing.T) {
			bindings, err := request.resolve()
			if err != nil {
				t.Fatal(err)
			}
			if err := bindings.requireAccounts(); err != nil {
				t.Fatal(err)
			}
			if err := bindings.requireAgents(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLiveVenueBindingsRejectAmbiguousOrUnsupportedRequests(t *testing.T) {
	tests := map[string]liveVenueBindingsRequest{
		"conflicting account alias": {
			Accounts: map[string]string{"pacifica": "sol-owner"}, AccountPacifica: "other-owner",
		},
		"unsupported venue": {
			Accounts: map[string]string{"aster": "0xabc"},
		},
		"duplicate normalized venue": {
			Accounts: map[string]string{"pacifica": "sol-owner", " Pacifica ": "sol-owner"},
		},
	}

	for name, request := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := request.resolve(); err == nil {
				t.Fatal("expected binding resolution error")
			}
		})
	}
}

func TestLiveVenueBindingsParseMapQuery(t *testing.T) {
	bindings, err := liveVenueBindingsFromQuery(url.Values{
		"accounts[pacifica]":    {"sol-owner"},
		"accounts[hyperliquid]": {"0xabc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bindings.requireAccounts(); err != nil {
		t.Fatal(err)
	}
	if bindings.Accounts["pacifica"] != "sol-owner" || bindings.Accounts["hyperliquid"] != "0xabc" {
		t.Fatalf("unexpected bindings: %+v", bindings.Accounts)
	}
}

func TestLiveVenueBindingsRejectDuplicateJSONAndQueryKeys(t *testing.T) {
	var request liveVenueBindingsRequest
	if err := json.Unmarshal([]byte(`{"accounts":{"pacifica":"first","pacifica":"second"}}`), &request); err == nil {
		t.Fatal("expected duplicate JSON venue binding error")
	}
	if err := json.Unmarshal([]byte(`{"accounts":{"pacifica":"first"},"accounts":{"hyperliquid":"second"}}`), &request); err == nil {
		t.Fatal("expected duplicate JSON bindings object error")
	}
	if _, err := liveVenueBindingsFromQuery(url.Values{
		"account_pacifica": {"first", "second"},
	}); err == nil {
		t.Fatal("expected duplicate legacy query parameter error")
	}
}
