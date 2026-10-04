package main

import (
	"context"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/dataagent"
)

type stubAsterFundingReader struct {
	dataagent.Reader
	payments []venue.FundingPayment
}

func (r stubAsterFundingReader) ReadFunding(context.Context, string, time.Time, time.Time) ([]venue.FundingPayment, error) {
	return r.payments, nil
}

func TestAsterFundingHistoryReturnsOnlyRequestedAsset(t *testing.T) {
	history := asterFundingHistory{reader: stubAsterFundingReader{payments: []venue.FundingPayment{
		{ExternalID: "2z-1", Asset: "2Z"},
		{ExternalID: "rez-1", Asset: "REZ"},
		{ExternalID: "2z-2", Asset: "2z"},
	}}}

	payments, err := history.FundingPayments(context.Background(), "owner", "2Z", time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(payments) != 2 || payments[0].ExternalID != "2z-1" || payments[1].ExternalID != "2z-2" {
		t.Fatalf("payments = %+v, want only requested asset", payments)
	}
}
