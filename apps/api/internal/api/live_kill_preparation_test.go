package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type killPreparationProbe struct {
	venue.LiveModule
	failSymbol string
	attempted  []string
	prepared   []*domain.SigningRequest
}

func (p *killPreparationProbe) BuildReduce(params venue.LiveReduceParams) (*domain.SigningRequest, error) {
	p.attempted = append(p.attempted, params.Symbol)
	if params.Symbol == p.failSymbol {
		return nil, errors.New("test close payload unavailable")
	}
	request, err := p.LiveModule.BuildReduce(params)
	if err == nil {
		p.prepared = append(p.prepared, request)
	}
	return request, err
}

type killPreparationResult struct {
	Targeted  int                     `json:"targeted"`
	Requests  []domain.SigningRequest `json:"signing_requests"`
	Positions []struct {
		ID       string `json:"id"`
		Legs     int    `json:"legs_to_close"`
		Code     string `json:"code"`
		Error    string `json:"error"`
		Exposure []struct {
			Leg    int     `json:"leg"`
			Venue  string  `json:"venue"`
			Amount float64 `json:"amount"`
		} `json:"remaining_exposure"`
	} `json:"positions"`
}

func prepareKillResult(t *testing.T, s *Server, bindings liveVenueBindings) killPreparationResult {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/live/kill", jsonBody(t,
		map[string]any{"accounts": bindings.Accounts, "agents": bindings.Agents}))
	w := httptest.NewRecorder()
	s.handleLiveKill(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("kill status=%d body=%s", w.Code, w.Body.String())
	}
	var result killPreparationResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Requests == nil {
		t.Fatal("kill signing_requests must be a JSON array, including on complete preparation failure")
	}
	return result
}

func probeKillModules(t *testing.T, s *Server, failingVenue string) []*killPreparationProbe {
	t.Helper()
	var probes []*killPreparationProbe
	var modules []venue.LiveModule
	for _, name := range []string{"pacifica", "hyperliquid"} {
		module, ok := s.live.modules.Module(name)
		if !ok {
			t.Fatalf("live module %q not registered", name)
		}
		probe := &killPreparationProbe{LiveModule: module}
		if name == failingVenue {
			probe.failSymbol = "SOL"
		}
		probes, modules = append(probes, probe), append(modules, probe)
	}
	registry, err := venue.NewLiveModuleRegistry(modules...)
	if err != nil {
		t.Fatal(err)
	}
	s.live.modules = registry
	s.closeMarkets = closeQuoteTestSource{snapshot: closePriceMarket(0, true)}
	return probes
}

func signingRequestPresence(s *Server, request *domain.SigningRequest) error {
	signer := request.Signer
	if signer == "" {
		signer = request.Account
	}
	_, err := s.live.signingStore.Validate(domain.SignedAction{
		RequestID: request.ID, ClientOrderID: request.ClientOrderID, Venue: request.Venue,
		SignerAddress: signer, Signature: "test-presence-only",
	})
	return err
}

