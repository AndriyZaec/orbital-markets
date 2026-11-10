package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/liquidity"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
	asterlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/live"
	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
)

type closeBookSourceFunc func(context.Context, liquidity.Market) (liquidity.BBOQuote, error)

func (f closeBookSourceFunc) FreshBBO(ctx context.Context, market liquidity.Market) (liquidity.BBOQuote, error) {
	return f(ctx, market)
}

type closeMarketSourceFunc func(context.Context, string, string) (venue.MarketData, error)

func (f closeMarketSourceFunc) MarketSnapshot(ctx context.Context, name, asset string) (venue.MarketData, error) {
	return f(ctx, name, asset)
}

func newClosePriceServer(t *testing.T, name string, market venue.MarketData) *Server {
	t.Helper()
	var module venue.LiveModule = hllive.NewLiveModule(closeQuoteTestAssetMap{})
	if name == "aster" {
		module = asterlive.NewLiveModule(hedgeMatrixRules{}, nil)
	}
	modules, err := venue.NewLiveModuleRegistry(module)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{live: &LiveDeps{modules: modules}, closeMarkets: closeQuoteTestSource{snapshot: market}}
}

func closePriceMarket(age time.Duration, available bool) venue.MarketData {
	status := venue.HealthAvailable
	if !available {
		status = venue.HealthUnavailable
	}
	return venue.MarketData{BidPrice: 100, AskPrice: 101, BidSize: 1000, AskSize: 1000, Timestamp: time.Now(),
		Health: &venue.MarketHealth{Book: venue.ComponentHealth{Status: status, SourceTime: time.Now().Add(-age)}}}
}

func TestCloseAndKillUseWSOrRESTWithoutWideningReduceOnlyBounds(t *testing.T) {
	for _, name := range []string{"hyperliquid", "aster"} {
		for _, side := range []string{"long", "short"} {
			for _, emergency := range []bool{false, true} {
				for _, scenario := range []string{"fresh_WS", "quiet_30s", "disconnected_WS", "REST_retry"} {
					t.Run(fmt.Sprintf("%s/%s/kill=%t/%s", name, side, emergency, scenario), func(t *testing.T) {
						age := 30 * time.Second
						if scenario == "fresh_WS" {
							age = 0
						}
						s := newClosePriceServer(t, name, closePriceMarket(age, scenario != "disconnected_WS"))
						symbol := "BTC"
						if name == "aster" {
							symbol = "BTCUSDT"
						}
						calls := 0
						s.closeBooks = closeBookSourceFunc(func(ctx context.Context, market liquidity.Market) (liquidity.BBOQuote, error) {
							calls++
							if market.Venue != name || market.Key != symbol {
								t.Fatalf("REST used wrong native market: %+v", market)
							}
							if scenario == "REST_retry" && calls == 1 {
								return liquidity.BBOQuote{}, liquidity.ErrUnavailable
							}
							return liquidity.BBOQuote{BidPrice: 100, AskPrice: 101, ObservedAt: time.Now()}, nil
						})
						request, err := s.buildCloseSigningRequestForBindings(context.Background(),
							executor.LiveFill{Venue: name, Symbol: symbol, Side: side, FilledAmount: 2, AvgFillPrice: 200},
							"BTC", "close-price-source", liveVenueBindings{
								Accounts: map[string]string{name: "0x1111111111111111111111111111111111111111"},
								Agents:   map[string]string{name: "0x2222222222222222222222222222222222222222"},
							}, emergency)
						if err != nil || request == nil {
							t.Fatalf("close preparation: %+v %v", request, err)
						}
						wantCalls := 1
						if scenario == "fresh_WS" {
							wantCalls = 0
						}
						if scenario == "REST_retry" {
							wantCalls = 2
						}
						if calls != wantCalls || !request.ReduceOnly || request.Amount != 2 {
							t.Fatalf("reads=%d want=%d request=%+v", calls, wantCalls, request)
						}
						slippage := 0.005
						if name == "aster" {
							slippage = 0.01
						}
						if side == "long" && (request.LimitPrice < 100*(1-slippage)-1e-9 || request.LimitPrice > 100+1e-9) ||
							side == "short" && (request.LimitPrice > 101*(1+slippage)+1e-9 || request.LimitPrice < 101-1e-9) {
							t.Fatalf("close widened the venue's book-based limit: %+v", request)
						}
					})
				}
			}
		}
	}
}

