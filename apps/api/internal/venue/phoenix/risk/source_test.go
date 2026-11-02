package risk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/account"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
)

type testStream struct {
	mu    sync.Mutex
	value phoenix.MarketStreamSnapshot
}

func (s *testStream) Snapshot() phoenix.MarketStreamSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.value
	v.Metadata.Markets = append([]phoenix.MarketMetadata(nil), v.Metadata.Markets...)
	v.Stats = make(map[string]phoenix.LiveMarketStats, len(s.value.Stats))
	for k, stat := range s.value.Stats {
		v.Stats[k] = stat
	}
	return v
}
func (s *testStream) update(f func(*phoenix.MarketStreamSnapshot)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.value)
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func streamAt(now time.Time) *testStream {
	return &testStream{value: phoenix.MarketStreamSnapshot{
		Connected: true, Synchronized: true, Generation: 3, Sequence: 42,
		Metadata: phoenix.ExchangeMetadata{Markets: []phoenix.MarketMetadata{
			{Symbol: "SOL", Status: "active", AmountIncrement: .01, PriceIncrement: .01, MaxLeverage: 25},
			{Symbol: "BTC", Status: "active", AmountIncrement: .0001, PriceIncrement: 1, MaxLeverage: 20},
		}},
		Stats: map[string]phoenix.LiveMarketStats{
			"SOL": {Symbol: "SOL", Generation: 3, MarkPrice: 100, IndexPrice: 90, SourceTime: now, ReceivedAt: now},
			"BTC": {Symbol: "BTC", Generation: 3, MarkPrice: 50000, IndexPrice: 49000, SourceTime: now, ReceivedAt: now},
		},
	}}
}

func acctAt(now time.Time) account.Snapshot {
	return account.Snapshot{Owner: "11111111111111111111111111111111", Connected: true, Synchronized: true,
		Generation: 1, Slot: 100, Sequence: 41, ObservedAt: now,
		Cross: account.CrossState{Collateral: "100000000", Positions: map[string]account.Position{}, Orders: map[string]map[string]account.Order{},
			SpotCollaterals: []account.SpotCollateral{{AssetIndex: 4294901760, Symbol: "SOL", Balance: "1000000000", Decimals: 9}, {AssetIndex: 4294836323, Symbol: "cbBTC", Balance: "1000000", Decimals: 8}}}}
}

