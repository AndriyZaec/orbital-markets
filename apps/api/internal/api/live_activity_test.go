package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	appdb "github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

func TestLiveActivityFiltersAccountPairsAndPaginatesLifecycleRows(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "activity.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	positions := []struct {
		id, venueA, venueB, state, pacifica, hyperliquid, bindings, openedAt, completedAt string
	}{
		{"pacifica-aster", "pacifica", "aster", "closed", "", "", `{"aster":"0xaster","pacifica":"sol-owner"}`, "2026-09-30T12:00:00Z", "2026-09-30T12:03:00Z"},
		{"legacy-pacifica-hyperliquid", "pacifica", "hyperliquid", "open", "sol-owner", "0xhyper", "", "2026-09-30T12:02:00Z", ""},
		{"hyperliquid-aster", "hyperliquid", "aster", "closed", "", "", `{"aster":"0xaster","hyperliquid":"0xhyper"}`, "2026-09-30T12:01:00Z", "2026-09-30T12:04:00Z"},
		{"other-account", "pacifica", "aster", "closed", "", "", `{"aster":"0xother","pacifica":"sol-owner"}`, "2026-09-30T12:05:00Z", "2026-09-30T12:05:00Z"},
	}
	for _, position := range positions {
		_, err := database.Exec(`
			INSERT INTO live_positions (
				id, plan_id, opportunity_id, asset, venue_a, venue_b, state,
				account_pacifica, account_hyperliquid, account_bindings_json, account_bindings_key,
				notional, leverage, started_at, opened_at, completed_at, updated_at
			) VALUES (?, ?, 'opportunity', 'SOL', ?, ?, ?, ?, ?, ?, ?, 100, 2,
				'2026-09-30T11:00:00Z', NULLIF(?, ''), NULLIF(?, ''), '2026-09-30T12:05:00Z')`,
			position.id, position.id, position.venueA, position.venueB, position.state,
			position.pacifica, position.hyperliquid, position.bindings, position.bindings,
			position.openedAt, position.completedAt,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(t.Context(), logger, nil, nil, nil, database, nil, "", "")
	query := url.Values{
		"accounts[pacifica]":    {"sol-owner"},
		"accounts[hyperliquid]": {"0xHyper"},
		"accounts[aster]":       {"0xAster"},
		"limit":                 {"2"},
	}

	first := getLiveActivity(t, server, query)
	assertActivityRows(t, first.Items, []string{"hyperliquid-aster:closed", "pacifica-aster:closed"})
	if first.NextCursor == "" {
		t.Fatal("first page omitted next_cursor")
	}

	query.Set("cursor", first.NextCursor)
	second := getLiveActivity(t, server, query)
	assertActivityRows(t, second.Items, []string{"legacy-pacifica-hyperliquid:opened"})
	if second.NextCursor != "" {
		t.Fatalf("last page next_cursor = %q, want empty", second.NextCursor)
	}

	query.Del("cursor")
	query.Set("type", "opened,closed")
	query.Set("limit", "50")
	all := getLiveActivity(t, server, query)
	if len(all.Items) != 3 {
		t.Fatalf("filtered lifecycle rows = %d, want 3", len(all.Items))
	}
	for _, item := range all.Items {
		if item.Position.ID == "other-account" {
			t.Fatal("activity included a position for an unbound account")
		}
	}

	query["type"] = []string{"closed"}
	closed := getLiveActivity(t, server, query)
	assertActivityRows(t, closed.Items, []string{"hyperliquid-aster:closed", "pacifica-aster:closed"})
}

func TestLiveActivityPaginatesEqualTimestampsByActivityID(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "activity-tie.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for _, id := range []string{"position-a", "position-b"} {
		_, err := database.Exec(`
			INSERT INTO live_positions (
				id, plan_id, opportunity_id, asset, venue_a, venue_b, state,
				account_pacifica, account_hyperliquid,
				notional, leverage, started_at, opened_at, updated_at
			) VALUES (?, ?, 'opportunity', 'SOL', 'pacifica', 'hyperliquid', 'open',
				'sol-owner', '0xhyper', 100, 2, '2026-09-30T11:00:00Z',
				'2026-09-30T12:00:00Z', '2026-09-30T12:00:00Z')`, id, id)
		if err != nil {
			t.Fatal(err)
		}
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(t.Context(), logger, nil, nil, nil, database, nil, "", "")
	query := url.Values{
		"accounts[pacifica]":    {"sol-owner"},
		"accounts[hyperliquid]": {"0xhyper"},
		"type":                  {"opened"},
		"limit":                 {"1"},
	}
	first := getLiveActivity(t, server, query)
	assertActivityRows(t, first.Items, []string{"position-b:opened"})
	query.Set("cursor", first.NextCursor)
	second := getLiveActivity(t, server, query)
	assertActivityRows(t, second.Items, []string{"position-a:opened"})
	if second.NextCursor != "" {
		t.Fatalf("last page next_cursor = %q, want empty", second.NextCursor)
	}
}

func TestLiveActivityRejectsInvalidQuery(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "activity-validation.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(t.Context(), logger, nil, nil, nil, database, nil, "", "")

	valid := url.Values{
		"accounts[pacifica]":    {"sol-owner"},
		"accounts[hyperliquid]": {"0xhyper"},
	}
	tests := map[string]url.Values{
		"one account": {
			"accounts[pacifica]": {"sol-owner"},
		},
		"unsupported venue": {
			"accounts[pacifica]": {"sol-owner"}, "accounts[other]": {"owner"},
		},
		"duplicate venue": {
			"accounts[pacifica]": {"sol-owner", "other"}, "accounts[hyperliquid]": {"0xhyper"},
		},
		"duplicate normalized venue": {
			"accounts[pacifica]": {"sol-owner"}, "accounts[ Pacifica ]": {"other"},
			"accounts[aster]": {"0xaster"},
		},
		"duplicate legacy alias": {
			"accounts[pacifica]": {"sol-owner"}, "account_pacifica": {"sol-owner"},
			"accounts[aster]": {"0xaster"},
		},
		"malformed account key": {
			"accounts[pacifica": {"sol-owner"}, "accounts[aster]": {"0xaster"},
		},
		"empty account": {
			"accounts[pacifica]": {""}, "accounts[aster]": {"0xaster"},
		},
	}
	for name, query := range tests {
		t.Run(name, func(t *testing.T) {
			assertLiveActivityStatus(t, server, query, http.StatusBadRequest)
		})
	}

	for name, value := range map[string]string{
		"unsupported type": "failed",
		"empty type":       "",
		"zero limit":       "0",
		"negative limit":   "-1",
		"oversized limit":  "51",
		"noninteger limit": "1.5",
		"bad cursor":       "not-base64!",
	} {
		t.Run(name, func(t *testing.T) {
			query := cloneQuery(valid)
			switch name {
			case "unsupported type", "empty type":
				query["type"] = []string{value}
			case "bad cursor":
				query.Set("cursor", value)
			default:
				query.Set("limit", value)
			}
			assertLiveActivityStatus(t, server, query, http.StatusBadRequest)
		})
	}
}

type liveActivityTestResponse struct {
	Items      []executor.LifecycleActivity `json:"items"`
	NextCursor string                       `json:"next_cursor"`
}

func getLiveActivity(t *testing.T, server *Server, query url.Values) liveActivityTestResponse {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/live/activity?"+query.Encode(), nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var body liveActivityTestResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func assertLiveActivityStatus(t *testing.T, server *Server, query url.Values, want int) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/live/activity?"+query.Encode(), nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("status = %d, want %d: %s", response.Code, want, response.Body.String())
	}
}

func cloneQuery(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for key, entries := range values {
		cloned[key] = append([]string(nil), entries...)
	}
	return cloned
}

func assertActivityRows(t *testing.T, items []executor.LifecycleActivity, want []string) {
	t.Helper()
	if len(items) != len(want) {
		t.Fatalf("activity rows = %d, want %d: %+v", len(items), len(want), items)
	}
	for i, id := range want {
		if items[i].ID != id {
			t.Fatalf("activity[%d].id = %q, want %q", i, items[i].ID, id)
		}
		if items[i].Position.ID == "" || items[i].At == "" {
			t.Fatalf("activity[%d] missing full position or timestamp: %+v", i, items[i])
		}
	}
}
