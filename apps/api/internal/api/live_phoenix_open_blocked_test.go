package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
	phoenixlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/live"
)

func TestPhoenixSizeCheckPrecedesBothLegsSigning(t *testing.T) {
	for _, streamed := range []bool{false, true} {
		for _, phoenixFirst := range []bool{false, true} {
			for _, unavailable := range []bool{false, true} {
				t.Run(fmt.Sprintf("stream=%t/phoenixFirst=%t/unavailable=%t", streamed, phoenixFirst, unavailable), func(t *testing.T) {
					f := newLocalPrepareFixture(t, "phoenix")
					if phoenixFirst {
						f.depth.quote.LongImpact = .0015
					} else {
						f.depth.quote.ShortImpact = .0015
					}
					phoenixModule, _ := f.s.live.liveModule("phoenix")
					probe := phoenixModule.(*admissionCheckpointModule)
					hl, _ := f.s.live.liveModule("hyperliquid")
					hlProbe := &admissionCheckpointModule{LiveModule: hl, name: "hyperliquid"}
					var err error
					f.s.live.modules, err = venue.NewLiveModuleRegistry(probe, hlProbe)
					if err != nil {
						t.Fatal(err)
					}
					calls := 0
					f.markets[0].openOrderCheck = func(_ context.Context, asset string, amount float64) (string, error) {
						calls++
						if asset != "BTC" || amount != .9 {
							t.Fatalf("wrong common base amount: %s %v", asset, amount)
						}
						if unavailable {
							return "", errors.New("catalog unavailable")
						}
						return venue.OpenBlockOpenInterestCap, nil
					}
					status, result, body, _ := f.prepare(t, context.Background(), streamed)
					var response map[string]string
					if err := json.Unmarshal([]byte(body), &response); err != nil {
						t.Fatal(err)
					}
					code := "OPEN_MARKET_BLOCKED"
					if unavailable {
						code = "OPEN_MARKET_STATUS_UNAVAILABLE"
					}
					if status != http.StatusUnprocessableEntity || response["code"] != code || calls != 1 {
						t.Fatalf("prepare=%d %s calls=%d", status, body, calls)
					}
					if probe.reached || hlProbe.reached || result.SessionID != "" || len(result.SigningRequests) != 0 {
						t.Fatal("cap check happened after signing construction")
					}
				})
			}
		}
	}
}

type capReductionPreparer struct{ intent phoenixlive.Intent }

func (*capReductionPreparer) NormalizeAmount(_ context.Context, _ string, amount float64) (float64, error) {
	return amount, nil
}
func (p *capReductionPreparer) Prepare(_ context.Context, intent phoenixlive.Intent) (*domain.SigningRequest, error) {
	p.intent = intent
	return &domain.SigningRequest{Venue: "phoenix", Amount: intent.Amount, ReduceOnly: intent.Action != "open"}, nil
}

func TestPhoenixCapDoesNotGateCloseOrKillPreparation(t *testing.T) {
	for _, emergency := range []bool{false, true} {
		s := newClosePriceServer(t, "hyperliquid", closePriceMarket(0, true))
		preparer := &capReductionPreparer{}
		module, err := phoenixlive.NewLiveModule(context.Background(), preparer)
		if err != nil {
			t.Fatal(err)
		}
		s.live.modules, err = venue.NewLiveModuleRegistry(module)
		if err != nil {
			t.Fatal(err)
		}
		checks := 0
		s.scanner = scanner.New(slog.Default(), &prepareMarketAdapter{
			data: venue.MarketData{Venue: "phoenix"}, openBlocked: map[string]string{"SOL": venue.OpenBlockOpenInterestCap},
			openOrderCheck: func(context.Context, string, float64) (string, error) {
				checks++
				return venue.OpenBlockOpenInterestCap, nil
			},
		})
		if err := s.scanner.CheckOpenAvailability("SOL", "phoenix"); err == nil {
			t.Fatal("test market allows opening")
		}
		request, err := s.buildCloseSigningRequestForBindings(context.Background(), executor.LiveFill{Venue: "phoenix", Symbol: "SOL", Side: "long", FilledAmount: 2, AvgFillPrice: 100}, "SOL", "phoenix-cap-close", liveVenueBindings{Accounts: map[string]string{"phoenix": phoenixTestOwner}, Agents: map[string]string{"phoenix": phoenixTestAgent}}, emergency)
		if err != nil || request == nil || !request.ReduceOnly || request.Amount != 2 || checks != 0 {
			t.Fatalf("close/kill gated by cap: %+v %v checks=%d", request, err, checks)
		}
		if preparer.intent.Action == "open" {
			t.Fatal("reduction became an opening")
		}
	}
}
