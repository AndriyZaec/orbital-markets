// Package live prepares exact Phoenix signing requests. It does not register a
// LiveModule, store/submit a signed action, or confirm execution.
package live

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/rpc"
	"github.com/mr-tron/base58"
)

const requestTTL = 30 * time.Second
const slotBudget = uint64(64)

type Backend interface {
	NormalizeOrder(context.Context, helper.NormalizeRequest) (helper.NormalizedOrder, error)
	BuildOrder(context.Context, helper.BuildOrderRequest) (helper.BuildOrderResult, error)
}

type Intent struct {
	ID, ClientOrderID, PositionID      string
	Leg                                int
	Owner, Agent, Symbol               string
	Action                             helper.Action
	Side                               string // buy or sell, matching domain.SigningRequest
	Amount, ReferencePrice, LimitPrice float64
	QuoteObservedAt                    time.Time
}

type AuthorityProof struct {
	Owner, Agent, RegisteredAgent  string
	ObservedAt                     time.Time
	Slot                           uint64
	CanIncreaseRisk, CanReduceRisk bool
}

// FlightProof must originate from verified chain state, not browser input or the
// diagnostic order manifest. Its producer belongs to live Flight readiness.
type FlightProof struct {
	ProgramOwner, BuilderAuthority, BuilderState, BuilderTraderAccount string
	FeeBPS                                                             uint64
	ObservedAt                                                         time.Time
	Slot                                                               uint64
}

// Capture is immutable, server-owned evidence assembled by the orchestrator.
// MetadataGeneration is the capture's risk/order generation, not a timestamp.
type Capture struct {
	Symbol                                                 string
	MetadataGeneration, StreamGeneration, ExchangeSequence uint64
	MetadataObservedAt                                     time.Time
	BaseLotDecimals                                        int8
	TickSize                                               string
	Accounts                                               helper.MarketAccounts
	AccountGeneration, AccountSlot, AccountSequence        uint64
	AccountObservedAt                                      time.Time
	Authority                                              AuthorityProof
	Blockhash                                              rpc.Blockhash
	BlockHeight                                            uint64
	BlockhashObservedAt                                    time.Time
	Flight                                                 *FlightProof
	Projection                                             *helper.RiskResult
}

// CheckCapture must recheck current account/metadata/delegation revisions at
// each call. For safety actions it must not depend on discovery/open projection
// or Flight. No caller-provided browser capture is accepted by an HTTP route.
type CheckCapture func(context.Context, Intent, Capture) error

type Builder struct {
	backend                    Backend
	feePayer, builderAuthority string
	check                      CheckCapture
	now                        func() time.Time
}

func NewBuilder(backend Backend, feePayer, builderAuthority string, check CheckCapture) (*Builder, error) {
	if backend == nil || !key(feePayer) || check == nil {
		return nil, errors.New("Phoenix signing builder dependencies unavailable")
	}
	if builderAuthority != "" && !key(builderAuthority) {
		return nil, errors.New("invalid Flight builder authority")
	}
	return &Builder{backend: backend, feePayer: feePayer, builderAuthority: builderAuthority, check: check, now: time.Now}, nil
}

func (b *Builder) Normalize(ctx context.Context, q Intent, c Capture) (helper.NormalizedOrder, error) {
	if err := b.validate(ctx, q, c, false); err != nil {
		return helper.NormalizedOrder{}, err
	}
	request := normalization(q, c)
	r, err := b.backend.NormalizeOrder(ctx, request)
	if err != nil {
		return helper.NormalizedOrder{}, err
	}
	if err := helper.ValidateNormalizedOrder(request, r); err != nil {
		return helper.NormalizedOrder{}, err
	}
	if err := b.validate(ctx, q, c, false); err != nil {
		return helper.NormalizedOrder{}, err
	}
	return r, nil
}

