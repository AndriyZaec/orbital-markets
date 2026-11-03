package live

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/relayer"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/rpc"
	"github.com/mr-tron/base58"
)

const owner = "11111111111111111111111111111111"
const agent = "So11111111111111111111111111111111111111112"
const payer = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
const flightBuilder = "Vote111111111111111111111111111111111111111"

type fixtureBackend struct {
	onBuild func()
	corrupt bool
	last    helper.BuildOrderRequest
}

func (f *fixtureBackend) NormalizeOrder(_ context.Context, q helper.NormalizeRequest) (helper.NormalizedOrder, error) {
	price, ticks := "100", uint64(10000)
	if q.Side == helper.SideAsk {
		price, ticks = "100.01", 10001
	}
	return helper.NormalizedOrder{Symbol: q.Symbol, Side: q.Side, Amount: "0.29", LimitPrice: price, NumBaseLots: 29, PriceLimitTicks: ticks, BaseLotDecimals: q.BaseLotDecimals, TickSize: q.TickSize, MetadataGeneration: q.MetadataGeneration}, nil
}
func (f *fixtureBackend) BuildOrder(_ context.Context, q helper.BuildOrderRequest) (helper.BuildOrderResult, error) {
	f.last = q
	message := []byte("offline request binding fixture")
	hash := sha256.Sum256(message)
	r := helper.BuildOrderResult{MessageBase64: base64.StdEncoding.EncodeToString(message), MessageSHA256: hex.EncodeToString(hash[:]), RecentBlockhash: q.RecentBlockhash, LastValidBlockHeight: q.LastValidBlockHeight, MetadataGeneration: q.MetadataGeneration, TraderAccount: owner,
		RequiredSigners: []helper.RequiredSigner{{Index: 0, Pubkey: q.FeePayer, Role: "fee_payer"}, {Index: 1, Pubkey: q.PositionAuthority, Role: "position_authority"}},
		Manifest: helper.OrderManifest{Format: "solana_legacy_message", PhoenixProgramID: helper.PhoenixProgramID, Routing: "native", IntentAction: q.Action, OwnerAuthority: q.OwnerAuthority, PositionAuthority: q.PositionAuthority, FeePayer: q.FeePayer, TraderAccount: owner, RequestedSymbol: q.Symbol, Orderbook: q.Accounts.Orderbook, MetadataGeneration: q.MetadataGeneration, Side: q.Side, NumBaseLots: q.NumBaseLots, PriceLimitTicks: q.PriceLimitTicks, ReduceOnly: q.ReduceOnly, ClientOrderID: q.ClientOrderID, LastValidSlot: q.LastValidSlot,
			AccountKeys: []helper.AccountManifest{{Pubkey: q.FeePayer}}, Instructions: []helper.InstructionManifest{{ProgramID: helper.PhoenixProgramID}}}}
	if normal(q.Action) {
		r.Manifest.Routing = "flight"
		r.Manifest.Flight = &helper.FlightManifest{FlightProgramID: helper.FlightProgramID, BuilderAuthority: q.BuilderAuthority, BuilderTraderAccount: agent, BuilderState: payer, ExpectedRegisteredFeeBPS: 2}
	}
	if f.corrupt {
		r.MessageSHA256 = "bad"
	}
	if f.onBuild != nil {
		f.onBuild()
	}
	return r, nil
}