func sourceFor(t *testing.T, now *time.Time, stream *testStream, handler func(http.ResponseWriter, *http.Request)) (*Source, *atomic.Int32) {
	t.Helper()
	parameters, collateral := fixture(t, "parameters"), fixture(t, "collateral")
	reads := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if handler != nil {
			handler(w, r)
			return
		}
		switch r.URL.Path {
		case "/v1/view/exchange":
			_, _ = w.Write(parameters)
		case "/v1/collateral/assets":
			_, _ = w.Write(collateral)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	source := NewSource(server.URL, server.Client(), stream)
	source.now = func() time.Time { return *now }
	return source, reads
}

func TestJoinCoversSpotPricingMarketsWithoutPerpPositions(t *testing.T) {
	now := time.Now().UTC()
	stream := streamAt(now)
	source, reads := sourceFor(t, &now, stream, nil)
	inputs, err := source.inputs(context.Background(), acctAt(now), stream.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs.Markets) != 2 || len(inputs.SpotParams) != 2 || inputs.MetadataGeneration == 0 || !inputs.ParamsObservedAt.Equal(now) {
		t.Fatalf("incomplete inputs: %+v", inputs)
	}
	if inputs.Markets[0].Symbol != "BTC" || inputs.SpotParams[0].Symbol != "cbBTC" || inputs.SpotParams[0].PricingMarketSymbol != "BTC" {
		t.Fatal("collateral must join by perp asset ID, not spot symbol")
	}
	if *inputs.Markets[1].MarkPriceUSD != 100 || *inputs.Markets[1].IndexPriceUSD != 90 || inputs.Markets[1].MarkPriceTicks != "" {
		t.Fatal("prices were replaced or converted outside SDK")
	}
	if inputs.Markets[1].RiskFactorsBPS != [3]uint16{5000, 2000, 1000} || inputs.Markets[1].LeverageTiers[0].UpperBoundBaseLots != "32164684" {
		t.Fatal("risk parameters were reduced to scanner max leverage")
	}
	if _, err := source.inputs(context.Background(), acctAt(now), stream.Snapshot()); err != nil || reads.Load() != 2 {
		t.Fatal("parameter cache did not share catalog reads", err, reads.Load())
	}
	inputs.Markets[0].LeverageTiers[0].MaxLeverage = 999
	if got, err := source.inputs(context.Background(), acctAt(now), stream.Snapshot()); err != nil || got.Markets[0].LeverageTiers[0].MaxLeverage != 20 {
		t.Fatal("result aliases parameter cache")
	}
}

func TestJoinIncludesOrderOnlyMarketsAndRejectsMissingExposure(t *testing.T) {
	now := time.Now().UTC()
	stream := streamAt(now)
	source, _ := sourceFor(t, &now, stream, nil)
	acct := acctAt(now)
	acct.Cross.SpotCollaterals = nil
	acct.Cross.Positions["SOL"] = account.Position{Symbol: "SOL"}
	acct.Cross.Orders["BTC"] = map[string]account.Order{"1": {}}
	inputs, err := source.inputs(context.Background(), acct, stream.Snapshot())
	if err != nil || len(inputs.Markets) != 2 {
		t.Fatal("omitted position or order-only market", err)
	}
	acct.Cross.Positions["UNKNOWN"] = account.Position{}
	if _, err := source.inputs(context.Background(), acct, stream.Snapshot()); err == nil {
		t.Fatal("omitted an unknown exposure")
	}
}

func TestCachedParametersNeverRenewMarketPrices(t *testing.T) {
	now := time.Now().UTC()
	stream := streamAt(now)
	source, reads := sourceFor(t, &now, stream, nil)
	if _, err := source.inputs(context.Background(), acctAt(now), stream.Snapshot()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(31 * time.Second)
	if _, err := source.inputs(context.Background(), acctAt(now), stream.Snapshot()); err == nil {
		t.Fatal("cache hit made old prices fresh")
	}
	stream.update(func(s *phoenix.MarketStreamSnapshot) {
		for symbol, value := range s.Stats {
			value.SourceTime, value.ReceivedAt = now, now
			s.Stats[symbol] = value
		}
	})
	if _, err := source.inputs(context.Background(), acctAt(now), stream.Snapshot()); err != nil || reads.Load() != 2 {
		t.Fatal("new stream prices required redundant REST reads", err)
	}
}

func TestJoinRejectsDisconnectedWrongGenerationAndBadCollateral(t *testing.T) {
	for name, mutate := range map[string]func(*phoenix.MarketStreamSnapshot, *account.Snapshot){
		"disconnect": func(s *phoenix.MarketStreamSnapshot, _ *account.Snapshot) { s.Connected = false },
		"resync":     func(s *phoenix.MarketStreamSnapshot, _ *account.Snapshot) { s.Synchronized = false },
		"old generation": func(s *phoenix.MarketStreamSnapshot, _ *account.Snapshot) {
			value := s.Stats["BTC"]
			value.Generation--
			s.Stats["BTC"] = value
		},
		"stale oracle": func(s *phoenix.MarketStreamSnapshot, _ *account.Snapshot) {
			value := s.Stats["BTC"]
			value.SourceTime = value.SourceTime.Add(-31 * time.Second)
			s.Stats["BTC"] = value
		},
		"future oracle": func(s *phoenix.MarketStreamSnapshot, _ *account.Snapshot) {
			value := s.Stats["SOL"]
			value.SourceTime = value.SourceTime.Add(time.Second)
			s.Stats["SOL"] = value
		},
		"missing pricing market": func(s *phoenix.MarketStreamSnapshot, _ *account.Snapshot) { delete(s.Stats, "BTC") },
		"bad index": func(s *phoenix.MarketStreamSnapshot, _ *account.Snapshot) {
			value := s.Stats["SOL"]
			value.IndexPrice = 0
			s.Stats["SOL"] = value
		},
		"precision mismatch": func(s *phoenix.MarketStreamSnapshot, _ *account.Snapshot) { s.Metadata.Markets[0].PriceIncrement = 1 },
		"missing spot config": func(_ *phoenix.MarketStreamSnapshot, a *account.Snapshot) {
			a.Cross.SpotCollaterals[0].AssetIndex = 123
		},
		"spot decimals mismatch": func(_ *phoenix.MarketStreamSnapshot, a *account.Snapshot) { a.Cross.SpotCollaterals[0].Decimals = 6 },
	} {
		t.Run(name, func(t *testing.T) {
			now := time.Now().UTC()
			stream := streamAt(now)
			acct := acctAt(now)
			stream.update(func(s *phoenix.MarketStreamSnapshot) { mutate(s, &acct) })
			source, _ := sourceFor(t, &now, stream, nil)
			if _, err := source.inputs(context.Background(), acct, stream.Snapshot()); err == nil {
				t.Fatal("accepted unavailable risk inputs")
			}
		})
	}
}

func TestParametersInvalidatedByExchangeSequenceAndReconnect(t *testing.T) {
	now := time.Now().UTC()
	stream := streamAt(now)
	source, reads := sourceFor(t, &now, stream, nil)
	first, err := source.inputs(context.Background(), acctAt(now), stream.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Second)
	stream.update(func(s *phoenix.MarketStreamSnapshot) { s.Sequence++ })
	second, err := source.inputs(context.Background(), acctAt(now), stream.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if first.MetadataGeneration == second.MetadataGeneration || reads.Load() != 4 {
		t.Fatal("exchange update reused old risk parameters")
	}
	now = now.Add(6 * time.Second)
	stream.update(func(s *phoenix.MarketStreamSnapshot) {
		s.Generation++
		for k, value := range s.Stats {
			value.Generation = s.Generation
			s.Stats[k] = value
		}
	})
	third, err := source.inputs(context.Background(), acctAt(now), stream.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if third.MetadataGeneration == second.MetadataGeneration || reads.Load() != 6 {
		t.Fatal("reconnect reused previous connection's risk parameters")
	}
}

func TestParameterReadRacingExchangeUpdateIsRejected(t *testing.T) {
	now := time.Now().UTC()
	stream := streamAt(now)
	parameters, collateral := fixture(t, "parameters"), fixture(t, "collateral")
	source, _ := sourceFor(t, &now, stream, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/view/exchange" {
			stream.update(func(s *phoenix.MarketStreamSnapshot) { s.Sequence++ })
			_, _ = w.Write(parameters)
		} else {
			_, _ = w.Write(collateral)
		}
	})
	if _, err := source.inputs(context.Background(), acctAt(now), stream.Snapshot()); err == nil {
		t.Fatal("joined catalog read across an exchange update")
	}
}

func TestParameterReadsSingleFlightWaiterCancellationAndRetryAfter(t *testing.T) {
	now := time.Now().UTC()
	stream := streamAt(now)
	parameters, collateral := fixture(t, "parameters"), fixture(t, "collateral")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	source, reads := sourceFor(t, &now, stream, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/view/exchange" {
			once.Do(func() { close(entered) })
			<-release
			_, _ = w.Write(parameters)
		} else {
			_, _ = w.Write(collateral)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := source.parameters(ctx, streamRevision(stream.Snapshot())); first <- err }()
	<-entered
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := source.parameters(context.Background(), streamRevision(stream.Snapshot())); err != nil {
				t.Error(err)
			}
		}()
	}
	close(release)
	wg.Wait()
	if reads.Load() != 2 {
		t.Fatal("single flight did not share reads", reads.Load())
	}

	backoff, count := sourceFor(t, &now, stream, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	if _, err := backoff.parameters(context.Background(), streamRevision(stream.Snapshot())); err == nil {
		t.Fatal("ignored 429")
	}
	now = now.Add(6 * time.Second)
	if _, err := backoff.parameters(context.Background(), streamRevision(stream.Snapshot())); err == nil || count.Load() != 1 {
		t.Fatal("ignored shared Retry-After")
	}
}

func TestMissingRiskFieldsAndMalformedCatalogFailClosed(t *testing.T) {
	var catalog struct {
		Markets []marketConfig `json:"markets"`
	}
	if err := json.Unmarshal(fixture(t, "parameters"), &catalog); err != nil {
		t.Fatal(err)
	}
	m := catalog.Markets[0]
	m.Risk.MaintenanceBPS = nil
	m.Risk.Maintenance = nil
	if _, err := normalizeMarket(m); err == nil {
		t.Fatal("missing factor treated as zero")
	}
	zero := uint16(0)
	m.Risk.MaintenanceBPS = &zero
	if _, err := normalizeMarket(m); err != nil {
		t.Fatal("explicit zero factor rejected", err)
	}
	m = catalog.Markets[0]
	m.Risk.MaintenanceBPS = nil
	if got, err := normalizeMarket(m); err != nil || got.RiskFactorsBPS[0] != 5000 {
		t.Fatal("legacy percentage normalization failed", err)
	}
	now := time.Now().UTC()
	stream := streamAt(now)
	source, _ := sourceFor(t, &now, stream, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat(" ", maxResponseBytes+1))
	})
	if _, err := source.parameters(context.Background(), streamRevision(stream.Snapshot())); err == nil {
		t.Fatal("accepted oversized catalog")
	}
}

