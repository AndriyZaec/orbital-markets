package helper

import (
	"context"
	"errors"

	"github.com/mr-tron/base58"
)

type FlightRequest struct {
	BuilderAuthority      string `json:"builder_authority"`
	ProtocolRootAuthority string `json:"protocol_root_authority"`
}

type FlightAccounts struct {
	ProtocolRootAuthority        string `json:"protocol_root_authority"`
	GlobalConfiguration          string `json:"global_configuration"`
	CollateralTransferPermission string `json:"collateral_transfer_permission"`
	PhoenixProgramID             string `json:"phoenix_program_id"`
	FlightProgramID              string `json:"flight_program_id"`
	BuilderAuthority             string `json:"builder_authority"`
	GlobalState                  string `json:"global_state"`
	BuilderState                 string `json:"builder_state"`
	BuilderTraderAccount         string `json:"builder_trader_account"`
	CollateralTransferAuthority  string `json:"collateral_transfer_authority"`
}

type FlightAccountData struct {
	Address    string `json:"address"`
	Owner      string `json:"owner"`
	DataBase64 string `json:"data_base64"`
}

type VerifyFlightRequest struct {
	BuilderAuthority             string            `json:"builder_authority"`
	ProtocolRootAuthority        string            `json:"protocol_root_authority"`
	GlobalConfiguration          FlightAccountData `json:"global_configuration"`
	CollateralTransferPermission FlightAccountData `json:"collateral_transfer_permission"`
	GlobalState                  FlightAccountData `json:"global_state"`
	BuilderState                 FlightAccountData `json:"builder_state"`
	BuilderTrader                FlightAccountData `json:"builder_trader"`
}

type VerifiedFlight struct {
	Accounts FlightAccounts `json:"accounts"`
	FeeBPS   uint64         `json:"fee_bps"`
	Active   bool           `json:"active"`
}

func ValidateFlightAccounts(builder string, r FlightAccounts) error {
	if r.BuilderAuthority != builder || r.PhoenixProgramID != PhoenixProgramID || r.FlightProgramID != FlightProgramID {
		return errors.New("Phoenix Flight accounts scope mismatch")
	}
	for _, key := range []string{builder, r.ProtocolRootAuthority, r.GlobalConfiguration, r.CollateralTransferPermission, r.GlobalState, r.BuilderState, r.BuilderTraderAccount, r.CollateralTransferAuthority} {
		if !validKey(key) {
			return errors.New("invalid Phoenix Flight account key")
		}
	}
	return nil
}

func validKey(key string) bool {
	decoded, err := base58.Decode(key)
	return err == nil && len(decoded) == 32 && len(key) >= 32 && len(key) <= 44
}

func (c *Client) FlightAccounts(ctx context.Context, q FlightRequest) (FlightAccounts, error) {
	var r FlightAccounts
	if err := c.call(ctx, "flight_accounts", q, &r); err != nil {
		return FlightAccounts{}, err
	}
	if err := ValidateFlightAccounts(q.BuilderAuthority, r); err != nil {
		c.stop()
		return FlightAccounts{}, err
	}
	if r.ProtocolRootAuthority != q.ProtocolRootAuthority {
		c.stop()
		return FlightAccounts{}, errors.New("Phoenix protocol root binding mismatch")
	}
	return r, nil
}

func (c *Client) VerifyFlight(ctx context.Context, q VerifyFlightRequest) (VerifiedFlight, error) {
	var r VerifiedFlight
	if err := c.call(ctx, "verify_flight", q, &r); err != nil {
		return VerifiedFlight{}, err
	}
	if err := ValidateFlightAccounts(q.BuilderAuthority, r.Accounts); err != nil {
		c.stop()
		return VerifiedFlight{}, err
	}
	if !r.Active || r.FeeBPS != 2 || r.Accounts.ProtocolRootAuthority != q.ProtocolRootAuthority || r.Accounts.GlobalConfiguration != q.GlobalConfiguration.Address || r.Accounts.CollateralTransferPermission != q.CollateralTransferPermission.Address || q.GlobalConfiguration.Owner != PhoenixProgramID || q.CollateralTransferPermission.Owner != PhoenixProgramID || r.Accounts.GlobalState != q.GlobalState.Address || r.Accounts.BuilderState != q.BuilderState.Address || r.Accounts.BuilderTraderAccount != q.BuilderTrader.Address || q.GlobalState.Owner != FlightProgramID || q.BuilderState.Owner != FlightProgramID || q.BuilderTrader.Owner != PhoenixProgramID {
		c.stop()
		return VerifiedFlight{}, errors.New("Phoenix Flight verification binding mismatch")
	}
	return r, nil
}