func captureFixture() (Intent, Capture) {
	now := time.Now().UTC()
	q := Intent{ID: "open-1", ClientOrderID: "18446744073709551615", PositionID: "position-1", Leg: 1, Owner: owner, Agent: agent, Symbol: "SOL", Action: helper.ActionOpen, Side: "buy", Amount: .299, ReferencePrice: 100, LimitPrice: 100.005, QuoteObservedAt: now}
	c := Capture{Symbol: "SOL", MetadataGeneration: 7, StreamGeneration: 3, ExchangeSequence: 42, MetadataObservedAt: now, BaseLotDecimals: 2, TickSize: "100",
		Accounts:          helper.MarketAccounts{PerpAssetMap: payer, Orderbook: agent, SplineCollection: flightBuilder, GlobalTraderIndex: []string{owner}, ActiveTraderBuffer: []string{owner}},
		AccountGeneration: 1, AccountSlot: 100, AccountSequence: 41, AccountObservedAt: now, Authority: AuthorityProof{Owner: owner, Agent: agent, RegisteredAgent: agent, ObservedAt: now, Slot: 120, CanIncreaseRisk: true, CanReduceRisk: true},
		Blockhash: rpc.Blockhash{Hash: owner, Slot: 100, LastValidBlockHeight: 150}, BlockHeight: 100, BlockhashObservedAt: now,
		Flight: &FlightProof{ProgramOwner: helper.FlightProgramID, BuilderAuthority: flightBuilder, BuilderState: payer, BuilderTraderAccount: agent, FeeBPS: 2, ObservedAt: now, Slot: 120},
		Projection: &helper.RiskResult{OwnerAuthority: owner, AccountGeneration: 1, AccountSlot: 100, AccountSequence: 41, MetadataGeneration: 7, AsOfUnixMS: now.UnixMilli(), ValidUntilUnixMS: now.Add(4 * time.Second).UnixMilli(), QuoteDecimals: 6, RiskTier: "Safe",
			Projection: &helper.ProjectionResult{IntentID: q.ID, Symbol: "SOL", Side: "bid", NumBaseLots: "29", PriceLimitTicks: "10000", FlightFeeBPS: 2, Model: "rise_full_fill_at_limit", FeePolicy: "undiscounted_taker_ceiling", ProjectedRiskTier: "Safe", MarginSufficient: true}}}
	return q, c
}

