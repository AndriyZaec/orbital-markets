package rpc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/mr-tron/base58"
)

type InnerInstruction struct {
	ProgramIDIndex uint16   `json:"programIdIndex"`
	Accounts       []uint16 `json:"accounts"`
	Data           string   `json:"data"`
	StackHeight    *uint32  `json:"stackHeight"`
}
type InnerGroup struct {
	Index        uint32             `json:"index"`
	Instructions []InnerInstruction `json:"instructions"`
}
type Transaction struct {
	Slot              uint64       `json:"slot"`
	BlockTime         *int64       `json:"blockTime"`
	PacketBase64      string       `json:"packet_base64"`
	FeeLamports       uint64       `json:"fee_lamports"`
	InnerInstructions []InnerGroup `json:"inner_instructions"`
}

// Transaction reads a known signature only; no address-wide transaction scan.
// The packet/signature binding is checked before handing data to the SDK decoder.
func (c *Client) Transaction(ctx context.Context, signature string, minSlot uint64) (Transaction, error) {
	if !validBase58(signature, 64) {
		return Transaction{}, errors.New("invalid Phoenix accounting signature")
	}
	var raw struct {
		Slot      uint64          `json:"slot"`
		BlockTime *int64          `json:"blockTime"`
		Packet    []string        `json:"transaction"`
		Version   json.RawMessage `json:"version"`
		Meta      *struct {
			Err    json.RawMessage `json:"err"`
			Fee    *uint64         `json:"fee"`
			Inner  *[]InnerGroup   `json:"innerInstructions"`
			Loaded *struct {
				Writable []string `json:"writable"`
				Readonly []string `json:"readonly"`
			} `json:"loadedAddresses"`
		} `json:"meta"`
	}
	if err := c.call(ctx, "getTransaction", []any{signature, map[string]any{"encoding": "base64", "commitment": "confirmed", "maxSupportedTransactionVersion": 0}}, &raw); err != nil {
		return Transaction{}, err
	}
	if raw.Slot < minSlot || raw.Slot == 0 || raw.Meta == nil || !bytes.Equal(bytes.TrimSpace(raw.Meta.Err), []byte("null")) || raw.Meta.Fee == nil || raw.Meta.Inner == nil || len(raw.Packet) != 2 || raw.Packet[1] != "base64" || len(raw.Packet[0]) > base64.StdEncoding.EncodedLen(1232) {
		return Transaction{}, ErrInvalidResponse
	}
	if len(raw.Version) > 0 && !bytes.Equal(raw.Version, []byte(`"legacy"`)) {
		return Transaction{}, ErrInvalidResponse
	}
	if raw.Meta.Loaded != nil && (len(raw.Meta.Loaded.Writable) != 0 || len(raw.Meta.Loaded.Readonly) != 0) {
		return Transaction{}, ErrInvalidResponse
	}
	packet, err := base64.StdEncoding.DecodeString(raw.Packet[0])
	if err != nil || len(packet) <= 129 || len(packet) > 1232 || packet[0] != 2 || base58.Encode(packet[1:65]) != signature {
		return Transaction{}, ErrInvalidResponse
	}
	if len(*raw.Meta.Inner) > 8 {
		return Transaction{}, ErrInvalidResponse
	}
	count := 0
	for _, group := range *raw.Meta.Inner {
		count += len(group.Instructions)
		if count > 512 {
			return Transaction{}, ErrInvalidResponse
		}
	}
	return Transaction{Slot: raw.Slot, BlockTime: raw.BlockTime, PacketBase64: raw.Packet[0], FeeLamports: *raw.Meta.Fee, InnerInstructions: *raw.Meta.Inner}, nil
}
