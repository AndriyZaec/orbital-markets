package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

const (
	liveActivityDefaultLimit = 20
	liveActivityMaxLimit     = 50
)

type liveActivityResponse struct {
	Items      []executor.LifecycleActivity `json:"items"`
	NextCursor string                       `json:"next_cursor,omitempty"`
}

type liveActivityCursor struct {
	At string `json:"at"`
	ID string `json:"id"`
}

func (s *Server) handleLiveActivity(w http.ResponseWriter, r *http.Request) {
	accounts, err := liveActivityAccounts(r.URL.Query())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	types, err := liveActivityTypes(r.URL.Query()["type"])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	limit, err := liveActivityLimit(r.URL.Query())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	cursor, err := decodeLiveActivityCursor(r.URL.Query())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	items, err := s.liveStore.ListLifecycleActivity(r.Context(), executor.LifecycleActivityQuery{
		Accounts: accounts,
		Types:    types,
		Before:   cursor,
		Limit:    limit + 1,
	})
	if err != nil {
		s.logger.Error("live activity: list", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list live activity"})
		return
	}

	response := liveActivityResponse{Items: items}
	if len(items) > limit {
		response.Items = items[:limit]
		last := response.Items[len(response.Items)-1]
		response.NextCursor = encodeLiveActivityCursor(last.At, last.ID)
	}
	writeJSON(w, http.StatusOK, response)
}

func liveActivityAccounts(values url.Values) (map[string]string, error) {
	accounts := make(map[string]string)
	add := func(rawVenue, rawAccount string) error {
		venue := strings.ToLower(strings.TrimSpace(rawVenue))
		if venue == "" || !supportedLiveVenue(venue) {
			return fmt.Errorf("unsupported live venue %q", venue)
		}
		if _, duplicate := accounts[venue]; duplicate {
			return fmt.Errorf("duplicate accounts binding for %s", venue)
		}
		account := strings.TrimSpace(rawAccount)
		if account == "" {
			return fmt.Errorf("accounts.%s must not be empty", venue)
		}
		if venue == "hyperliquid" || venue == "aster" {
			account = strings.ToLower(account)
		}
		accounts[venue] = account
		return nil
	}

	for key, queryValues := range values {
		if !strings.HasPrefix(key, "accounts") {
			continue
		}
		if !strings.HasPrefix(key, "accounts[") || !strings.HasSuffix(key, "]") {
			return nil, fmt.Errorf("malformed account binding %q", key)
		}
		if len(queryValues) != 1 {
			return nil, fmt.Errorf("duplicate accounts binding for %s", key)
		}
		venue := strings.TrimSuffix(strings.TrimPrefix(key, "accounts["), "]")
		if err := add(venue, queryValues[0]); err != nil {
			return nil, err
		}
	}
	for _, venue := range []string{"pacifica", "hyperliquid"} {
		key := "account_" + venue
		queryValues, present := values[key]
		if !present {
			continue
		}
		if len(queryValues) != 1 {
			return nil, fmt.Errorf("duplicate query parameter %s", key)
		}
		if err := add(venue, queryValues[0]); err != nil {
			return nil, err
		}
	}
	if len(accounts) < 2 || len(accounts) > 3 {
		return nil, fmt.Errorf("account bindings required for two or three supported venues")
	}
	return accounts, nil
}

func liveActivityTypes(rawValues []string) ([]executor.LifecycleActivityType, error) {
	if len(rawValues) == 0 {
		return []executor.LifecycleActivityType{
			executor.LifecycleActivityOpened,
			executor.LifecycleActivityClosed,
		}, nil
	}
	seen := make(map[executor.LifecycleActivityType]bool)
	for _, rawValue := range rawValues {
		for _, value := range strings.Split(rawValue, ",") {
			activityType := executor.LifecycleActivityType(strings.TrimSpace(value))
			switch activityType {
			case executor.LifecycleActivityOpened, executor.LifecycleActivityClosed:
				seen[activityType] = true
			default:
				return nil, fmt.Errorf("unsupported activity type %q", value)
			}
		}
	}
	types := make([]executor.LifecycleActivityType, 0, 2)
	for _, activityType := range []executor.LifecycleActivityType{
		executor.LifecycleActivityOpened, executor.LifecycleActivityClosed,
	} {
		if seen[activityType] {
			types = append(types, activityType)
		}
	}
	return types, nil
}

func liveActivityLimit(values url.Values) (int, error) {
	raw, err := singleQueryValue(values, "limit")
	if err != nil {
		return 0, err
	}
	if raw == "" {
		return liveActivityDefaultLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 || limit > liveActivityMaxLimit {
		return 0, fmt.Errorf("limit must be a positive integer no greater than %d", liveActivityMaxLimit)
	}
	return limit, nil
}

func decodeLiveActivityCursor(values url.Values) (*executor.LifecycleActivityCursor, error) {
	raw, err := singleQueryValue(values, "cursor")
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid cursor")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var cursor liveActivityCursor
	if err := decoder.Decode(&cursor); err != nil {
		return nil, fmt.Errorf("invalid cursor")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("invalid cursor")
	}
	if cursor.ID == "" {
		return nil, fmt.Errorf("invalid cursor")
	}
	if _, err := time.Parse(time.RFC3339Nano, cursor.At); err != nil {
		return nil, fmt.Errorf("invalid cursor")
	}
	return &executor.LifecycleActivityCursor{At: cursor.At, ID: cursor.ID}, nil
}

func encodeLiveActivityCursor(at, id string) string {
	data, _ := json.Marshal(liveActivityCursor{At: at, ID: id})
	return base64.RawURLEncoding.EncodeToString(data)
}