func TestPrepareBindsExactMessageSummaryExpiryAndOneShotStore(t *testing.T) {
	q, c := captureFixture()
	backend := &fixtureBackend{}
	builder, err := NewBuilder(backend, payer, flightBuilder, func(context.Context, Intent, Capture) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	r, err := builder.Prepare(context.Background(), q, c)
	if err != nil {
		t.Fatal(err)
	}
	if r.Account != owner || r.Signer != agent || r.Amount != .29 || r.LimitPrice != 100 || r.ReduceOnly || r.ClientOrderID != q.ClientOrderID || r.ExpiresAt.UnixMilli() != c.Projection.ValidUntilUnixMS {
		t.Fatalf("wrong signing summary: %+v", r)
	}
	var payload OrderPayload
	var metadata SigningMetadata
	if err := json.Unmarshal(r.UnsignedPayload, &payload); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(r.VenueMetadata, &metadata); err != nil {
		t.Fatal(err)
	}
	if payload.ClientOrderID != "18446744073709551615" || payload.NumBaseLots != "29" || payload.PriceLimitTicks != "10000" || payload.MessageBase64 != metadata.Built.MessageBase64 || metadata.MinContextSlot != 120 || backend.last.LastValidSlot != 164 {
		t.Fatal("native order or context binding was lost")
	}
	store := domain.NewSigningRequestStore()
	store.Store(r)
	wrong := domain.SignedAction{RequestID: r.ID, ClientOrderID: r.ClientOrderID, Venue: "phoenix", SignerAddress: owner, Signature: "fixture"}
	if _, err := store.ValidateAndConsume(wrong); err == nil {
		t.Fatal("owner accepted instead of delegated signer")
	}
	wrong.SignerAddress = agent
	if _, err := store.ValidateAndConsume(wrong); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ValidateAndConsume(wrong); err == nil {
		t.Fatal("request was consumed twice")
	}
}

func TestReductionsUseFlightOnlyForNormalCloseAndKeepSafetyIndependent(t *testing.T) {
	for _, action := range []helper.Action{helper.ActionClose, helper.ActionUnwind, helper.ActionEmergencyClose} {
		t.Run(string(action), func(t *testing.T) {
			q, c := captureFixture()
			q.Action = action
			q.Side = "sell"
			c.Projection = nil
			if action != helper.ActionClose {
				c.Flight = nil
			}
			backend := &fixtureBackend{}
			builder, _ := NewBuilder(backend, payer, flightBuilder, func(context.Context, Intent, Capture) error { return nil })
			r, err := builder.Prepare(context.Background(), q, c)
			if err != nil {
				t.Fatal(err)
			}
			if !r.ReduceOnly || r.LimitPrice != 100.01 || (backend.last.BuilderAuthority != "") != (action == helper.ActionClose) || backend.last.NumBaseLots != 29 {
				t.Fatal("wrong reduction route or bounds")
			}
			if r.ExpiresAt.Sub(r.CreatedAt) > 30*time.Second {
				t.Fatal("unbounded recent-blockhash request")
			}
		})
	}
}

func TestPrepareRejectsMismatchedOrExpiredEvidence(t *testing.T) {
	for name, mutate := range map[string]func(*Intent, *Capture){
		"market": func(_ *Intent, c *Capture) { c.Symbol = "BTC" }, "projection qty": func(_ *Intent, c *Capture) { c.Projection.Projection.NumBaseLots = "30" }, "projection price": func(_ *Intent, c *Capture) { c.Projection.Projection.PriceLimitTicks = "10001" },
		"projection intent": func(_ *Intent, c *Capture) { c.Projection.Projection.IntentID = "other" }, "account generation": func(_ *Intent, c *Capture) { c.Projection.AccountGeneration++ }, "metadata": func(_ *Intent, c *Capture) { c.Projection.MetadataGeneration++ },
		"expired projection": func(_ *Intent, c *Capture) { c.Projection.ValidUntilUnixMS = time.Now().Add(-time.Second).UnixMilli() }, "extended projection": func(_ *Intent, c *Capture) { c.Projection.ValidUntilUnixMS += 60000 },
		"expired hash": func(_ *Intent, c *Capture) { c.BlockHeight = c.Blockhash.LastValidBlockHeight }, "stale hash": func(_ *Intent, c *Capture) { c.BlockhashObservedAt = time.Now().Add(-31 * time.Second) },
		"stale quote": func(q *Intent, _ *Capture) { q.QuoteObservedAt = time.Now().Add(-6 * time.Second) }, "wrong delegate": func(_ *Intent, c *Capture) { c.Authority.RegisteredAgent = owner }, "old authority slot": func(_ *Intent, c *Capture) { c.Authority.Slot = 99 },
		"wrong Flight fee": func(_ *Intent, c *Capture) { c.Flight.FeeBPS = 3 }, "missing Flight": func(_ *Intent, c *Capture) { c.Flight = nil }, "stale Flight": func(_ *Intent, c *Capture) { c.Flight.ObservedAt = time.Now().Add(-31 * time.Second) },
		"Flight account mismatch": func(_ *Intent, c *Capture) { c.Flight.BuilderState = owner },
		"projection blocked":      func(_ *Intent, c *Capture) { c.Projection.Projection.MarginSufficient = false },
		"capability":              func(_ *Intent, c *Capture) { c.Authority.CanIncreaseRisk = false },
	} {
		t.Run(name, func(t *testing.T) {
			q, c := captureFixture()
			mutate(&q, &c)
			builder, _ := NewBuilder(&fixtureBackend{}, payer, flightBuilder, func(context.Context, Intent, Capture) error { return nil })
			if r, err := builder.Prepare(context.Background(), q, c); err == nil || r != nil {
				t.Fatal("returned a signable request for invalid evidence")
			}
		})
	}
}

func TestPrepareRechecksCaptureAfterSDKAndRejectsCorruptedMessage(t *testing.T) {
	q, c := captureFixture()
	changed := false
	backend := &fixtureBackend{onBuild: func() { changed = true }}
	builder, _ := NewBuilder(backend, payer, flightBuilder, func(context.Context, Intent, Capture) error {
		if changed {
			return errors.New("capture changed")
		}
		return nil
	})
	if r, err := builder.Prepare(context.Background(), q, c); err == nil || r != nil {
		t.Fatal("SDK build crossed a context change")
	}
	backend = &fixtureBackend{corrupt: true}
	builder, _ = NewBuilder(backend, payer, flightBuilder, func(context.Context, Intent, Capture) error { return nil })
	if r, err := builder.Prepare(context.Background(), q, c); err == nil || r != nil {
		t.Fatal("corrupt message digest accepted")
	}
	backend = &fixtureBackend{}
	builder, _ = NewBuilder(backend, payer, flightBuilder, func(context.Context, Intent, Capture) error { return nil })
	backend.onBuild = func() { builder.now = func() time.Time { return q.QuoteObservedAt.Add(6 * time.Second) } }
	if r, err := builder.Prepare(context.Background(), q, c); err == nil || r != nil {
		t.Fatal("request became signable after quote/projection expired during SDK work")
	}
}

func TestPinnedHelperRequestCanBeAgentSignedAndRelayerVerifiedUnchanged(t *testing.T) {
	path := os.Getenv("ORBITAL_PHOENIX_HELPER_TEST_PATH")
	if path == "" {
		t.Skip("set helper path for actual SDK messages")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := helper.Start(ctx, path, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	agentKey := ed25519.NewKeyFromSeed(make([]byte, 32))
	seed := make([]byte, 32)
	seed[0] = 1
	payerKey := ed25519.NewKeyFromSeed(seed)
	signer, err := relayer.NewSigner(base58.Encode(payerKey))
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []helper.Action{helper.ActionOpen, helper.ActionClose, helper.ActionUnwind, helper.ActionEmergencyClose} {
		q, c := captureFixture()
		q.Action = action
		q.Agent = base58.Encode(agentKey[32:])
		c.Authority.Agent = q.Agent
		c.Authority.RegisteredAgent = q.Agent
		probe, err := client.BuildOrder(ctx, helper.BuildOrderRequest{Action: helper.ActionClose, OwnerAuthority: q.Owner, PositionAuthority: q.Agent, FeePayer: signer.Address(), BuilderAuthority: flightBuilder, Symbol: q.Symbol, Side: helper.SideBid, NumBaseLots: 29, PriceLimitTicks: 10000, ReduceOnly: true, ClientOrderID: 42, LastValidSlot: 164, RecentBlockhash: owner, LastValidBlockHeight: 150, MetadataGeneration: 7, Accounts: c.Accounts})
		if err != nil {
			t.Fatal(err)
		}
		c.Flight.BuilderState = probe.Manifest.Flight.BuilderState
		c.Flight.BuilderTraderAccount = probe.Manifest.Flight.BuilderTraderAccount
		if action != helper.ActionOpen {
			c.Projection = nil
		}
		builder, _ := NewBuilder(client, signer.Address(), flightBuilder, func(context.Context, Intent, Capture) error { return nil })
		r, err := builder.Prepare(ctx, q, c)
		if err != nil {
			t.Fatal(err)
		}
		var metadata SigningMetadata
		if err := json.Unmarshal(r.VenueMetadata, &metadata); err != nil {
			t.Fatal(err)
		}
		message, _ := base64.StdEncoding.DecodeString(metadata.Built.MessageBase64)
		packet, err := signer.CoSign(metadata.Built, q.Agent, base58.Encode(ed25519.Sign(agentKey, message)))
		if err != nil {
			t.Fatal(err)
		}
		if string(packet[129:]) != string(message) {
			t.Fatal("co-signing changed the prepared message")
		}
		if (metadata.Built.Manifest.Routing == "flight") != normal(action) {
			t.Fatal("wrong real SDK routing")
		}
	}
}
