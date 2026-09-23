package account

import (
	"testing"
	"time"
)

func TestParseFundingPaymentsNormalizesStrictRows(t *testing.T) {
	now := time.UnixMilli(1_750_000_000_000)
	payments, err := ParseFundingPayments([]byte(`[
		{"symbol":"2ZUSDT","incomeType":"FUNDING_FEE","income":"-0.0125","asset":"USDT","time":1749999999000,"tranId":"9689322392"}
	]`), "0xABC", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(payments) != 1 || payments[0].ExternalID != "9689322392" || payments[0].Account != "0xabc" ||
		payments[0].Asset != "2Z" || payments[0].MarketKey != "2ZUSDT" || payments[0].AmountUSD != -0.0125 {
		t.Fatalf("payments = %+v", payments)
	}
}

func TestParseFundingPaymentsRejectsInvalidRows(t *testing.T) {
	now := time.UnixMilli(1_750_000_000_000)
	rows := []string{
		`[{"symbol":"2ZUSDT","incomeType":"COMMISSION","income":"1","asset":"USDT","time":1749999999000,"tranId":"1"}]`,
		`[{"symbol":"2ZUSD","incomeType":"FUNDING_FEE","income":"1","asset":"USDT","time":1749999999000,"tranId":"1"}]`,
		`[{"symbol":"B-MONEYUSDT","incomeType":"FUNDING_FEE","income":"1","asset":"USDT","time":1749999999000,"tranId":"1"}]`,
		`[{"symbol":"2ZUSDT","incomeType":"FUNDING_FEE","income":"NaN","asset":"USDT","time":1749999999000,"tranId":"1"}]`,
		`[{"symbol":"2ZUSDT","incomeType":"FUNDING_FEE","income":"1","asset":"USDT","time":1750000060001,"tranId":"1"}]`,
		`[{"symbol":"2ZUSDT","incomeType":"FUNDING_FEE","income":"1","asset":"USDT","time":1749999999000,"tranId":"1"},{"symbol":"2ZUSDT","incomeType":"FUNDING_FEE","income":"1","asset":"USDT","time":1749999999001,"tranId":"1"}]`,
	}
	for _, body := range rows {
		if _, err := ParseFundingPayments([]byte(body), "0xabc", now); err == nil {
			t.Fatalf("invalid rows accepted: %s", body)
		}
	}
}
