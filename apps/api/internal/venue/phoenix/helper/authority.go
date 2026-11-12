package helper

import (
	"context"
	"errors"

	"github.com/mr-tron/base58"
)

type DecodeAuthorityRequest struct {
	OwnerAuthority string `json:"owner_authority"`
	TraderAccount  string `json:"trader_account"`
	AccountOwner   string `json:"account_owner"`
	DataBase64     string `json:"data_base64"`
}

type AuthorityResult struct {
	OwnerAuthority    string `json:"owner_authority"`
	TraderAccount     string `json:"trader_account"`
	PositionAuthority string `json:"position_authority"`
	TraderPDAIndex    uint8  `json:"trader_pda_index"`
	SubaccountIndex   uint8  `json:"subaccount_index"`
}

func (c *Client) DecodeTraderAuthority(ctx context.Context, req DecodeAuthorityRequest) (AuthorityResult, error) {
	var result AuthorityResult
	if err := c.call(ctx, "decode_trader_authority", req, &result); err != nil {
		return result, err
	}
	position, err := base58.Decode(result.PositionAuthority)
	if err != nil || len(position) != 32 || result.OwnerAuthority != req.OwnerAuthority || result.TraderAccount != req.TraderAccount || result.TraderPDAIndex != 0 || result.SubaccountIndex != 0 {
		c.stop()
		return AuthorityResult{}, errors.New("Phoenix helper returned mismatched trader authority")
	}
	return result, nil
}
