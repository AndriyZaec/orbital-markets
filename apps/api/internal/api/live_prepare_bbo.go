package api

import (
	"context"
	"errors"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
)

// Wait only for transient BBO readiness, within the prepare request's deadline.
// Each check re-reads the existing market feeds; this never submits an order.
func waitForLiveBBO(ctx context.Context, progress *livePrepareResponse, check func() error) error {
	if err := check(); !errors.Is(err, scanner.ErrFreshBBOUnavailable) {
		return err
	}
	progress.Stage("waiting_bbo")
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := check(); !errors.Is(err, scanner.ErrFreshBBOUnavailable) {
				return err
			}
		}
	}
}
