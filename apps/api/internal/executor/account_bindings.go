package executor

import (
	"encoding/json"
	"fmt"
	"strings"
)

func canonicalAccountBindings(bindings map[string]string) (map[string]string, string, string, error) {
	if len(bindings) != 2 {
		return nil, "", "", fmt.Errorf("exactly two account bindings required")
	}
	normalized := make(map[string]string, len(bindings))
	for rawVenue, rawAccount := range bindings {
		venue := strings.ToLower(strings.TrimSpace(rawVenue))
		if venue == "" {
			return nil, "", "", fmt.Errorf("account binding venue must not be empty")
		}
		if _, duplicate := normalized[venue]; duplicate {
			return nil, "", "", fmt.Errorf("duplicate account binding for %s", venue)
		}
		account := strings.TrimSpace(rawAccount)
		if account == "" {
			return nil, "", "", fmt.Errorf("account binding for %s must not be empty", venue)
		}
		if venue == "hyperliquid" || venue == "aster" {
			account = strings.ToLower(account)
		}
		normalized[venue] = account
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return nil, "", "", err
	}
	canonical := string(encoded)
	return normalized, canonical, canonical, nil
}

func storedAccountBindings(
	bindings map[string]string,
	accountPacifica, accountHyperliquid string,
) (map[string]string, string, string, string, string, error) {
	if len(bindings) == 0 {
		bindings = map[string]string{
			"pacifica":    strings.TrimSpace(accountPacifica),
			"hyperliquid": strings.ToLower(strings.TrimSpace(accountHyperliquid)),
		}
	}
	normalized, encoded, key, err := canonicalAccountBindings(bindings)
	if err != nil {
		return nil, "", "", "", "", err
	}
	pacifica := normalized["pacifica"]
	hyperliquid := normalized["hyperliquid"]
	legacyPair := len(normalized) == 2 && pacifica != "" && hyperliquid != ""
	if !legacyPair {
		if accountPacifica != "" || accountHyperliquid != "" {
			return nil, "", "", "", "", fmt.Errorf("legacy accounts require the Pacifica/Hyperliquid pair")
		}
		pacifica = ""
		hyperliquid = ""
	}
	if accountPacifica != "" && strings.TrimSpace(accountPacifica) != pacifica {
		return nil, "", "", "", "", fmt.Errorf("Pacifica account conflicts with account bindings")
	}
	if accountHyperliquid != "" && !strings.EqualFold(strings.TrimSpace(accountHyperliquid), hyperliquid) {
		return nil, "", "", "", "", fmt.Errorf("Hyperliquid account conflicts with account bindings")
	}
	return normalized, encoded, key, pacifica, hyperliquid, nil
}

func decodeAccountBindings(encoded, key, accountPacifica, accountHyperliquid string) (map[string]string, error) {
	if encoded == "" && key == "" {
		pacifica := strings.TrimSpace(accountPacifica)
		hyperliquid := strings.ToLower(strings.TrimSpace(accountHyperliquid))
		if pacifica == "" && hyperliquid == "" {
			return nil, nil
		}
		return canonicalBindingsOnly(map[string]string{
			"pacifica": pacifica, "hyperliquid": hyperliquid,
		})
	}
	var keyBindings map[string]string
	if err := json.Unmarshal([]byte(key), &keyBindings); err != nil {
		return nil, fmt.Errorf("decode account bindings key: %w", err)
	}
	normalized, canonicalJSON, canonicalKey, err := canonicalAccountBindings(keyBindings)
	if err != nil {
		return nil, err
	}
	if key != canonicalKey {
		return nil, fmt.Errorf("account bindings key is not canonical")
	}
	if encoded != canonicalJSON {
		return normalized, fmt.Errorf("account bindings are not canonical")
	}
	if accountPacifica != "" || accountHyperliquid != "" {
		if normalized["pacifica"] != strings.TrimSpace(accountPacifica) ||
			normalized["hyperliquid"] != strings.ToLower(strings.TrimSpace(accountHyperliquid)) {
			return normalized, fmt.Errorf("account bindings conflict with legacy accounts")
		}
	}
	return normalized, nil
}

func canonicalBindingsOnly(bindings map[string]string) (map[string]string, error) {
	normalized, _, _, err := canonicalAccountBindings(bindings)
	return normalized, err
}

func requireBindingVenuePair(bindings map[string]string, rawVenueA, rawVenueB string) (string, string, error) {
	venueA := strings.ToLower(strings.TrimSpace(rawVenueA))
	venueB := strings.ToLower(strings.TrimSpace(rawVenueB))
	if venueA == "" || venueB == "" || venueA == venueB {
		return "", "", fmt.Errorf("two distinct position venues required")
	}
	if len(bindings) != 2 || bindings[venueA] == "" || bindings[venueB] == "" {
		return "", "", fmt.Errorf("account bindings do not match venue pair %s/%s", venueA, venueB)
	}
	return venueA, venueB, nil
}

func accountBindingsLookup(bindings map[string]string) (string, []any, error) {
	normalized, _, key, err := canonicalAccountBindings(bindings)
	if err != nil {
		return "", nil, err
	}
	if len(normalized) == 2 && normalized["pacifica"] != "" && normalized["hyperliquid"] != "" {
		return `(account_bindings_key = ? OR (
			account_bindings_key = '' AND account_pacifica = ? AND account_hyperliquid = ?
		))`, []any{key, normalized["pacifica"], normalized["hyperliquid"]}, nil
	}
	return `account_bindings_key = ?`, []any{key}, nil
}
