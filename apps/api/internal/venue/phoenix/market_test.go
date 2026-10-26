package phoenix

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchExchangeMetadataIncludesAllPerpetualMarkets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/view/exchange" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{
			"keys":{
				"perpAssetMap":"asset-map",
				"globalTraderIndex":["index-1","index-2"],
				"activeTraderBuffer":["buffer-1"]
			},
			"markets":[
				{"symbol":"SOL","marketStatus":"active","marketPubkey":"sol-market","splinePubkey":"sol-spline","tickSize":100,"baseLotsDecimals":2,"fundingIntervalSeconds":3600,"leverageTiers":[{"maxLeverage":25}],"statsSnapshot":{"openInterestBaseLots":"12345"}},
				{"symbol":"AAPL","marketStatus":"active","marketPubkey":"aapl-market","splinePubkey":"aapl-spline","tickSize":100,"baseLotsDecimals":-1,"fundingIntervalSeconds":7200,"leverageTiers":[{"maxLeverage":20}],"commodityMetadata":{"status":"active"},"statsSnapshot":{"openInterestBaseLots":3}},
				{"symbol":"OLD-PERP","marketStatus":"closed","marketPubkey":"old-market","splinePubkey":"old-spline","tickSize":1,"baseLotsDecimals":0,"fundingIntervalSeconds":3600,"leverageTiers":[{"maxLeverage":1}],"statsSnapshot":null}
			]
		}`)
	}))
	defer server.Close()

	metadata, err := NewClient(server.URL, server.Client()).FetchExchangeMetadata(context.Background())
	if err != nil {
		t.Fatalf("fetch exchange metadata: %v", err)
	}
	if metadata.Accounts.PerpAssetMap != "asset-map" || len(metadata.Accounts.GlobalTraderIndex) != 2 ||
		len(metadata.Accounts.ActiveTraderBuffer) != 1 {
		t.Fatalf("unexpected exchange accounts: %+v", metadata.Accounts)
	}
	if len(metadata.Markets) != 3 {
		t.Fatalf("markets = %+v", metadata.Markets)
	}

	bySymbol := make(map[string]MarketMetadata, len(metadata.Markets))
	for _, market := range metadata.Markets {
		bySymbol[market.Symbol] = market
	}
	sol := bySymbol["SOL"]
	if sol.Status != "active" || sol.Asset != "SOL" || sol.AmountIncrement != 0.01 || sol.PriceIncrement != 0.01 ||
		sol.OpenInterest != 123.45 || sol.MaxLeverage != 25 || sol.MarketPubkey != "sol-market" {
		t.Fatalf("unexpected SOL metadata: %+v", sol)
	}
	aapl := bySymbol["AAPL"]
	if aapl.Status != "active" || aapl.Asset != "AAPL" || aapl.AmountIncrement != 10 ||
		aapl.PriceIncrement != 0.00001 || aapl.OpenInterest != 30 || aapl.MaxLeverage != 20 {
		t.Fatalf("RWA metadata was filtered or normalized incorrectly: %+v", aapl)
	}
	old := bySymbol["OLD-PERP"]
	if old.Asset != "OLD" || old.Status != "closed" || old.OpenInterest != 0 {
		t.Fatalf("unexpected inactive metadata: %+v", old)
	}
}

func TestFetchExchangeMetadataFailsClosedOnMissingAccounts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"keys":{},"markets":[{"symbol":"SOL"}]}`)
	}))
	defer server.Close()

	_, err := NewClient(server.URL, server.Client()).FetchExchangeMetadata(context.Background())
	if err == nil {
		t.Fatal("expected missing exchange accounts error")
	}
}

func TestFetchExchangeMetadataRejectsUnknownMarketStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{
			"keys":{"perpAssetMap":"map","globalTraderIndex":["index"],"activeTraderBuffer":["buffer"]},
			"markets":[{"symbol":"SOL","marketStatus":"mystery","marketPubkey":"market","splinePubkey":"spline","tickSize":1,"baseLotsDecimals":0,"fundingIntervalSeconds":3600,"leverageTiers":[{"maxLeverage":1}]}]
		}`)
	}))
	defer server.Close()

	_, err := NewClient(server.URL, server.Client()).FetchExchangeMetadata(context.Background())
	if err == nil {
		t.Fatal("expected unknown market status error")
	}
}

func TestClientRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write(make([]byte, maxResponseBytes+1))
	}))
	defer server.Close()

	_, err := NewClient(server.URL, server.Client()).FetchExchangeMetadata(context.Background())
	if err == nil {
		t.Fatal("expected oversized response error")
	}
}
