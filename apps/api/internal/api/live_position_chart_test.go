package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

func TestHandleLivePositionIncludesDerivedChartContext(t *testing.T) {
	server, database := newResidualExposureServer(t)
	const now = "2026-07-22T12:00:00Z"
	_, err := database.Exec(`
		INSERT INTO live_fills (
			position_id, leg, venue, symbol, side, order_id, client_order_id,
			requested_amount, filled_amount, avg_fill_price, fill_ratio, fee,
			accepted, filled, error, filled_at
		) VALUES (
			'position-residual', 2, 'hyperliquid', 'SOL', 'short', 'order-2', 'client-2',
			10, 2.75, 100, 0.275, 0, 1, 1, '', ?
		)`, now)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"plan":{"id":"plan-1","asset":"SOL","leg_1":{"venue":"pacifica","fee":0.0005,"slippage":0.001},"leg_2":{"venue":"hyperliquid","fee":0.0005,"slippage":0.002}}}`)
	if err := server.liveStore.UpsertDurableSession(context.Background(), executor.DurableSessionRecord{
		ID: "session-plan-1", State: "complete", Payload: payload, Asset: "SOL",
		AccountPacifica: "sol-wallet", AccountHyperliquid: "0xwallet", ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := server.liveStore.FinishDurableSession(context.Background(), "session-plan-1", "complete", ""); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/live/positions/position-residual?account_pacifica=sol-wallet&account_hyperliquid=0xwallet", nil)
	response := httptest.NewRecorder()
	server.handleLivePosition(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	var body struct {
		ChartContext positionChartContext `json:"chart_context"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.ChartContext.Available || body.ChartContext.Direction != "long_a_short_b" ||
		body.ChartContext.ProjectionSource != "execution_plan" || body.ChartContext.SlippageEstimate != 0.003 {
		t.Fatalf("chart context = %+v", body.ChartContext)
	}
}

func TestBuildPositionChartContextUsesPersistedDirectionAndPlanAssumptions(t *testing.T) {
	position := &executor.LivePosition{
		ID: "plan-fbdae6a2-6368-481f-bd9a-6180e0feff0e", PlanID: "plan-fbdae6a2-6368-481f-bd9a-6180e0feff0e",
		OpportunityID: "2Z-pacifica-aster-long_b_short_a", Asset: "2Z", VenueA: "aster", VenueB: "pacifica",
		Notional: 15, CurrentSpread: 0,
	}
	fills := []executor.LiveFill{
		{Venue: "pacifica", Side: "short", Filled: true, FilledAmount: 315, Fee: 0.01},
		{Venue: "aster", Side: "long", Filled: true, FilledAmount: 315, Fee: 0.02},
	}
	payload := []byte(`{"plan":{"id":"plan-fbdae6a2-6368-481f-bd9a-6180e0feff0e","asset":"2Z","leg_1":{"venue":"aster","fee":0.0005,"slippage":0.00147679324894516},"leg_2":{"venue":"pacifica","fee":0.0005,"slippage":0.00105596620908127}}}`)

	context := buildPositionChartContext(position, fills, payload)
	if !context.Available || context.Direction != "long_a_short_b" {
		t.Fatalf("chart context = %+v", context)
	}
	if context.Asset != "2Z" || context.VenueA != "aster" || context.VenueB != "pacifica" || context.CurrentAPR != 0 || context.Notional != 15 {
		t.Fatalf("position fields = %+v", context)
	}
	if context.ProjectionSource != "execution_plan" || context.FeeEstimate != 0.001 {
		t.Fatalf("projection assumptions = %+v", context)
	}
	wantSlippage := 0.00147679324894516 + 0.00105596620908127
	if math.Abs(context.SlippageEstimate-wantSlippage) > 1e-12 {
		t.Fatalf("slippage estimate = %v, want %v", context.SlippageEstimate, wantSlippage)
	}
	encoded, err := json.Marshal(context)
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(encoded, &response); err != nil {
		t.Fatal(err)
	}
	if _, ok := response["current_apr"]; !ok {
		t.Fatalf("zero current_apr omitted from %s", encoded)
	}
}

func TestBuildPositionChartContextPreservesReverseDirectionAndCurrentAPR(t *testing.T) {
	position := &executor.LivePosition{PlanID: "plan-1", Asset: "SOL", VenueA: "aster", VenueB: "pacifica", Notional: 100, CurrentSpread: -0.12}
	fills := []executor.LiveFill{
		{Venue: "aster", Side: "short", Filled: true, FilledAmount: 1},
		{Venue: "pacifica", Side: "long", Filled: true, FilledAmount: 1},
	}

	context := buildPositionChartContext(position, fills, nil)
	if !context.Available || context.Direction != "long_b_short_a" || context.CurrentAPR != -0.12 {
		t.Fatalf("chart context = %+v", context)
	}
	if context.ProjectionSource != "fills_fallback" || context.SlippageEstimate != 0 || context.ProjectionWarning == "" {
		t.Fatalf("fallback projection = %+v", context)
	}
}

func TestBuildPositionChartContextFallsBackToRecordedFillFees(t *testing.T) {
	position := &executor.LivePosition{PlanID: "old-plan", Asset: "BTC", VenueA: "aster", VenueB: "pacifica", Notional: 200}
	fills := []executor.LiveFill{
		{Venue: "aster", Side: "long", Filled: true, FilledAmount: 1, Fee: 0.12},
		{Venue: "pacifica", Side: "short", Filled: true, FilledAmount: 1, Fee: 0.08},
	}

	context := buildPositionChartContext(position, fills, []byte(`{"plan":{"id":"old-plan"}}`))
	if !context.Available || math.Abs(context.FeeEstimate-0.001) > 1e-12 || context.SlippageEstimate != 0 {
		t.Fatalf("fallback context = %+v", context)
	}
}

func TestBuildPositionChartContextRejectsIncompleteOrConflictingFills(t *testing.T) {
	position := &executor.LivePosition{PlanID: "plan-1", Asset: "ETH", VenueA: "aster", VenueB: "pacifica", Notional: 100}
	tests := map[string][]executor.LiveFill{
		"missing leg": {{Venue: "aster", Side: "long", Filled: true, FilledAmount: 1}},
		"same side": {
			{Venue: "aster", Side: "long", Filled: true, FilledAmount: 1},
			{Venue: "pacifica", Side: "long", Filled: true, FilledAmount: 1},
		},
		"conflicting duplicate": {
			{Venue: "aster", Side: "long", Filled: true, FilledAmount: 1},
			{Venue: "aster", Side: "short", Filled: true, FilledAmount: 1},
			{Venue: "pacifica", Side: "short", Filled: true, FilledAmount: 1},
		},
	}
	for name, fills := range tests {
		t.Run(name, func(t *testing.T) {
			context := buildPositionChartContext(position, fills, nil)
			if context.Available || context.UnavailableReason == "" {
				t.Fatalf("chart context = %+v", context)
			}
		})
	}
}
