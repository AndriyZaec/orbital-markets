package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	asterlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/live"
	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
)

func TestHyperliquidAllowedAgentNamesFitVenueLimit(t *testing.T) {
	for _, name := range []string{hllive.DefaultAgentName, hllive.StagingAgentName} {
		t.Run(name, func(t *testing.T) {
			if _, err := hllive.ParseAgentName(name); err != nil {
				t.Fatal(err)
			}
			if length := utf8.RuneCountInString(name); length > 16 {
				t.Fatalf("Hyperliquid agent name %q has %d characters; maximum is 16", name, length)
			}
		})
	}
}

func TestAgentApprovalNamesConfigAndFailClosed(t *testing.T) {
	for _, test := range []struct {
		hl, aster             string
		hlStatus, asterStatus int
	}{
		{"", "", 200, 200},
		{hllive.StagingAgentName, asterlive.StagingAgentName, 200, 200},
		{"Other", "", 503, 200},
		{"", "OrbitalStg\n", 200, 503},
		{"OrbitalMarketsStg", "OrbitalMarketsStg", 503, 503},
		{"bad name", "Other", 503, 503},
	} {
		t.Run(test.hl+"/"+test.aster, func(t *testing.T) {
			var logs bytes.Buffer
			deps := &LiveDeps{logger: slog.New(slog.NewJSONHandler(&logs, nil)), hlAgentApprover: &fakeHyperliquidAgentApprover{}, asterAgentApprover: &fakeAsterAgentApprover{}}
			deps.ConfigureAgentNames(test.hl, test.aster)
			server := &Server{live: deps}
			mux := http.NewServeMux()
			server.mux = mux
			server.routes()
			for _, venue := range []string{"hyperliquid", "aster"} {
				wantStatus, expected := test.hlStatus, test.hl
				if venue == "aster" {
					wantStatus, expected = test.asterStatus, test.aster
				}
				if expected == "" {
					expected = hllive.DefaultAgentName
					if venue == "aster" {
						expected = asterlive.DefaultAgentName
					}
				}
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/live/agents/"+venue+"/approval-config", nil))
				if w.Code != wantStatus {
					t.Fatalf("%s: HTTP %d body=%s", venue, w.Code, w.Body.String())
				}
				if wantStatus == 200 {
					var body map[string]string
					if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body) != 1 || body["agent_name"] != expected {
						t.Fatalf("wrong approval config: %s err=%v", w.Body.String(), err)
					}
				} else {
					if !strings.Contains(logs.String(), `"level":"WARN"`) {
						t.Fatal("invalid configuration did not log WARN")
					}
					request := httptest.NewRequest(http.MethodPost, "/unused", strings.NewReader(`{}`))
					disabled := httptest.NewRecorder()
					if venue == "aster" {
						server.handleAsterAgentApprove(disabled, request)
					} else {
						server.handleHyperliquidAgentApprove(disabled, request)
					}
					if disabled.Code != http.StatusServiceUnavailable {
						t.Fatal("invalid config did not disable approvals")
					}
				}
			}
		})
	}
}

func TestAgentApprovalNamesAcrossBrowserAndVenueSerialization(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required for browser signing contract")
	}
	web := filepath.Join("..", "..", "..", "web")
	if _, err := os.Stat(filepath.Join(web, "node_modules", "viem")); err != nil {
		t.Skip("install web dependencies for browser signing contract")
	}
	for _, staging := range []bool{false, true} {
		for _, venue := range []string{"hyperliquid", "aster"} {
			t.Run(venue+map[bool]string{false: "/production", true: "/staging"}[staging], func(t *testing.T) {
				names := make(chan string, 3)
				exchange := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/hl" {
						var wire struct {
							Action hllive.ApproveAgentAction `json:"action"`
						}
						if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
							t.Error(err)
						}
						names <- wire.Action.AgentName
						_, _ = io.WriteString(w, `{"status":"ok","response":{"type":"default"}}`)
					} else {
						if r.URL.Path == "/builder" {
							names <- r.FormValue("builderName")
						} else {
							names <- r.FormValue("agentName")
						}
						_, _ = io.WriteString(w, `{"code":200}`)
					}
				}))
				defer exchange.Close()
				deps := &LiveDeps{
					logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
					hlAgentApprover:    hllive.NewAgentApprover(exchange.URL+"/hl", exchange.Client()),
					asterAgentApprover: asterlive.NewAgentApprover(exchange.URL+"/agent", exchange.URL+"/builder", exchange.Client()),
					asterBuilder:       &asterlive.BuilderConfig{Address: hllive.OrbitalBuilderCode().Address, FeeRate: "0.0002"},
				}
				hlName, asterName := "", ""
				if staging {
					hlName, asterName = hllive.StagingAgentName, asterlive.StagingAgentName
				}
				deps.ConfigureAgentNames(hlName, asterName)
				server := &Server{live: deps, logger: deps.logger}
				mux := http.NewServeMux()
				server.mux = mux
				server.routes()
				config := httptest.NewRecorder()
				mux.ServeHTTP(config, httptest.NewRequest(http.MethodGet, "/api/v1/live/agents/"+venue+"/approval-config", nil))
				if config.Code != http.StatusOK {
					t.Fatal(config.Body.String())
				}
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				signConfig := func(configJSON string) []byte {
					input, _ := json.Marshal(map[string]string{"venue": venue, "config": configJSON})
					cmd := exec.CommandContext(ctx, "node", filepath.Join("tests", "fixtures", "trading-agent-approval.ts"))
					cmd.Dir, cmd.Stdin = web, bytes.NewReader(input)
					var stderr bytes.Buffer
					cmd.Stderr = &stderr
					body, err := cmd.Output()
					if err != nil {
						t.Fatalf("browser approval: %v %s", err, stderr.String())
					}
					return body
				}
				body := signConfig(config.Body.String())
				w := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/api/v1/live/agents/"+venue+"/approve", bytes.NewReader(body))
				mux.ServeHTTP(w, request)
				if w.Code != http.StatusNoContent {
					t.Fatalf("approval HTTP %d: %s", w.Code, w.Body.String())
				}
				if venue == "aster" {
					if builder := <-names; builder != "OrbitalMarkets" {
						t.Fatalf("builder renamed to %q", builder)
					}
				}
				expected := hllive.DefaultAgentName
				if venue == "aster" {
					expected = asterlive.DefaultAgentName
				}
				if staging {
					if venue == "aster" {
						expected = asterlive.StagingAgentName
					} else {
						expected = hllive.StagingAgentName
					}
				}
				if got := <-names; got != expected {
					t.Fatalf("venue name=%q want=%q", got, expected)
				}
				// Changing the requested name must be rejected before any relay,
				// even if it is a valid name for the other environment.
				otherName := hllive.StagingAgentName
				if venue == "aster" {
					otherName = asterlive.StagingAgentName
				}
				if staging {
					if venue == "aster" {
						otherName = asterlive.DefaultAgentName
					} else {
						otherName = hllive.DefaultAgentName
					}
				}
				wrongConfig, _ := json.Marshal(map[string]string{"agent_name": otherName})
				badBody := signConfig(string(wrongConfig))
				bad := httptest.NewRecorder()
				badRequest := httptest.NewRequest(http.MethodPost, "/unused", bytes.NewReader(badBody))
				if venue == "aster" {
					server.handleAsterAgentApprove(bad, badRequest)
				} else {
					server.handleHyperliquidAgentApprove(bad, badRequest)
				}
				if bad.Code != http.StatusBadRequest || len(names) != 0 {
					t.Fatal("wrong configured name was relayed")
				}
			})
		}
	}
}
