package account

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

var fundingSymbolPattern = regexp.MustCompile(`^[A-Z0-9_]{1,32}$`)

func ParseFundingPayments(body []byte, account string, now time.Time) ([]venue.FundingPayment, error) {
	var rows []struct {
		Symbol     string          `json:"symbol"`
		IncomeType string          `json:"incomeType"`
		Income     string          `json:"income"`
		Asset      string          `json:"asset"`
		Time       int64           `json:"time"`
		TranID     json.RawMessage `json:"tranId"`
	}
	if err := decodeJSON(body, &rows); err != nil || rows == nil {
		return nil, fmt.Errorf("invalid income history")
	}
	payments := make([]venue.FundingPayment, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		amount, err := strconv.ParseFloat(row.Income, 64)
		transactionID := strings.Trim(string(row.TranID), `"`)
		paidAt := time.UnixMilli(row.Time).UTC()
		market := strings.TrimSuffix(row.Symbol, "USDT")
		if row.IncomeType != "FUNDING_FEE" || row.Asset != "USDT" || !fundingSymbolPattern.MatchString(row.Symbol) ||
			market == row.Symbol || market == "" || err != nil || math.IsNaN(amount) || math.IsInf(amount, 0) ||
			!decimalDigits(transactionID) || seen[transactionID] || row.Time <= 0 || paidAt.After(now.Add(time.Minute)) {
			return nil, fmt.Errorf("invalid funding income row")
		}
		seen[transactionID] = true
		payments = append(payments, venue.FundingPayment{
			ExternalID: transactionID, Venue: "aster", Account: strings.ToLower(account),
			Asset: market, MarketKey: row.Symbol, AmountUSD: amount, PaidAt: paidAt,
		})
	}
	return payments, nil
}

func decimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
