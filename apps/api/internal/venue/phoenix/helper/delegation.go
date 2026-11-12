package helper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
)

type DelegationAction string

const (
	ActionDelegate DelegationAction = "delegate"
	ActionRevoke   DelegationAction = "revoke"
)

type BuildDelegationRequest struct {
	Action               DelegationAction `json:"action"`
	OwnerAuthority       string           `json:"owner_authority"`
	NewPositionAuthority string           `json:"new_position_authority"`
	FeePayer             string           `json:"fee_payer"`
	RecentBlockhash      string           `json:"recent_blockhash"`
	LastValidBlockHeight uint64           `json:"last_valid_block_height"`
}

type BuildDelegationResult struct {
	MessageBase64        string             `json:"message_base64"`
	MessageSHA256        string             `json:"message_sha256"`
	RequiredSigners      []RequiredSigner   `json:"required_signers"`
	RecentBlockhash      string             `json:"recent_blockhash"`
	LastValidBlockHeight uint64             `json:"last_valid_block_height"`
	TraderAccount        string             `json:"trader_account"`
	TraderPDAIndex       uint8              `json:"trader_pda_index"`
	SubaccountIndex      uint8              `json:"subaccount_index"`
	TraderBump           uint8              `json:"trader_bump"`
	Manifest             DelegationManifest `json:"manifest"`
}

type DelegationManifest struct {
	Format               string                `json:"format"`
	PhoenixProgramID     string                `json:"phoenix_program_id"`
	Action               DelegationAction      `json:"action"`
	OwnerAuthority       string                `json:"owner_authority"`
	NewPositionAuthority string                `json:"new_position_authority"`
	FeePayer             string                `json:"fee_payer"`
	TraderAccount        string                `json:"trader_account"`
	AccountKeys          []AccountManifest     `json:"account_keys"`
	Instructions         []InstructionManifest `json:"instructions"`
}

func (c *Client) BuildDelegation(ctx context.Context, req BuildDelegationRequest) (BuildDelegationResult, error) {
	var result BuildDelegationResult
	if err := c.call(ctx, "build_delegation", req, &result); err != nil {
		return BuildDelegationResult{}, err
	}
	if err := validateBuildDelegationResult(req, result); err != nil {
		c.stop()
		return BuildDelegationResult{}, err
	}
	return result, nil
}

func validateBuildDelegationResult(req BuildDelegationRequest, result BuildDelegationResult) error {
	if req.LastValidBlockHeight == 0 || req.FeePayer == req.OwnerAuthority || req.FeePayer == req.NewPositionAuthority ||
		(req.Action != ActionDelegate && req.Action != ActionRevoke) ||
		(req.Action == ActionDelegate && req.NewPositionAuthority == req.OwnerAuthority) ||
		(req.Action == ActionRevoke && req.NewPositionAuthority != req.OwnerAuthority) {
		return errors.New("invalid Phoenix delegation intent")
	}
	message, err := base64.StdEncoding.DecodeString(result.MessageBase64)
	if err != nil || len(message) == 0 {
		return errors.New("invalid Phoenix delegation message_base64")
	}
	digest := sha256.Sum256(message)
	if !strings.EqualFold(result.MessageSHA256, hex.EncodeToString(digest[:])) {
		return errors.New("Phoenix delegation digest mismatch")
	}
	if result.RecentBlockhash != req.RecentBlockhash || result.LastValidBlockHeight != req.LastValidBlockHeight ||
		result.TraderPDAIndex != 0 || result.SubaccountIndex != 0 || result.TraderAccount == "" {
		return errors.New("Phoenix delegation blockhash or cross account mismatch")
	}
	m := result.Manifest
	if m.Format != "solana_legacy_message" || m.PhoenixProgramID != PhoenixProgramID || m.Action != req.Action ||
		m.OwnerAuthority != req.OwnerAuthority || m.NewPositionAuthority != req.NewPositionAuthority || m.FeePayer != req.FeePayer ||
		m.TraderAccount != result.TraderAccount || len(m.AccountKeys) == 0 || len(m.Instructions) != 3 || m.Instructions[2].ProgramID != PhoenixProgramID {
		return errors.New("Phoenix delegation manifest mismatch")
	}
	// Fixed default allowance for one native instruction, with no priority fee.
	budgetData := [][]byte{binary.LittleEndian.AppendUint32([]byte{2}, 200_000), binary.LittleEndian.AppendUint64([]byte{3}, 0)}
	for index, expected := range budgetData {
		instruction := m.Instructions[index]
		data, err := base64.StdEncoding.DecodeString(instruction.DataBase64)
		if err != nil || instruction.ProgramID != "ComputeBudget111111111111111111111111111111" || len(instruction.AccountIndexes) != 0 || !bytes.Equal(data, expected) {
			return errors.New("Phoenix delegation compute budget mismatch")
		}
	}
	if len(result.RequiredSigners) != 2 || result.RequiredSigners[0] != (RequiredSigner{Index: 0, Pubkey: req.FeePayer, Role: "fee_payer"}) ||
		result.RequiredSigners[1] != (RequiredSigner{Index: 1, Pubkey: req.OwnerAuthority, Role: "owner_authority"}) {
		return errors.New("Phoenix delegation requires fee payer and owner signers")
	}
	return nil
}
