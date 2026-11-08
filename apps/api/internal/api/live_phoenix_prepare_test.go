package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/delegation"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	phoenixlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/live"
	phoenixrisk "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/risk"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/rpc"
	"github.com/mr-tron/base58"
)

type phoenixOrderTestMarkets struct {
	base     *phoenixTestMarkets
	sequence atomic.Uint64
	quoteAge atomic.Int64
}

func orderTestKey(n byte) string {
	bytes := make([]byte, 32)
	for i := range bytes {
		bytes[i] = n
	}
	return base58.Encode(bytes)
}
func (m *phoenixOrderTestMarkets) Snapshot() phoenix.MarketStreamSnapshot {
	s := m.base.Snapshot()
	s.Sequence += m.sequence.Load()
	s.MetadataGeneration = 7
	s.Metadata.Accounts = phoenix.ExchangeAccounts{PerpAssetMap: orderTestKey(10), GlobalTraderIndex: []string{orderTestKey(13)}, ActiveTraderBuffer: []string{orderTestKey(14)}}
	for i := range s.Metadata.Markets {
		s.Metadata.Markets[i].TickSize = 100
		s.Metadata.Markets[i].BaseLotDecimals = 2
		if s.Metadata.Markets[i].Symbol == "BTC" {
			s.Metadata.Markets[i].BaseLotDecimals = 4
		}
		s.Metadata.Markets[i].MarketPubkey = orderTestKey(byte(20 + i))
		s.Metadata.Markets[i].SplinePubkey = orderTestKey(byte(30 + i))
	}
	s.Books = map[string]phoenix.LiveBook{"SOL": {Symbol: "SOL", Generation: s.Generation, BidPrice: 99.99, AskPrice: 100, BidSizeUSD: 10000, AskSizeUSD: 10000, ReceivedAt: time.Now().UTC().Add(-time.Duration(m.quoteAge.Load()))}}
	return s
}

type phoenixOrderTestBackend struct {
	*phoenixTestBackend
	sdk                          *helper.Client
	flight                       helper.FlightAccounts
	mu                           sync.Mutex
	afterBuild, afterNormalize   func()
	last                         helper.BuildOrderRequest
	denyFlight, denyRisk         atomic.Bool
	projectionCalls, flightCalls atomic.Int32
}

func (*phoenixOrderTestBackend) Address() string            { return orderTestKey(3) }
func (b *phoenixOrderTestBackend) BuilderAuthority() string { return b.flight.BuilderAuthority }
func (*phoenixOrderTestBackend) LatestBlockhash(context.Context) (rpc.Blockhash, error) {
	return rpc.Blockhash{Hash: orderTestKey(9), Slot: 100, LastValidBlockHeight: 150}, nil
}
func (*phoenixOrderTestBackend) BlockHeight(context.Context, uint64) (uint64, error) { return 100, nil }
func (b *phoenixOrderTestBackend) AccountAuthority(ctx context.Context, owner string, slot uint64) (delegation.Authority, error) {
	a, err := b.phoenixTestBackend.AccountAuthority(ctx, owner, slot)
	a.Slot = max(slot, 120)
	return a, err
}
func (b *phoenixOrderTestBackend) FlightProof(context.Context, uint64) (phoenixlive.FlightProof, error) {
	b.flightCalls.Add(1)
	if b.denyFlight.Load() {
		return phoenixlive.FlightProof{}, errors.New("Flight unavailable")
	}
	return phoenixlive.FlightProof{ProgramOwner: helper.FlightProgramID, BuilderAuthority: b.flight.BuilderAuthority, BuilderState: b.flight.BuilderState, BuilderTraderAccount: b.flight.BuilderTraderAccount, FeeBPS: 2, Slot: 120, ObservedAt: time.Now().UTC()}, nil
}
func (b *phoenixOrderTestBackend) NormalizeOrder(ctx context.Context, q helper.NormalizeRequest) (helper.NormalizedOrder, error) {
	r, err := b.sdk.NormalizeOrder(ctx, q)
	b.mu.Lock()
	callback := b.afterNormalize
	b.mu.Unlock()
	if callback != nil {
		callback()
	}
	return r, err
}
func (b *phoenixOrderTestBackend) BuildOrder(ctx context.Context, q helper.BuildOrderRequest) (helper.BuildOrderResult, error) {
	r, err := b.sdk.BuildOrder(ctx, q)
	b.mu.Lock()
	b.last = q
	callback := b.afterBuild
	b.mu.Unlock()
	if callback != nil {
		callback()
	}
	return r, err
}
func (b *phoenixOrderTestBackend) ComputeRisk(ctx context.Context, q helper.RiskRequest) (helper.RiskResult, error) {
	if q.Projection != nil {
		b.projectionCalls.Add(1)
	}
	if b.denyRisk.Load() {
		return helper.RiskResult{}, errors.New("risk unavailable")
	}
	return b.sdk.ComputeRisk(ctx, q)
}

