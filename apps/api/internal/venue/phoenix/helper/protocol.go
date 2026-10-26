package helper

import "encoding/json"

const (
	ProtocolVersion      uint32 = 1
	PhoenixRiseVersion          = "0.6.13"
	PhoenixProgramID            = "EtrnLzgbS7nMMy5fbD42kXiUzGg8XQzJ972Xtk1cjWih"
	FlightProgramID             = "F1ightu9cujFYo34k9CabifLrJT8qzfDVM2Q7BqhJn2W"
	ExpectedFlightFeeBPS        = 2
)

type Action string

const (
	ActionOpen           Action = "open"
	ActionClose          Action = "close"
	ActionUnwind         Action = "unwind"
	ActionEmergencyClose Action = "emergency_close"
)

type Side string

const (
	SideBid Side = "bid"
	SideAsk Side = "ask"
)

type HealthResult struct {
	HelperVersion      string `json:"helper_version"`
	PhoenixRiseVersion string `json:"phoenix_rise_version"`
}

type BuildOrderRequest struct {
	Action               Action         `json:"action"`
	OwnerAuthority       string         `json:"owner_authority"`
	PositionAuthority    string         `json:"position_authority"`
	FeePayer             string         `json:"fee_payer"`
	BuilderAuthority     string         `json:"builder_authority,omitempty"`
	Symbol               string         `json:"symbol"`
	Side                 Side           `json:"side"`
	NumBaseLots          uint64         `json:"num_base_lots"`
	PriceLimitTicks      uint64         `json:"price_limit_ticks"`
	ReduceOnly           bool           `json:"reduce_only"`
	ClientOrderID        uint64         `json:"client_order_id"`
	LastValidSlot        uint64         `json:"last_valid_slot"`
	RecentBlockhash      string         `json:"recent_blockhash"`
	LastValidBlockHeight uint64         `json:"last_valid_block_height"`
	MetadataGeneration   uint64         `json:"metadata_generation"`
	Accounts             MarketAccounts `json:"accounts"`
}

type MarketAccounts struct {
	PerpAssetMap       string   `json:"perp_asset_map"`
	Orderbook          string   `json:"orderbook"`
	SplineCollection   string   `json:"spline_collection"`
	GlobalTraderIndex  []string `json:"global_trader_index"`
	ActiveTraderBuffer []string `json:"active_trader_buffer"`
}

type BuildOrderResult struct {
	MessageBase64        string           `json:"message_base64"`
	MessageSHA256        string           `json:"message_sha256"`
	RequiredSigners      []RequiredSigner `json:"required_signers"`
	RecentBlockhash      string           `json:"recent_blockhash"`
	LastValidBlockHeight uint64           `json:"last_valid_block_height"`
	MetadataGeneration   uint64           `json:"metadata_generation"`
	TraderAccount        string           `json:"trader_account"`
	TraderPDAIndex       uint8            `json:"trader_pda_index"`
	SubaccountIndex      uint8            `json:"subaccount_index"`
	TraderBump           uint8            `json:"trader_bump"`
	Manifest             OrderManifest    `json:"manifest"`
}

type RequiredSigner struct {
	Index  int    `json:"index"`
	Pubkey string `json:"pubkey"`
	Role   string `json:"role"`
}

type OrderManifest struct {
	Format             string                `json:"format"`
	PhoenixProgramID   string                `json:"phoenix_program_id"`
	Routing            string                `json:"routing"`
	Flight             *FlightManifest       `json:"flight"`
	IntentAction       Action                `json:"intent_action"`
	OwnerAuthority     string                `json:"owner_authority"`
	PositionAuthority  string                `json:"position_authority"`
	FeePayer           string                `json:"fee_payer"`
	TraderAccount      string                `json:"trader_account"`
	RequestedSymbol    string                `json:"requested_symbol"`
	Orderbook          string                `json:"orderbook"`
	MetadataGeneration uint64                `json:"metadata_generation"`
	Side               Side                  `json:"side"`
	NumBaseLots        uint64                `json:"num_base_lots"`
	PriceLimitTicks    uint64                `json:"price_limit_ticks"`
	ReduceOnly         bool                  `json:"reduce_only"`
	ClientOrderID      uint64                `json:"client_order_id"`
	LastValidSlot      uint64                `json:"last_valid_slot"`
	AccountKeys        []AccountManifest     `json:"account_keys"`
	Instructions       []InstructionManifest `json:"instructions"`
}

type FlightManifest struct {
	FlightProgramID          string `json:"flight_program_id"`
	BuilderAuthority         string `json:"builder_authority"`
	BuilderTraderAccount     string `json:"builder_trader_account"`
	BuilderState             string `json:"builder_state"`
	BuilderPDAIndex          uint8  `json:"builder_pda_index"`
	BuilderSubaccountIndex   uint8  `json:"builder_subaccount_index"`
	ExpectedRegisteredFeeBPS uint64 `json:"expected_registered_fee_bps"`
	UsesFeeOverride          bool   `json:"uses_fee_override"`
}

type AccountManifest struct {
	Index    int    `json:"index"`
	Pubkey   string `json:"pubkey"`
	Signer   bool   `json:"signer"`
	Writable bool   `json:"writable"`
}

type InstructionManifest struct {
	ProgramID      string  `json:"program_id"`
	ProgramIDIndex uint8   `json:"program_id_index"`
	AccountIndexes []uint8 `json:"account_indexes"`
	DataBase64     string  `json:"data_base64"`
}

type request struct {
	ProtocolVersion uint32 `json:"protocol_version"`
	RequestID       string `json:"request_id"`
	Operation       string `json:"operation"`
	Payload         any    `json:"payload,omitempty"`
}

type response struct {
	ProtocolVersion uint32          `json:"protocol_version"`
	RequestID       string          `json:"request_id"`
	OK              bool            `json:"ok"`
	Result          json.RawMessage `json:"result"`
	Error           *ProtocolError  `json:"error"`
}

type ProtocolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ProtocolError) Error() string {
	return "phoenix helper " + e.Code + ": " + e.Message
}