func TestCloseWaitsOnlyForCachedBBOAfterTwoRESTFailures(t *testing.T) {
	s := newClosePriceServer(t, "hyperliquid", closePriceMarket(30*time.Second, true))
	checks, calls := 0, 0
	s.closeMarkets = closeMarketSourceFunc(func(context.Context, string, string) (venue.MarketData, error) {
		checks++
		age := 30 * time.Second
		if checks >= 4 {
			age = 0
		}
		return closePriceMarket(age, true), nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	s.closeBooks = closeBookSourceFunc(func(ctx context.Context, _ liquidity.Market) (liquidity.BBOQuote, error) {
		calls++
		if got, _ := ctx.Deadline(); !got.Equal(deadline) {
			t.Fatal("REST attempt restarted the common deadline")
		}
		return liquidity.BBOQuote{}, liquidity.ErrUnavailable
	})
	request, err := s.buildCloseSigningRequest(ctx,
		executor.LiveFill{Venue: "hyperliquid", Symbol: "BTC", Side: "long", FilledAmount: 2, AvgFillPrice: 200},
		"cache-recovery", "pac-owner", "hl-owner", "pac-agent", "hl-agent")
	if err != nil || request == nil || request.Price != 100 || calls != 2 || checks < 4 {
		t.Fatalf("cache recovery: request=%+v err=%v REST=%d cache=%d", request, err, calls, checks)
	}
}

func newRESTCloseHandlerServer(t *testing.T) (*Server, liveVenueBindings, *sql.DB) {
	t.Helper()
	s, database := newResidualExposureServer(t)
	pac, _ := s.live.modules.Module("pacifica")
	modules, err := venue.NewLiveModuleRegistry(pac, hllive.NewLiveModule(closeQuoteTestAssetMap{}))
	if err != nil {
		t.Fatal(err)
	}
	s.live.modules = modules
	_, err = database.Exec(`INSERT INTO live_fills (
		position_id, leg, venue, symbol, side, requested_amount, filled_amount,
		avg_fill_price, fill_ratio, fee, accepted, filled, filled_at
	) VALUES ('position-residual', 2, 'hyperliquid', 'SOL', 'short', 2, 2, 200, 1, 0, 1, 1, '2026-07-22T12:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	s.closeMarkets = closeQuoteTestSource{snapshot: closePriceMarket(30*time.Second, true)}
	return s, liveVenueBindings{Accounts: map[string]string{"pacifica": "sol-wallet", "hyperliquid": "0xwallet"}, Agents: map[string]string{"pacifica": "sol-agent", "hyperliquid": "0xagent"}}, database
}

func TestCloseAndKillHandlersPrepareQuietMarketThroughREST(t *testing.T) {
	for _, kill := range []bool{false, true} {
		t.Run(fmt.Sprintf("kill=%t", kill), func(t *testing.T) {
			s, bindings, _ := newRESTCloseHandlerServer(t)
			calls := 0
			s.closeBooks = closeBookSourceFunc(func(context.Context, liquidity.Market) (liquidity.BBOQuote, error) {
				calls++
				return liquidity.BBOQuote{BidPrice: 100, AskPrice: 101, ObservedAt: time.Now()}, nil
			})
			path := "/api/v1/live/close/position-residual"
			if kill {
				path = "/api/v1/live/kill"
			}
			r := httptest.NewRequest(http.MethodPost, path, jsonBody(t, map[string]any{"accounts": bindings.Accounts, "agents": bindings.Agents}))
			w := httptest.NewRecorder()
			if kill {
				s.handleLiveKill(w, r)
			} else {
				s.handleLiveClose(w, r)
			}
			var body struct {
				SigningRequests []domain.SigningRequest `json:"signing_requests"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusOK || len(body.SigningRequests) != 2 || calls != 1 {
				t.Fatalf("quiet handler: status=%d body=%s reads=%d", w.Code, w.Body.String(), calls)
			}
		})
	}
}