func newPhoenixPrepareFixture(t *testing.T, configure ...func(*phoenixAccountFixture, *phoenixOrderTestBackend, *phoenixOrderTestMarkets)) (*phoenixAccountFixture, *phoenixOrderTestBackend, *phoenixOrderTestMarkets, *phoenixlive.LiveModule, *phoenixAccountFeed) {
	t.Helper()
	path := os.Getenv("ORBITAL_PHOENIX_HELPER_TEST_PATH")
	if path == "" {
		t.Skip("set ORBITAL_PHOENIX_HELPER_TEST_PATH to built phoenix-helper")
	}
	f := newPhoenixAccountFixture(t)
	sdk, err := helper.Start(context.Background(), path, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sdk.Close)
	flight, err := sdk.FlightAccounts(context.Background(), helper.FlightRequest{BuilderAuthority: orderTestKey(4)})
	if err != nil {
		t.Fatal(err)
	}
	b := &phoenixOrderTestBackend{phoenixTestBackend: f.backend, sdk: sdk, flight: flight}
	m := &phoenixOrderTestMarkets{base: f.markets}
	f.factory.config.Backend, f.factory.config.Markets = b, m
	f.factory.risk = phoenixrisk.NewSource(f.factory.config.RESTURL, f.factory.config.HTTPClient, m)
	for _, setup := range configure {
		setup(f, b, m)
	}
	lease, err := f.registry.Acquire("phoenix", phoenixTestOwner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lease.Release)
	feed := lease.Feed().(*phoenixAccountFeed)
	waitPhoenix(t, func() bool { return feed.state.Snapshot().Fresh(time.Now()) })
	module, err := phoenixlive.NewLiveModule(context.Background(), &phoenixOrderPreparer{deps: f.server.live, config: f.factory.config, backend: b})
	if err != nil {
		t.Fatal(err)
	}
	return f, b, m, module, feed
}

func phoenixPrepareParams() venue.LiveOrderParams {
	return venue.LiveOrderParams{Account: phoenixTestOwner, Signer: phoenixTestAgent, Symbol: "SOL", Side: domain.SideLong, Amount: .299, Price: 100, LimitPrice: 100.005, ClientOrderID: "orbital-l1open-123"}
}