func TestRealHelperConsumesJoinedSpotInputs(t *testing.T) {
	path := os.Getenv("ORBITAL_PHOENIX_HELPER_TEST_PATH")
	if path == "" {
		t.Skip("set helper path for real SDK integration")
	}
	now := time.Now().UTC()
	stream := streamAt(now)
	source, _ := sourceFor(t, &now, stream, nil)
	acct := acctAt(now)
	inputs, err := source.inputs(context.Background(), acct, stream.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := helper.Start(ctx, path, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	q := helper.RiskRequest{OwnerAuthority: acct.Owner, AccountGeneration: acct.Generation, AccountSlot: acct.Slot, AccountSequence: acct.Sequence,
		MetadataGeneration: inputs.MetadataGeneration, AsOfUnixMS: time.Now().UnixMilli(), AccountObservedUnixMS: acct.ObservedAt.UnixMilli(), ParamsObservedUnixMS: inputs.ParamsObservedAt.UnixMilli(),
		CollateralQuoteLots: acct.Cross.Collateral, Positions: []helper.RiskPosition{}, Orders: []helper.RiskOrder{}, SpotBalances: []helper.RiskSpotBalance{}, Markets: inputs.Markets, SpotParams: inputs.SpotParams}
	for _, spot := range acct.Cross.SpotCollaterals {
		q.SpotBalances = append(q.SpotBalances, helper.RiskSpotBalance{AssetIndex: spot.AssetIndex, Symbol: spot.Symbol, Balance: spot.Balance, Decimals: spot.Decimals})
	}
	result, err := client.ComputeRisk(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	// $100 USDC + $90 SOL + $490 cbBTC; both spot assets have a 20% haircut.
	if result.EquityQuoteLots != "680000000" || result.EffectiveCollateralQuoteLots != "564000000" || result.InitialMarginQuoteLots != "0" {
		t.Fatalf("wrong joined SDK view: %+v", result)
	}
}

func TestPublicRiskParameterSchema(t *testing.T) {
	if os.Getenv("ORBITAL_PHOENIX_PUBLIC_RISK_SMOKE") != "1" {
		t.Skip("opt in to two public read-only catalog requests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	source := NewSource("", nil, nil)
	p, err := source.parameters(ctx, revision{1, 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range []string{"SOL", "BTC"} {
		if _, err := normalizeMarket(p.markets[symbol]); err != nil {
			t.Fatalf("%s: %v", symbol, err)
		}
	}
	for _, asset := range p.assets {
		if asset.Spot != nil && asset.Spot.Active != nil && *asset.Spot.Active && asset.Spot.PerpAssetIndex != nil {
			pricing, ok := p.symbolsByID[*asset.Spot.PerpAssetIndex]
			if !ok {
				t.Fatal("unknown spot pricing market")
			}
			if _, err := normalizeSpot(asset, pricing); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestCollateralCapRetainsJsSafeU64Precision(t *testing.T) {
	for _, raw := range []string{`9007199254740993`, `"9007199254740993"`} {
		var value wireUint64
		if err := json.Unmarshal([]byte(raw), &value); err != nil || value != "9007199254740993" {
			t.Fatal("lost collateral cap precision", err, value)
		}
	}
	for _, raw := range []string{`-1`, `null`, `1.5`, `1e4`} {
		var value wireUint64
		if err := json.Unmarshal([]byte(raw), &value); err == nil {
			t.Fatal("accepted invalid unsigned collateral cap", raw)
		}
	}
}
