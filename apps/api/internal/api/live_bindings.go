package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
)

var currentLiveVenues = []string{"pacifica", "hyperliquid"}

type liveVenueBindingsRequest struct {
	Accounts uniqueVenueBindings `json:"accounts,omitempty"`
	Agents   uniqueVenueBindings `json:"agents,omitempty"`

	AccountPacifica    string `json:"account_pacifica,omitempty"`
	AccountHyperliquid string `json:"account_hyperliquid,omitempty"`
	AgentPacifica      string `json:"agent_pacifica,omitempty"`
	AgentHyperliquid   string `json:"agent_hyperliquid,omitempty"`
}

type uniqueVenueBindings map[string]string

func (bindings *uniqueVenueBindings) UnmarshalJSON(data []byte) error {
	if *bindings != nil {
		return fmt.Errorf("duplicate venue bindings object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := start.(json.Delim); !ok || delimiter != '{' {
		return fmt.Errorf("venue bindings must be an object")
	}

	result := make(uniqueVenueBindings)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("venue binding key must be a string")
		}
		if _, exists := result[key]; exists {
			return fmt.Errorf("duplicate venue binding key %q", key)
		}
		var value string
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		result[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected data after venue bindings")
		}
		return err
	}
	*bindings = result
	return nil
}

type liveVenueBindings struct {
	Accounts map[string]string
	Agents   map[string]string
}

func (bindings liveVenueBindings) clone() liveVenueBindings {
	return liveVenueBindings{
		Accounts: cloneVenueBindingMap(bindings.Accounts),
		Agents:   cloneVenueBindingMap(bindings.Agents),
	}
}

func cloneVenueBindingMap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for venue, value := range source {
		cloned[venue] = value
	}
	return cloned
}

func (bindings liveVenueBindings) matchesAccounts(accounts map[string]string) bool {
	if len(bindings.Accounts) != len(accounts) {
		return false
	}
	for venue, expected := range bindings.Accounts {
		if !sameVenueBinding(venue, expected, accounts[venue]) {
			return false
		}
	}
	return true
}

func (bindings liveVenueBindings) requireAccountsFor(venues []string) error {
	return requireVenueBindings("account", bindings.Accounts, venues)
}

func (bindings liveVenueBindings) requireAgentsFor(venues []string) error {
	return requireVenueBindings("agent", bindings.Agents, venues)
}

func (bindings liveVenueBindings) requireAccountsWithin(venues []string) error {
	allowed := make(map[string]struct{}, len(venues))
	for _, venue := range venues {
		allowed[venue] = struct{}{}
	}
	for venue := range bindings.Accounts {
		if _, ok := allowed[venue]; !ok {
			return fmt.Errorf("unsupported live venue %q", venue)
		}
	}
	return nil
}

func (bindings liveVenueBindings) requirePair() error {
	if len(bindings.Accounts) != 2 || len(bindings.Agents) != 2 {
		return fmt.Errorf("live execution requires account and agent bindings for exactly two venues")
	}
	for venue := range bindings.Accounts {
		if bindings.Agents[venue] == "" {
			return fmt.Errorf("agent.%s required", venue)
		}
	}
	for venue := range bindings.Agents {
		if bindings.Accounts[venue] == "" {
			return fmt.Errorf("account.%s required", venue)
		}
	}
	return nil
}

func requireVenueBindings(kind string, bindings map[string]string, venues []string) error {
	if len(venues) != 2 || venues[0] == venues[1] {
		return fmt.Errorf("live execution requires exactly two distinct venues")
	}
	if len(bindings) != len(venues) {
		return fmt.Errorf("%s bindings required for both plan venues", kind)
	}
	for _, venue := range venues {
		if strings.TrimSpace(bindings[venue]) == "" {
			return fmt.Errorf("%s.%s required", kind, venue)
		}
	}
	return nil
}