func TestPhoenixPrepareUsesRealSDKAndBindsAllFourActions(t *testing.T) {
	_, b, _, module, feed := newPhoenixPrepareFixture(t)
	amount, err := module.NormalizeAmount("SOL", .299)
	if err != nil || amount != .29 {
		t.Fatalf("amount=%v err=%v", amount, err)
	}
	for _, action := range []string{"open", "close", "unwind", "emergency_close"} {
		params := phoenixPrepareParams()
		params.ClientOrderID = "orbital-" + action + "-123"
		var request *domain.SigningRequest
		if action == "open" {
			request, err = module.BuildOpen(params)
		} else {
			params.Price = 42 // Reductions must use current BBO, never entry/fill price.
			request, err = module.BuildReduce(venue.LiveReduceParams{LiveOrderParams: params, Action: venue.ReduceAction(action)})
		}
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		var metadata phoenixlive.SigningMetadata
		if err := json.Unmarshal(request.VenueMetadata, &metadata); err != nil {
			t.Fatal(err)
		}
		if request.Amount != .29 || request.ReduceOnly != (action != "open") || metadata.Normalized.NumBaseLots != 29 || metadata.MarketMetadataGeneration != 7 || metadata.AccountSlot != 100 || metadata.MinContextSlot != 120 {
			t.Fatalf("%s binding request=%+v metadata=%+v", action, request, metadata)
		}
		normal := action == "open" || action == "close"
		if (metadata.Built.Manifest.Routing == "flight") != normal {
			t.Fatal("wrong route", action)
		}
		if action != "open" && (request.Side != "sell" || request.Price != 99.99 || request.LimitPrice != 99.5) {
			t.Fatalf("wrong reduction bound: %+v", request)
		}
		if action == "open" && (request.LimitPrice != 100 || time.Until(request.ExpiresAt) > helper.ProjectionQuoteMaxAge) {
			t.Fatal("opening projection or tick limit lost")
		}
	}
	if b.projectionCalls.Load() != 1 || b.flightCalls.Load() != 2 || !supportedLiveVenue("phoenix") || feed.Snapshot().Phoenix.LiveReady {
		t.Fatal("preparation changed execution readiness or action dependencies")
	}
	if _, err := feed.SubmitSigned(context.Background(), domain.SignedAction{}, &domain.SigningRequest{}); !errors.Is(err, errPhoenixLiveDisabled) {
		t.Fatal("preparation enabled submission")
	}
}

func TestPhoenixNativeSafetyPreparationDoesNotRequireRiskOrFlight(t *testing.T) {
	_, b, _, module, _ := newPhoenixPrepareFixture(t)
	b.denyRisk.Store(true)
	b.denyFlight.Store(true)
	for _, action := range []venue.ReduceAction{venue.ReduceActionUnwind, venue.ReduceActionEmergencyClose} {
		if _, err := module.BuildReduce(venue.LiveReduceParams{LiveOrderParams: phoenixPrepareParams(), Action: action}); err != nil {
			t.Fatal(action, err)
		}
	}
	if b.projectionCalls.Load() != 0 || b.flightCalls.Load() != 0 {
		t.Fatal("native safety action depended on risk/Flight")
	}
	if _, err := module.BuildReduce(venue.LiveReduceParams{LiveOrderParams: phoenixPrepareParams(), Action: venue.ReduceActionClose}); err == nil {
		t.Fatal("normal close bypassed Flight readiness")
	}
	b.denyFlight.Store(false)
	if request, err := module.BuildOpen(phoenixPrepareParams()); err == nil || request != nil || b.projectionCalls.Load() != 1 {
		t.Fatal("open bypassed unavailable risk projection", err)
	}
}

func TestPhoenixPrepareRejectsChangedMarketAuthorityAuthorizationAndStaleQuote(t *testing.T) {
	for _, change := range []string{"market", "authority", "registration", "quote"} {
		t.Run(change, func(t *testing.T) {
			f, b, m, module, feed := newPhoenixPrepareFixture(t)
			b.mu.Lock()
			b.afterBuild = func() {
				switch change {
				case "market":
					m.sequence.Add(1)
				case "authority":
					feed.invalidateAuthority()
				case "registration":
					if err := f.store.DeleteAgentAuthorization(context.Background(), "phoenix", phoenixTestOwner, phoenixTestAgent); err != nil {
						t.Error(err)
					}
				case "quote":
					m.quoteAge.Store(int64(6 * time.Second))
				}
			}
			b.mu.Unlock()
			if _, err := module.BuildOpen(phoenixPrepareParams()); err == nil {
				t.Fatal("changed capture returned signable order")
			}
		})
	}
}

func TestNewLiveDepsWiresPhoenixPreparationModuleWithoutExecutionAdmission(t *testing.T) {
	f, b, m, _, _ := newPhoenixPrepareFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	deps := NewLiveDeps(ctx, slog.Default(), domain.NewSigningRequestStore(), f.store, nil, nil, nil, nil, PhoenixAccountConfig{Backend: b, Markets: m})
	module, err := deps.liveModule("phoenix")
	if err != nil || module.Name() != "phoenix" || module.Capabilities().LeverageUpdate != venue.LeverageUpdateNotRequired || !supportedLiveVenue("phoenix") {
		t.Fatalf("module=%+v err=%v", module, err)
	}
}
