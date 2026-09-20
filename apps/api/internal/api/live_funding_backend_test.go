package api

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

const fundingTestAsterOwner = "0x1111111111111111111111111111111111111111"

type recordingAsterFundingFeed struct {
	fakeAccountFeed
	refreshes atomic.Int64
	started   chan struct{}
	release   chan struct{}
}

func (f *recordingAsterFundingFeed) RefreshFunding(ctx context.Context) error {
	f.refreshes.Add(1)
	if f.started != nil {
		close(f.started)
	}
	if f.release == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.release:
		return nil
	}
}

type recordingAsterFundingFactory struct {
	feed   *recordingAsterFundingFeed
	starts atomic.Int64
}

func (f *recordingAsterFundingFactory) Normalize(account string) (string, error) {
	account = strings.ToLower(strings.TrimSpace(account))
	if account == "" {
		return "", fmt.Errorf("account required")
	}
	return account, nil
}

func (f *recordingAsterFundingFactory) Start(context.Context, string) (liveAccountFeed, error) {
	f.starts.Add(1)
	return f.feed, nil
}

func TestAsterFundingRecoveryRefreshesEachClosedOwnerOnce(t *testing.T) {
	server, database := newResidualExposureServer(t)
	bindings := `{"aster":"` + fundingTestAsterOwner + `","pacifica":"sol-wallet"}`
	if _, err := database.Exec(`
		UPDATE live_positions SET state = 'closed', venue_b = 'aster',
			account_bindings_json = ?, account_bindings_key = ?,
			completed_at = '2026-07-22T12:02:00Z'
		WHERE id = 'position-residual'`, bindings, bindings); err != nil {
		t.Fatal(err)
	}
	feed := &recordingAsterFundingFeed{started: make(chan struct{}), release: make(chan struct{})}
	factory := &recordingAsterFundingFactory{feed: feed}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server.live.accounts = newAccountFeedRegistry(ctx, map[string]accountFeedFactory{
		"aster": factory,
	}, accountFeedRegistryConfig{})

	server.refreshUnfinalizedAsterFunding()

	select {
	case <-feed.started:
	case <-time.After(time.Second):
		t.Fatal("funding recovery did not start")
	}
	close(feed.release)
	if starts, refreshes := factory.starts.Load(), feed.refreshes.Load(); starts != 1 || refreshes != 1 {
		t.Fatalf("feed starts = %d, funding refreshes = %d, want 1 each", starts, refreshes)
	}
}

func TestApplyAsterFundingBatchAppliesCurrentPaymentToOldPositionWithoutClaimingCoverage(t *testing.T) {
	server, database := newResidualExposureServer(t)
	bindings := `{"aster":"` + fundingTestAsterOwner + `","pacifica":"sol-wallet"}`
	if _, err := database.Exec(`
		UPDATE live_positions SET state = 'closed', venue_b = 'aster',
			account_bindings_json = ?, account_bindings_key = ?,
			completed_at = '2026-07-22T12:02:00Z'
		WHERE id = 'position-residual';
		INSERT INTO live_fills (
			position_id, leg, venue, symbol, side, order_id, client_order_id,
			requested_amount, filled_amount, avg_fill_price, fill_ratio, fee,
			accepted, filled, error, filled_at
		) VALUES (
			'position-residual', 2, 'aster', 'SOLUSDT', 'short', 'aster-order', 'aster-client',
			10, 2.75, 100, 0.275, 0, 1, 1, '', '2026-07-22T12:00:00Z'
		)`, bindings, bindings); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE live_positions SET opened_at = '2026-06-22T12:00:00Z' WHERE id = 'position-residual'`); err != nil {
		t.Fatal(err)
	}
	submittedAt := time.Date(2026, 7, 22, 12, 3, 0, 0, time.UTC)
	payment := venue.FundingPayment{
		Venue: "aster", Account: fundingTestAsterOwner, ExternalID: "funding-1",
		Asset: "SOL", MarketKey: "SOLUSDT", AmountUSD: 1.25,
		PaidAt: time.Date(2026, 7, 22, 12, 1, 0, 0, time.UTC),
	}
	apply := func() error {
		return server.live.applyAsterFundingBatch(
			context.Background(), fundingTestAsterOwner, []venue.FundingPayment{payment},
			submittedAt, submittedAt,
		)
	}
	if err := apply(); err != nil {
		t.Fatal(err)
	}
	if err := apply(); err != nil {
		t.Fatal(err)
	}
	var count int
	var aggregate float64
	var coveredThrough *int64
	if err := database.QueryRow(`SELECT COUNT(*) FROM live_funding_payments WHERE position_id = 'position-residual'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`
		SELECT amount_usd, covered_through_ms FROM live_funding_coverage
		WHERE position_id = 'position-residual' AND venue = 'aster'`).Scan(&aggregate, &coveredThrough); err != nil {
		t.Fatal(err)
	}
	if count != 1 || aggregate != 1.25 || coveredThrough != nil {
		t.Fatalf("count = %d, aggregate = %v, covered through = %v", count, aggregate, coveredThrough)
	}
}
