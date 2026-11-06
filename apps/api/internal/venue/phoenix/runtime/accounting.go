package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/orders"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/rpc"
)

func (r *Runtime) OrderAccounting(ctx context.Context, q *domain.SigningRequest, signature string) (orders.ObservedAccounting, error) {
	var zero orders.ObservedAccounting
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if err := r.ctx.Err(); err != nil {
		return zero, err
	}
	p, err := orders.Decode(q)
	if err != nil {
		return zero, err
	}
	reader, ok := r.chain.(interface {
		Transaction(context.Context, string, uint64) (rpc.Transaction, error)
	})
	if !ok {
		return zero, errors.New("Phoenix accounting RPC unavailable")
	}
	tx, err := reader.Transaction(ctx, signature, p.Metadata.MinContextSlot)
	if err != nil {
		return zero, err
	}
	packet, err := base64.StdEncoding.DecodeString(tx.PacketBase64)
	if err != nil || len(packet) <= 129 {
		return zero, rpc.ErrInvalidResponse
	}
	message, _ := base64.StdEncoding.DecodeString(p.Metadata.Built.MessageBase64)
	if !bytes.Equal(packet[129:], message) || tx.Slot > p.LastSlot {
		return zero, errors.New("observed Phoenix transaction differs from stored order")
	}
	inner, err := json.Marshal(tx.InnerInstructions)
	if err != nil {
		return zero, err
	}
	var builder *string
	if flight := p.Metadata.Built.Manifest.Flight; flight != nil {
		authority := flight.BuilderAuthority
		builder = &authority
	}
	result, err := r.helper.DecodeAccounting(r.ctx, helper.AccountingRequest{OwnerAuthority: q.Account, BuilderAuthority: builder, ExpectedMessageBase64: p.Metadata.Built.MessageBase64, TransactionBase64: tx.PacketBase64, InnerInstructions: inner})
	if err != nil {
		return zero, err
	}
	if ctx.Err() != nil {
		return zero, ctx.Err()
	}
	if result.TraderAccount != p.Metadata.Built.TraderAccount {
		return zero, errors.New("observed Phoenix accounting trader mismatch")
	}
	return orders.ObservedAccounting{Slot: tx.Slot, NetworkFeeLamports: strconv.FormatUint(tx.FeeLamports, 10), Events: result}, nil
}