func (request liveVenueBindingsRequest) resolve() (liveVenueBindings, error) {
	accounts, err := normalizeVenueBindings("accounts", request.Accounts)
	if err != nil {
		return liveVenueBindings{}, err
	}
	agents, err := normalizeVenueBindings("agents", request.Agents)
	if err != nil {
		return liveVenueBindings{}, err
	}

	legacyAccounts := map[string]string{
		"pacifica": request.AccountPacifica, "hyperliquid": request.AccountHyperliquid,
	}
	legacyAgents := map[string]string{
		"pacifica": request.AgentPacifica, "hyperliquid": request.AgentHyperliquid,
	}
	if err := mergeLegacyVenueBindings("accounts", accounts, legacyAccounts); err != nil {
		return liveVenueBindings{}, err
	}
	if err := mergeLegacyVenueBindings("agents", agents, legacyAgents); err != nil {
		return liveVenueBindings{}, err
	}
	return liveVenueBindings{Accounts: accounts, Agents: agents}, nil
}

func normalizeVenueBindings(kind string, input uniqueVenueBindings) (map[string]string, error) {
	result := make(map[string]string, len(input))
	seen := make(map[string]struct{}, len(input))
	for rawVenue, rawValue := range input {
		venue := strings.ToLower(strings.TrimSpace(rawVenue))
		if venue == "" {
			return nil, fmt.Errorf("%s venue must not be empty", kind)
		}
		if _, duplicate := seen[venue]; duplicate {
			return nil, fmt.Errorf("duplicate %s binding for %s", kind, venue)
		}
		seen[venue] = struct{}{}
		value := strings.TrimSpace(rawValue)
		if value == "" {
			return nil, fmt.Errorf("%s.%s must not be empty", kind, venue)
		}
		result[venue] = value
	}
	return result, nil
}

func mergeLegacyVenueBindings(kind string, target, legacy map[string]string) error {
	for venue, rawValue := range legacy {
		value := strings.TrimSpace(rawValue)
		if value == "" {
			continue
		}
		if existing := target[venue]; existing != "" && !sameVenueBinding(venue, existing, value) {
			return fmt.Errorf("%s.%s conflicts with legacy %s_%s", kind, venue, strings.TrimSuffix(kind, "s"), venue)
		}
		target[venue] = value
	}
	return nil
}

func (bindings liveVenueBindings) requireAccounts() error {
	if err := bindings.requireAccountsFor(currentLiveVenues); err != nil {
		return fmt.Errorf("account_pacifica and account_hyperliquid required")
	}
	return nil
}

func (bindings liveVenueBindings) requireAgents() error {
	if err := bindings.requireAgentsFor(currentLiveVenues); err != nil {
		return fmt.Errorf("agent_pacifica and agent_hyperliquid required")
	}
	return nil
}

func sameVenueBinding(venue, left, right string) bool {
	if venue == "hyperliquid" || venue == "aster" {
		return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
	}
	return strings.TrimSpace(left) == strings.TrimSpace(right)
}

func liveVenueBindingsFromQuery(values url.Values) (liveVenueBindings, error) {
	accounts := make(uniqueVenueBindings)
	for key, queryValues := range values {
		if !strings.HasPrefix(key, "accounts[") || !strings.HasSuffix(key, "]") {
			continue
		}
		if len(queryValues) != 1 {
			return liveVenueBindings{}, fmt.Errorf("duplicate accounts binding for %s", key)
		}
		venue := strings.TrimSuffix(strings.TrimPrefix(key, "accounts["), "]")
		accounts[venue] = queryValues[0]
	}
	pacifica, err := singleQueryValue(values, "account_pacifica")
	if err != nil {
		return liveVenueBindings{}, err
	}
	hyperliquid, err := singleQueryValue(values, "account_hyperliquid")
	if err != nil {
		return liveVenueBindings{}, err
	}
	return (liveVenueBindingsRequest{
		Accounts: accounts, AccountPacifica: pacifica, AccountHyperliquid: hyperliquid,
	}).resolve()
}

func singleQueryValue(values url.Values, key string) (string, error) {
	queryValues := values[key]
	if len(queryValues) > 1 {
		return "", fmt.Errorf("duplicate query parameter %s", key)
	}
	if len(queryValues) == 0 {
		return "", nil
	}
	return queryValues[0], nil
}