func normalization(q Intent, c Capture) helper.NormalizeRequest {
	side := helper.SideBid
	if q.Side == "sell" {
		side = helper.SideAsk
	}
	return helper.NormalizeRequest{Symbol: q.Symbol, Side: side, Amount: strconv.FormatFloat(q.Amount, 'f', -1, 64), LimitPrice: strconv.FormatFloat(q.LimitPrice, 'f', -1, 64), BaseLotDecimals: c.BaseLotDecimals, TickSize: c.TickSize, MetadataGeneration: c.MetadataGeneration}
}

type OrderPayload struct {
	Format            string `json:"format"`
	MessageBase64     string `json:"message_base64"`
	MessageSHA256     string `json:"message_sha256"`
	OwnerAuthority    string `json:"owner_authority"`
	PositionAuthority string `json:"position_authority"`
	FeePayer          string `json:"fee_payer"`
	TraderAccount     string `json:"trader_account"`
	ClientOrderID     string `json:"client_order_id"`
	NumBaseLots       string `json:"num_base_lots"`
	PriceLimitTicks   string `json:"price_limit_ticks"`
	LastValidSlot     string `json:"last_valid_slot"`
}

type SigningMetadata struct {
	ProtocolVersion          uint32                  `json:"protocol_version"`
	Built                    helper.BuildOrderResult `json:"built"`
	Normalized               helper.NormalizedOrder  `json:"normalized"`
	StreamGeneration         uint64                  `json:"stream_generation"`
	ExchangeSequence         uint64                  `json:"exchange_sequence"`
	AccountGeneration        uint64                  `json:"account_generation"`
	AccountSlot              uint64                  `json:"account_slot"`
	AccountSequence          uint64                  `json:"account_sequence"`
	MinContextSlot           uint64                  `json:"min_context_slot"`
	BlockHeightAtPreparation uint64                  `json:"block_height_at_preparation"`
}

