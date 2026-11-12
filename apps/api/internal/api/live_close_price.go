package api

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/liquidity"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

// Leave room for the structured response before the browser's 10s close timeout.
const liveClosePrepareTimeout = 8 * time.Second

// This covers all REST attempts and cache waiting for one kill leg together.
const liveKillLegPrepareTimeout = 3 * time.Second

type closeBookSource interface {
	FreshBBO(context.Context, liquidity.Market) (liquidity.BBOQuote, error)
}

// EnableCloseBooks shares the opening liquidity service's flights and quotas.
// Configure at startup, before serving requests.
func (s *Server) EnableCloseBooks(books closeBookSource) { s.closeBooks = books }

func closeSidePrice(side domain.Side, bid, ask float64) (float64, error) {
	price := bid
	if side == domain.SideShort {
		price = ask
	} else if side != domain.SideLong {
		return 0, fmt.Errorf("invalid position side: %q", side)
	}
	if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return 0, fmt.Errorf("close book price unavailable")
	}
	return price, nil
}

func (s *Server) cachedClosePrice(ctx context.Context, fill executor.LiveFill, asset string) (float64, error) {
	if s.closeMarkets == nil {
		return 0, fmt.Errorf("current %s BBO for %s unavailable", fill.Venue, fill.Symbol)
	}
	market, err := s.closeMarkets.MarketSnapshot(ctx, fill.Venue, asset)
	if err != nil {
		return 0, err
	}
	bookAt := market.Timestamp
	if market.Health != nil {
		book := market.Health.Book
		if book.Status != venue.HealthAvailable {
			return 0, fmt.Errorf("current %s BBO for %s is stale", fill.Venue, fill.Symbol)
		}
		bookAt = book.SourceTime
		if bookAt.IsZero() {
			bookAt = book.ReceivedAt
		}
	}
	age := time.Since(bookAt)
	if bookAt.IsZero() || age > maxCloseQuoteAge || age < -time.Second {
		return 0, fmt.Errorf("current %s BBO for %s is stale", fill.Venue, fill.Symbol)
	}
	size := market.BidSize
	if domain.Side(fill.Side) == domain.SideShort {
		size = market.AskSize
	}
	if size <= 0 || math.IsNaN(size) || math.IsInf(size, 0) {
		return 0, fmt.Errorf("current %s BBO size unavailable", fill.Venue)
	}
	return closeSidePrice(domain.Side(fill.Side), market.BidPrice, market.AskPrice)
}

// closeReferencePrice never uses mark/oracle or the historical fill as a BBO
// substitute. All attempts and cache waiting consume the caller's one deadline.
func (s *Server) closeReferencePrice(ctx context.Context, fill executor.LiveFill, asset string) (float64, error) {
	if domain.Side(fill.Side) != domain.SideLong && domain.Side(fill.Side) != domain.SideShort {
		return 0, fmt.Errorf("invalid position side: %q", fill.Side)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if price, err := s.cachedClosePrice(ctx, fill, asset); err == nil {
		return price, nil
	}
	for attempt := 0; attempt < 2 && s.closeBooks != nil; attempt++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		quote, err := s.closeBooks.FreshBBO(ctx, liquidity.Market{Venue: fill.Venue, Key: fill.Symbol})
		age := time.Since(quote.ObservedAt)
		if err == nil && !quote.ObservedAt.IsZero() && age <= maxCloseQuoteAge && age >= -time.Second {
			if price, err := closeSidePrice(domain.Side(fill.Side), quote.BidPrice, quote.AskPrice); err == nil {
				return price, nil
			}
		}
	}
	// REST is exhausted: subsequent checks only read the cached market feeds.
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return 0, fmt.Errorf("close price for %s unavailable: %w", fill.Venue, err)
		}
		if price, err := s.cachedClosePrice(ctx, fill, asset); err == nil {
			return price, nil
		}
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("close price for %s unavailable: %w", fill.Venue, ctx.Err())
		case <-ticker.C:
		}
	}
}