func TestCloseAndKillPriceFailureUsesOneDeadlineAndStructuredCode(t *testing.T) {
	for _, kill := range []bool{false, true} {
		t.Run(fmt.Sprintf("kill=%t", kill), func(t *testing.T) {
			s, bindings, _ := newRESTCloseHandlerServer(t)
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			deadline, _ := ctx.Deadline()
			calls := 0
			s.closeBooks = closeBookSourceFunc(func(ctx context.Context, _ liquidity.Market) (liquidity.BBOQuote, error) {
				calls++
				if got, _ := ctx.Deadline(); !got.Equal(deadline) {
					t.Fatal("price attempt restarted deadline")
				}
				return liquidity.BBOQuote{}, liquidity.ErrUnavailable
			})
			path := "/api/v1/live/close/position-residual"
			if kill {
				path = "/api/v1/live/kill"
			}
			r := httptest.NewRequest(http.MethodPost, path, jsonBody(t, map[string]any{"accounts": bindings.Accounts, "agents": bindings.Agents})).WithContext(ctx)
			w := httptest.NewRecorder()
			if kill {
				s.handleLiveKill(w, r)
			} else {
				s.handleLiveClose(w, r)
			}
			var body struct {
				Code      string                  `json:"code"`
				Requests  []domain.SigningRequest `json:"signing_requests"`
				Positions []struct {
					Code string `json:"code"`
					Legs int    `json:"legs_to_close"`
				} `json:"positions"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			code := body.Code
			if kill && len(body.Positions) == 1 {
				code = body.Positions[0].Code
			}
			if code != "CLOSE_PREPARATION_UNAVAILABLE" || calls != 2 || !errors.Is(ctx.Err(), context.DeadlineExceeded) ||
				len(body.Requests) != 0 || kill && (len(body.Positions) != 1 || body.Positions[0].Legs != 0 || body.Requests == nil) {
				t.Fatalf("deadline failure: body=%s REST=%d ctx=%v", w.Body.String(), calls, ctx.Err())
			}
		})
	}
}

func TestKillLegBudgetLeavesTimeForNextPosition(t *testing.T) {
	for _, scenario := range []string{"fast_REST_failures_then_cache_wait", "slow_REST_failures"} {
		t.Run(scenario, func(t *testing.T) {
			s, bindings, database := newRESTCloseHandlerServer(t)
			for _, statement := range []string{
				`INSERT INTO live_positions (
					id, plan_id, opportunity_id, asset, venue_a, venue_b, state,
					account_pacifica, account_hyperliquid, notional, leverage,
					started_at, opened_at, updated_at
				) VALUES ('position-healthy', 'plan-healthy', 'opportunity-healthy',
					'SOL', 'pacifica', 'hyperliquid', 'open', 'sol-wallet', '0xwallet',
					10, 2, '2026-07-21T12:00:00Z', '2026-07-21T12:00:00Z', '2026-07-21T12:00:00Z')`,
				`INSERT INTO live_fills (
					position_id, leg, venue, symbol, side, requested_amount, filled_amount,
					avg_fill_price, fill_ratio, fee, accepted, filled, filled_at
				) SELECT 'position-healthy', leg, venue, symbol, side, requested_amount,
					filled_amount, avg_fill_price, fill_ratio, fee, accepted, filled, filled_at
					FROM live_fills WHERE position_id='position-residual'`,
				`DELETE FROM live_fills WHERE position_id='position-residual' AND venue='pacifica'`,
				`UPDATE live_positions SET asset='BAD' WHERE id='position-residual'`,
				`UPDATE live_fills SET symbol='BAD' WHERE position_id='position-residual'`,
			} {
				if _, err := database.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			// The newer, unavailable position must be processed first.
			positions, err := s.liveStore.ListCloseablePositionsForBindings(context.Background(), bindings.Accounts)
			if err != nil || len(positions) != 2 || positions[0].ID != "position-residual" {
				t.Fatalf("unexpected kill order: %+v %v", positions, err)
			}
			s.closeMarkets = closeMarketSourceFunc(func(context.Context, string, string) (venue.MarketData, error) {
				return closePriceMarket(30*time.Second, false), nil
			})
			badReads, healthyReads := 0, 0
			var failedLegDeadline time.Time
			s.closeBooks = closeBookSourceFunc(func(ctx context.Context, market liquidity.Market) (liquidity.BBOQuote, error) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 3*time.Second {
					t.Fatal("kill leg has more than its total 3s budget")
				}
				if market.Key != "BAD" {
					healthyReads++
					return liquidity.BBOQuote{BidPrice: 100, AskPrice: 101, ObservedAt: time.Now()}, nil
				}
				badReads++
				if badReads == 1 {
					failedLegDeadline = deadline
				} else if !deadline.Equal(failedLegDeadline) {
					t.Fatal("REST retry restarted the leg deadline")
				}
				if scenario == "slow_REST_failures" {
					select {
					case <-ctx.Done():
						return liquidity.BBOQuote{}, ctx.Err()
					case <-time.After(2 * time.Second):
					}
				}
				return liquidity.BBOQuote{}, liquidity.ErrUnavailable
			})
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/live/kill", jsonBody(t,
				map[string]any{"accounts": bindings.Accounts, "agents": bindings.Agents})).WithContext(ctx)
			w := httptest.NewRecorder()
			started := time.Now()
			s.handleLiveKill(w, r)
			var body struct {
				Requests  []domain.SigningRequest `json:"signing_requests"`
				Positions []struct {
					ID   string `json:"id"`
					Legs int    `json:"legs_to_close"`
					Code string `json:"code"`
				} `json:"positions"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusOK || ctx.Err() != nil || time.Since(started) >= 8*time.Second ||
				len(body.Positions) != 2 || body.Positions[0].Code != "CLOSE_PREPARATION_UNAVAILABLE" ||
				body.Positions[1].ID != "position-healthy" || body.Positions[1].Legs != 2 || body.Positions[1].Code != "" ||
				len(body.Requests) != 2 || badReads < 1 || badReads > 2 || healthyReads != 1 ||
				scenario == "fast_REST_failures_then_cache_wait" && badReads != 2 {
				t.Fatalf("failed leg starved healthy position: body=%s bad=%d healthy=%d ctx=%v", w.Body.String(), badReads, healthyReads, ctx.Err())
			}
			for _, request := range body.Requests {
				if request.PositionID != "position-healthy" || !request.ReduceOnly {
					t.Fatalf("unexpected prepared request: %+v", request)
				}
			}
		})
	}
}
