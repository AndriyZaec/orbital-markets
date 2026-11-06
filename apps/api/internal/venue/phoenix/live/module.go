package live

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/google/uuid"
)

type Preparer interface {
	NormalizeAmount(context.Context, string, float64) (float64, error)
	Prepare(context.Context, Intent) (*domain.SigningRequest, error)
}

// LiveModule translates shared orchestration parameters into server-owned
// Phoenix intents. Preparer owns account leases, chain evidence and SDK calls.
type LiveModule struct {
	ctx      context.Context
	preparer Preparer
}

var _ venue.LiveModule = (*LiveModule)(nil)

func NewLiveModule(ctx context.Context, preparer Preparer) (*LiveModule, error) {
	if ctx == nil || preparer == nil {
		return nil, errors.New("Phoenix order preparer unavailable")
	}
	return &LiveModule{ctx: ctx, preparer: preparer}, nil
}
func (*LiveModule) Name() string { return "phoenix" }
func (*LiveModule) Capabilities() venue.LiveCapabilities {
	return venue.LiveCapabilities{ClosePricePolicy: venue.ClosePriceFromMarketBBO, LeverageUpdate: venue.LeverageUpdateNotRequired}
}
func (m *LiveModule) NormalizeAmount(symbol string, amount float64) (float64, error) {
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()
	return m.preparer.NormalizeAmount(ctx, symbol, amount)
}
func (m *LiveModule) BuildOpen(p venue.LiveOrderParams) (*domain.SigningRequest, error) {
	return m.build(p, helper.ActionOpen, false)
}
func (m *LiveModule) BuildReduce(p venue.LiveReduceParams) (*domain.SigningRequest, error) {
	switch p.Action {
	case venue.ReduceActionClose, venue.ReduceActionUnwind, venue.ReduceActionEmergencyClose:
		return m.build(p.LiveOrderParams, helper.Action(p.Action), true)
	default:
		return nil, errors.New("unsupported Phoenix reduce action")
	}
}
func (*LiveModule) BuildLeverage(venue.LiveLeverageParams) (*domain.SigningRequest, error) {
	return nil, errors.New("Phoenix cross margin has no set-leverage action")
}

// ReductionLimit follows the current-BBO 0.5% close/unwind policy used by
// Hyperliquid. SDK bid-floor/ask-ceil quantization may only tighten this bound.
func ReductionLimit(price float64, side string) (float64, error) {
	if !positive(price) {
		return 0, errors.New("invalid Phoenix reduction reference")
	}
	switch side {
	case "buy":
		return price * 1.005, nil
	case "sell":
		return price * 0.995, nil
	default:
		return 0, errors.New("invalid Phoenix reduction side")
	}
}

func (m *LiveModule) build(p venue.LiveOrderParams, action helper.Action, reduce bool) (*domain.SigningRequest, error) {
	if err := m.ctx.Err(); err != nil {
		return nil, err
	}
	if !key(p.Account) || !key(p.Signer) || p.Account == p.Signer || p.Symbol == "" || len(p.Symbol) > 32 || !positive(p.Amount) || !positive(p.Price) || (!reduce && !positive(p.LimitPrice)) {
		return nil, errors.New("invalid Phoenix order intent")
	}
	side := "buy"
	switch p.Side {
	case domain.SideLong:
		if reduce {
			side = "sell"
		}
	case domain.SideShort:
		side = "sell"
		if reduce {
			side = "buy"
		}
	default:
		return nil, errors.New("invalid Phoenix position side")
	}
	cid, err := NativeClientOrderID(p.ClientOrderID)
	if err != nil {
		return nil, err
	}
	ctx := p.Context
	if ctx == nil {
		ctx = m.ctx
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	stop := context.AfterFunc(m.ctx, cancel)
	defer func() { stop(); cancel() }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return m.preparer.Prepare(ctx, Intent{ID: uuid.NewString(), ClientOrderID: cid, Owner: p.Account, Agent: p.Signer,
		Symbol: p.Symbol, Action: action, Side: side, Amount: p.Amount, ReferencePrice: p.Price, LimitPrice: p.LimitPrice})
}

// Existing orchestration supplies descriptive correlation IDs. Phoenix's wire
// CID is a u128 restricted by our contract to u64. Preserve canonical u64 input;
// domain-separated hashing maps shared opaque IDs deterministically into that
// namespace. The returned request's native decimal CID is the tracking identity.
func NativeClientOrderID(id string) (string, error) {
	if id == "" || len(id) > 128 || strings.TrimSpace(id) != id {
		return "", errors.New("invalid Phoenix client order ID")
	}
	if strings.IndexFunc(id, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
		n, err := strconv.ParseUint(id, 10, 64)
		if err != nil || n == 0 || strconv.FormatUint(n, 10) != id {
			return "", errors.New("invalid canonical Phoenix client order ID")
		}
		return id, nil
	}
	digest := sha256.Sum256([]byte("orbital:phoenix:client-order:v1:" + id))
	n := binary.LittleEndian.Uint64(digest[:8])
	if n == 0 {
		return "", errors.New("Phoenix client order ID maps to zero")
	}
	return strconv.FormatUint(n, 10), nil
}
