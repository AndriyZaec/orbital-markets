package account

import "testing"

func TestParseSnapshotParts(t *testing.T) {
	mode, err := ParseSnapshotPart("get_position_mode", []byte(`{"dualSidePosition":false}`))
	if err != nil || mode.Mode == nil || !mode.Mode.OneWay {
		t.Fatalf("mode = %+v, err = %v", mode, err)
	}
	margin, err := ParseSnapshotPart("get_account", []byte(
		`{"canTrade":true,"totalMarginBalance":"123.5","availableBalance":"100.25","positions":[]}`,
	))
	if err != nil || margin.Margin == nil || !margin.Margin.CanTrade || margin.Margin.Equity != 123.5 || margin.Margin.Available != 100.25 {
		t.Fatalf("margin = %+v, err = %v", margin, err)
	}
	positions, err := ParseSnapshotPart("get_positions", []byte(`[
		{"symbol":"BTCUSDT","positionSide":"BOTH","positionAmt":"0.25","entryPrice":"100","unRealizedProfit":"2","leverage":"5","liquidationPrice":"50","isolatedMargin":"10"},
		{"symbol":"ETHUSDT","positionSide":"BOTH","positionAmt":"-2","entryPrice":"20","unRealizedProfit":"-1","leverage":"4","liquidationPrice":"30","isolatedMargin":"0"},
		{"symbol":"SOLUSDT","positionSide":"BOTH","positionAmt":"0","entryPrice":"0","unRealizedProfit":"0","leverage":"3","liquidationPrice":"0","isolatedMargin":"0"}
	]`))
	if err != nil || positions.Positions == nil || len(*positions.Positions) != 2 {
		t.Fatalf("positions = %+v, err = %v", positions, err)
	}
	if (*positions.Positions)[0].Side != "long" || (*positions.Positions)[1].Side != "short" || (*positions.Positions)[1].Size != 2 {
		t.Fatalf("normalized positions = %+v", *positions.Positions)
	}
}

func TestParsePositionsRejectsDuplicateAndNonFiniteValues(t *testing.T) {
	duplicate := []byte(`[
		{"symbol":"BTCUSDT","positionSide":"BOTH","positionAmt":"1","entryPrice":"100","leverage":"5","liquidationPrice":"50"},
		{"symbol":"BTCUSDT","positionSide":"BOTH","positionAmt":"2","entryPrice":"100","leverage":"5","liquidationPrice":"50"}
	]`)
	if _, err := ParsePositions(duplicate); err == nil {
		t.Fatal("duplicate position was accepted")
	}
	nonFinite := []byte(`[{"symbol":"BTCUSDT","positionSide":"BOTH","positionAmt":"NaN","entryPrice":"100","leverage":"5","liquidationPrice":"50"}]`)
	if _, err := ParsePositions(nonFinite); err == nil {
		t.Fatal("non-finite position was accepted")
	}
}

func TestParseLeverageData(t *testing.T) {
	update, err := ParseLeverageUpdate([]byte(`{"symbol":"BTCUSDT","leverage":5}`))
	if err != nil || update.Symbol != "BTCUSDT" || update.Leverage != 5 {
		t.Fatalf("update = %+v, err = %v", update, err)
	}
	brackets, err := ParseLeverageBrackets([]byte(`{"symbol":"BTCUSDT","brackets":[
		{"initialLeverage":20,"notionalCap":10000,"notionalFloor":0},
		{"initialLeverage":10,"notionalCap":100000,"notionalFloor":10000}
	]}`), "BTCUSDT")
	if err != nil || len(brackets["BTCUSDT"]) != 2 || brackets["BTCUSDT"][0].InitialLeverage != 20 {
		t.Fatalf("brackets = %+v, err = %v", brackets, err)
	}
}