func (b *Builder) Prepare(ctx context.Context, q Intent, c Capture) (*domain.SigningRequest, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	normalized, err := b.Normalize(ctx, q, c)
	if err != nil {
		return nil, err
	}
	if err := b.validate(ctx, q, c, true); err != nil {
		return nil, err
	}
	if q.Action == helper.ActionOpen {
		p := c.Projection.Projection
		if p.Symbol != q.Symbol || p.Side != string(normalized.Side) || p.NumBaseLots != strconv.FormatUint(normalized.NumBaseLots, 10) || p.PriceLimitTicks != strconv.FormatUint(normalized.PriceLimitTicks, 10) {
			return nil, errors.New("Phoenix projection differs from normalized order")
		}
	}
	cid, _ := strconv.ParseUint(q.ClientOrderID, 10, 64)
	request := helper.BuildOrderRequest{Action: q.Action, OwnerAuthority: q.Owner, PositionAuthority: q.Agent, FeePayer: b.feePayer,
		Symbol: q.Symbol, Side: normalized.Side, NumBaseLots: normalized.NumBaseLots, PriceLimitTicks: normalized.PriceLimitTicks, ReduceOnly: q.Action != helper.ActionOpen,
		ClientOrderID: cid, LastValidSlot: c.Blockhash.Slot + slotBudget, RecentBlockhash: c.Blockhash.Hash, LastValidBlockHeight: c.Blockhash.LastValidBlockHeight, MetadataGeneration: c.MetadataGeneration, Accounts: c.Accounts}
	if normal(q.Action) {
		request.BuilderAuthority = b.builderAuthority
	}
	built, err := b.backend.BuildOrder(ctx, request)
	if err != nil {
		return nil, err
	}
	if err := helper.ValidateBuildOrderResult(request, built); err != nil {
		return nil, err
	}
	if normal(q.Action) && (built.Manifest.Flight.BuilderState != c.Flight.BuilderState || built.Manifest.Flight.BuilderTraderAccount != c.Flight.BuilderTraderAccount) {
		return nil, errors.New("Flight account proof does not match SDK-derived accounts")
	}
	if err := b.validate(ctx, q, c, true); err != nil {
		return nil, err
	}
	now := b.now().UTC()
	expires := now.Add(requestTTL)
	for _, at := range []time.Time{c.AccountObservedAt.Add(requestTTL), c.Authority.ObservedAt.Add(requestTTL), c.BlockhashObservedAt.Add(requestTTL), c.MetadataObservedAt.Add(helper.RiskParamsMaxAge)} {
		if at.Before(expires) {
			expires = at
		}
	}
	if normal(q.Action) && c.Flight.ObservedAt.Add(requestTTL).Before(expires) {
		expires = c.Flight.ObservedAt.Add(requestTTL)
	}
	if q.Action == helper.ActionOpen {
		if time.UnixMilli(c.Projection.ValidUntilUnixMS).Before(expires) {
			expires = time.UnixMilli(c.Projection.ValidUntilUnixMS)
		}
		if q.QuoteObservedAt.Add(helper.ProjectionQuoteMaxAge).Before(expires) {
			expires = q.QuoteObservedAt.Add(helper.ProjectionQuoteMaxAge)
		}
	}
	if !now.Before(expires) {
		return nil, errors.New("Phoenix signing request expired during preparation")
	}
	payload, err := json.Marshal(OrderPayload{Format: "solana_legacy_message", MessageBase64: built.MessageBase64, MessageSHA256: built.MessageSHA256,
		OwnerAuthority: q.Owner, PositionAuthority: q.Agent, FeePayer: b.feePayer, TraderAccount: built.TraderAccount, ClientOrderID: q.ClientOrderID,
		NumBaseLots: strconv.FormatUint(normalized.NumBaseLots, 10), PriceLimitTicks: strconv.FormatUint(normalized.PriceLimitTicks, 10), LastValidSlot: strconv.FormatUint(request.LastValidSlot, 10)})
	if err != nil {
		return nil, err
	}
	metadata, err := json.Marshal(SigningMetadata{ProtocolVersion: helper.ProtocolVersion, Built: built, Normalized: normalized,
		StreamGeneration: c.StreamGeneration, ExchangeSequence: c.ExchangeSequence, AccountGeneration: c.AccountGeneration, AccountSlot: c.AccountSlot, AccountSequence: c.AccountSequence,
		MinContextSlot: max(c.Blockhash.Slot, c.Authority.Slot), BlockHeightAtPreparation: c.BlockHeight})
	if err != nil {
		return nil, err
	}
	amount, err := strconv.ParseFloat(normalized.Amount, 64)
	if err != nil || !positive(amount) {
		return nil, errors.New("invalid normalized amount summary")
	}
	limit, err := strconv.ParseFloat(normalized.LimitPrice, 64)
	if err != nil || !positive(limit) {
		return nil, errors.New("invalid normalized price summary")
	}
	return &domain.SigningRequest{ID: q.ID, ClientOrderID: q.ClientOrderID, PositionID: q.PositionID, Leg: q.Leg, Venue: "phoenix", Action: string(q.Action), Account: q.Owner, Signer: q.Agent,
		Symbol: q.Symbol, Side: q.Side, Amount: amount, Price: q.ReferencePrice, LimitPrice: limit, ReduceOnly: request.ReduceOnly, UnsignedPayload: payload, VenueMetadata: metadata, CreatedAt: now, ExpiresAt: expires}, nil
}

