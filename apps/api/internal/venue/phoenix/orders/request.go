package orders

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/account"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	phoenixlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/live"
	"github.com/mr-tron/base58"
)

type Prepared struct {
	Metadata              phoenixlive.SigningMetadata
	Payload               phoenixlive.OrderPayload
	Lots, Ticks, LastSlot uint64
}

func nativeUint(s string) (uint64, error) {
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil || v == 0 || strconv.FormatUint(v, 10) != s {
		return 0, ErrConflict
	}
	return v, nil
}

// Decode validates the immutable stored envelope, not a browser transaction.
// Submission additionally rebuilds the instruction with the pinned SDK and
// requires byte-for-byte equality against current market account metadata.
func Decode(q *domain.SigningRequest) (Prepared, error) {
	var p Prepared
	if q == nil || q.Venue != "phoenix" || q.ID == "" || !account.ValidOwner(q.Account) || !account.ValidOwner(q.Signer) || q.Account == q.Signer || q.Symbol == "" ||
		(q.Side != "buy" && q.Side != "sell") || q.CreatedAt.IsZero() || !q.ExpiresAt.After(q.CreatedAt) || q.ExpiresAt.After(q.CreatedAt.Add(30*time.Second)) ||
		q.Amount <= 0 || math.IsNaN(q.Amount) || math.IsInf(q.Amount, 0) || q.LimitPrice <= 0 || math.IsNaN(q.LimitPrice) || math.IsInf(q.LimitPrice, 0) {
		return p, ErrConflict
	}
	switch q.Action {
	case "open", "close", "unwind", "emergency_close":
	default:
		return p, ErrConflict
	}
	if q.ReduceOnly != (q.Action != "open") {
		return p, ErrConflict
	}
	if _, _, err := requestDigest(q); err != nil {
		return p, err
	}
	if err := json.Unmarshal(q.UnsignedPayload, &p.Payload); err != nil {
		return p, err
	}
	if err := json.Unmarshal(q.VenueMetadata, &p.Metadata); err != nil {
		return p, err
	}
	cid, err := nativeUint(q.ClientOrderID)
	if err != nil {
		return p, err
	}
	p.Lots, err = nativeUint(p.Payload.NumBaseLots)
	if err != nil || p.Lots > math.MaxUint32 {
		return p, ErrConflict
	}
	p.Ticks, err = nativeUint(p.Payload.PriceLimitTicks)
	if err != nil || p.Ticks > math.MaxUint32 {
		return p, ErrConflict
	}
	p.LastSlot, err = nativeUint(p.Payload.LastValidSlot)
	if err != nil {
		return p, err
	}
	b, n, payload := p.Metadata.Built, p.Metadata.Normalized, p.Payload
	side := helper.SideBid
	if q.Side == "sell" {
		side = helper.SideAsk
	}
	amount, e1 := strconv.ParseFloat(n.Amount, 64)
	price, e2 := strconv.ParseFloat(n.LimitPrice, 64)
	if p.Metadata.ProtocolVersion != helper.ProtocolVersion || p.Metadata.MinContextSlot == 0 || p.Metadata.MinContextSlot >= p.LastSlot ||
		payload.Format != "solana_legacy_message" || payload.OwnerAuthority != q.Account || payload.PositionAuthority != q.Signer || payload.ClientOrderID != q.ClientOrderID ||
		payload.TraderAccount != b.TraderAccount || payload.MessageBase64 != b.MessageBase64 || payload.MessageSHA256 != b.MessageSHA256 || payload.FeePayer != b.Manifest.FeePayer ||
		n.Symbol != q.Symbol || n.Side != side || n.NumBaseLots != p.Lots || n.PriceLimitTicks != p.Ticks || n.MetadataGeneration != b.MetadataGeneration || e1 != nil || e2 != nil || amount != q.Amount || price != q.LimitPrice {
		return p, ErrConflict
	}
	request := helper.BuildOrderRequest{Action: helper.Action(q.Action), OwnerAuthority: q.Account, PositionAuthority: q.Signer, FeePayer: payload.FeePayer,
		ComputeBudget: b.Manifest.ComputeBudget,
		Symbol:        q.Symbol, Side: side, NumBaseLots: p.Lots, PriceLimitTicks: p.Ticks, ReduceOnly: q.ReduceOnly, ClientOrderID: cid, LastValidSlot: p.LastSlot,
		RecentBlockhash: b.RecentBlockhash, LastValidBlockHeight: b.LastValidBlockHeight, MetadataGeneration: b.MetadataGeneration, Accounts: helper.MarketAccounts{Orderbook: b.Manifest.Orderbook}}
	if b.Manifest.Flight != nil {
		request.BuilderAuthority = b.Manifest.Flight.BuilderAuthority
		request.Accounts.ProtocolRootAuthority = p.Metadata.ProtocolRootAuthority
	}
	if err := helper.ValidateBuildOrderResult(request, b); err != nil {
		return p, err
	}
	return p, nil
}

func verifySigned(q *domain.SigningRequest, p Prepared, signed domain.SignedAction) error {
	if signed.RequestID != q.ID || signed.ClientOrderID != q.ClientOrderID || signed.Venue != q.Venue || signed.SignerAddress != q.Signer || len(signed.Signature) > 88 {
		return ErrConflict
	}
	message, err := base64.StdEncoding.DecodeString(p.Payload.MessageBase64)
	if err != nil || len(message) == 0 || len(message)+129 > 1232 {
		return ErrConflict
	}
	hash := sha256.Sum256(message)
	if hex.EncodeToString(hash[:]) != p.Payload.MessageSHA256 {
		return ErrConflict
	}
	key, err := base58.Decode(q.Signer)
	if err != nil || len(key) != 32 {
		return ErrConflict
	}
	signature, err := base58.Decode(signed.Signature)
	if err != nil || len(signature) != 64 || !ed25519.Verify(key, message, signature) {
		return errors.New("invalid Phoenix agent signature")
	}
	return nil
}