func addHealthyKillPosition(t *testing.T, database *sql.DB) {
	t.Helper()
	for _, statement := range []string{
		`INSERT INTO live_positions (
			id, plan_id, opportunity_id, asset, venue_a, venue_b, state,
			account_pacifica, account_hyperliquid, notional, leverage,
			started_at, opened_at, updated_at
		) VALUES ('position-healthy', 'plan-healthy', 'opportunity-healthy',
			'BTC', 'pacifica', 'hyperliquid', 'open', 'sol-wallet', '0xwallet',
			10, 2, '2026-07-21T12:00:00Z', '2026-07-21T12:00:00Z', '2026-07-21T12:00:00Z')`,
		`INSERT INTO live_fills (
			position_id, leg, venue, symbol, side, requested_amount, filled_amount,
			avg_fill_price, fill_ratio, fee, accepted, filled, filled_at
		) SELECT 'position-healthy', leg, venue, 'BTC', side, requested_amount,
			filled_amount, avg_fill_price, fill_ratio, fee, accepted, filled, filled_at
			FROM live_fills WHERE position_id='position-residual'`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func TestKillPreparationPublishesNoPartialPositionAndContinuesOthers(t *testing.T) {
	for _, failingVenue := range []string{"pacifica", "hyperliquid"} {
		t.Run("fails_"+failingVenue, func(t *testing.T) {
			s, bindings, database := newRESTCloseHandlerServer(t)
			addHealthyKillPosition(t, database)
			probes := probeKillModules(t, s, failingVenue)
			body := prepareKillResult(t, s, bindings)
			if body.Targeted != 2 || len(body.Positions) != 2 || len(body.Requests) != 2 {
				t.Fatalf("unexpected kill result: %+v", body)
			}
			failed, healthy := body.Positions[0], body.Positions[1]
			if failed.ID != "position-residual" || failed.Legs != 0 || failed.Code != "CLOSE_PREPARATION_UNAVAILABLE" || failed.Error == "" || len(failed.Exposure) != 2 {
				t.Fatalf("failed position leaked a partial preparation or lost exposure: %+v", failed)
			}
			if failed.Exposure[0].Leg != 1 || failed.Exposure[0].Venue != "pacifica" || failed.Exposure[0].Amount != 2.75 ||
				failed.Exposure[1].Leg != 2 || failed.Exposure[1].Venue != "hyperliquid" || failed.Exposure[1].Amount != 2 {
				t.Fatalf("failed position exposure changed: %+v", failed.Exposure)
			}
			if healthy.ID != "position-healthy" || healthy.Legs != 2 || healthy.Code != "" || healthy.Error != "" {
				t.Fatalf("unrelated position was blocked: %+v", healthy)
			}
			for _, request := range body.Requests {
				if request.PositionID != "position-healthy" || !request.ReduceOnly || request.Action != "emergency_close" {
					t.Fatalf("failed position leaked a returned request: %+v", request)
				}
			}
			for _, probe := range probes {
				for _, request := range probe.prepared {
					err := signingRequestPresence(s, request)
					if request.PositionID == "position-residual" {
						if err == nil || !strings.Contains(err.Error(), "unknown request id") {
							t.Fatalf("discarded request is still signable: request=%+v err=%v", request, err)
						}
					} else if err != nil {
						t.Fatalf("healthy position request not stored: %v", err)
					}
				}
			}
			if failingVenue == "pacifica" {
				for _, symbol := range probes[1].attempted {
					if symbol == "SOL" {
						t.Fatal("continued preparing legs after this position already failed")
					}
				}
			}
			var failedEvents, healthyEvents int
			if err := database.QueryRow(`SELECT COUNT(*) FROM live_events WHERE position_id='position-residual' AND event='emergency_close_prepared'`).Scan(&failedEvents); err != nil {
				t.Fatal(err)
			}
			if err := database.QueryRow(`SELECT COUNT(*) FROM live_events WHERE position_id='position-healthy' AND event='emergency_close_prepared'`).Scan(&healthyEvents); err != nil {
				t.Fatal(err)
			}
			if failedEvents != 0 || healthyEvents != 1 {
				t.Fatalf("prepared events: failed=%d healthy=%d", failedEvents, healthyEvents)
			}
		})
	}
}

func TestKillFailedPreparationReturnsNoRequestsAndStoresNoPrefix(t *testing.T) {
	s, bindings, _ := newRESTCloseHandlerServer(t)
	probes := probeKillModules(t, s, "hyperliquid")
	body := prepareKillResult(t, s, bindings)
	if body.Targeted != 1 || len(body.Requests) != 0 || len(body.Positions) != 1 ||
		body.Positions[0].Legs != 0 || body.Positions[0].Code != "CLOSE_PREPARATION_UNAVAILABLE" || len(body.Positions[0].Exposure) != 2 {
		t.Fatalf("failed preparation returned a partial batch: %+v", body)
	}
	if len(probes[0].prepared) != 1 {
		t.Fatal("test did not reach a prepared first leg before second-leg failure")
	}
	if err := signingRequestPresence(s, probes[0].prepared[0]); err == nil || !strings.Contains(err.Error(), "unknown request id") {
		t.Fatalf("first-leg prefix was stored despite second-leg failure: %v", err)
	}
}

func TestKillPreparationRequiresOnlyOutstandingLegs(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		t.Run(map[bool]string{false: "both_legs", true: "one_leg_already_closed"}[confirmed], func(t *testing.T) {
			s, bindings, database := newRESTCloseHandlerServer(t)
			probes := probeKillModules(t, s, "")
			want := 2
			if confirmed {
				want = 1
				probes[1].failSymbol = "SOL" // Must not attempt the already closed leg.
				_, err := database.Exec(`INSERT INTO live_close_outcomes (
					position_id, leg, venue, client_order_id, requested_amount, filled_amount,
					accepted, confirmed, resolved, created_at, updated_at
				) VALUES ('position-residual', 2, 'hyperliquid', 'confirmed-close', 2, 2,
					1, 1, 1, '2026-07-22T12:01:00Z', '2026-07-22T12:01:00Z')`)
				if err != nil {
					t.Fatal(err)
				}
			}
			body := prepareKillResult(t, s, bindings)
			if len(body.Positions) != 1 || body.Positions[0].Legs != want || body.Positions[0].Error != "" || len(body.Requests) != want || len(body.Positions[0].Exposure) != want {
				t.Fatalf("outstanding-leg preparation: %+v", body)
			}
			if confirmed && len(probes[1].attempted) != 0 {
				t.Fatal("confirmed leg was prepared again")
			}
			for _, request := range body.Requests {
				if err := signingRequestPresence(s, &request); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