func (b *Builder) validate(ctx context.Context, q Intent, c Capture, requireProjection bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now := b.now()
	cid, err := strconv.ParseUint(q.ClientOrderID, 10, 64)
	if q.ID == "" || len(q.ID) > 128 || err != nil || cid == 0 || strconv.FormatUint(cid, 10) != q.ClientOrderID || q.Leg < 0 || q.Leg > 2 || !key(q.Owner) || !key(q.Agent) || q.Owner == q.Agent || q.Agent == b.feePayer || q.Owner == b.feePayer || q.Symbol == "" || len(q.Symbol) > 32 || (q.Side != "buy" && q.Side != "sell") || !positive(q.Amount) || !positive(q.ReferencePrice) || !positive(q.LimitPrice) {
		return errors.New("invalid Phoenix signing intent")
	}
	switch q.Action {
	case helper.ActionOpen, helper.ActionClose, helper.ActionUnwind, helper.ActionEmergencyClose:
	default:
		return errors.New("unsupported Phoenix signing action")
	}
	if !fresh(q.QuoteObservedAt, now, helper.ProjectionQuoteMaxAge) || !fresh(c.AccountObservedAt, now, requestTTL) || !fresh(c.Authority.ObservedAt, now, requestTTL) || !fresh(c.BlockhashObservedAt, now, requestTTL) || !fresh(c.MetadataObservedAt, now, helper.RiskParamsMaxAge) {
		return errors.New("Phoenix signing capture is missing, stale or future-dated")
	}
	if c.Symbol != q.Symbol || c.MetadataGeneration == 0 || c.StreamGeneration == 0 || c.AccountGeneration == 0 || c.Authority.Owner != q.Owner || c.Authority.Agent != q.Agent || c.Authority.RegisteredAgent != q.Agent || c.Authority.Slot < max(c.AccountSlot, c.Blockhash.Slot) || !key(c.Blockhash.Hash) || c.Blockhash.Slot == 0 || c.Blockhash.Slot > math.MaxUint64-slotBudget || c.BlockHeight == 0 || c.BlockHeight >= c.Blockhash.LastValidBlockHeight {
		return errors.New("invalid Phoenix authority, metadata or blockhash evidence")
	}
	if (q.Action == helper.ActionOpen && !c.Authority.CanIncreaseRisk) || (q.Action != helper.ActionOpen && !c.Authority.CanReduceRisk) {
		return errors.New("Phoenix account capability does not allow action")
	}
	if normal(q.Action) {
		f := c.Flight
		if b.builderAuthority == "" || f == nil || f.ProgramOwner != helper.FlightProgramID || f.BuilderAuthority != b.builderAuthority || f.FeeBPS != 2 || !key(f.BuilderState) || !key(f.BuilderTraderAccount) || f.Slot < c.Blockhash.Slot || !fresh(f.ObservedAt, now, requestTTL) {
			return errors.New("fresh verified Flight 2 bps registration required")
		}
	}
	if requireProjection && q.Action == helper.ActionOpen {
		r := c.Projection
		if r == nil || r.Projection == nil || !r.Projection.MarginSufficient || r.Projection.Reason != "" || r.OwnerAuthority != q.Owner || r.AccountGeneration != c.AccountGeneration || r.AccountSlot != c.AccountSlot || r.AccountSequence != c.AccountSequence || r.MetadataGeneration != c.MetadataGeneration || r.TraderPDAIndex != 0 || r.SubaccountIndex != 0 || r.QuoteDecimals != 6 || !fresh(time.UnixMilli(r.AsOfUnixMS), now, helper.ProjectionQuoteMaxAge) || now.UnixMilli() >= r.ValidUntilUnixMS || r.ValidUntilUnixMS > r.AsOfUnixMS+helper.ProjectionQuoteMaxAge.Milliseconds() || r.Projection.IntentID != q.ID || r.Projection.FlightFeeBPS != 2 || r.Projection.Model != "rise_full_fill_at_limit" || r.Projection.FeePolicy != "undiscounted_taker_ceiling" || r.RiskTier != "Safe" || r.Projection.ProjectedRiskTier != "Safe" {
			return errors.New("fresh matching Phoenix opening projection required")
		}
	}
	return b.check(ctx, q, c)
}

func normal(action helper.Action) bool {
	return action == helper.ActionOpen || action == helper.ActionClose
}
func fresh(at, now time.Time, maxAge time.Duration) bool {
	return !at.IsZero() && !now.Before(at) && now.Sub(at) <= maxAge
}
func positive(value float64) bool { return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0) }
func key(value string) bool {
	if len(value) < 32 || len(value) > 44 {
		return false
	}
	data, err := base58.Decode(value)
	return err == nil && len(data) == 32
}
