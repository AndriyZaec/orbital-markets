// Package funding reads the pinned settlement-history contract. Hourly accrual
// (Unix-second timestamps) is deliberately not a realized funding source.
package funding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/account"
)

type Markets interface {
	Snapshot() phoenix.MarketStreamSnapshot
}
type Reader struct {
	base    string
	http    *http.Client
	markets Markets
	mu      sync.Mutex
	retryAt time.Time
}

var _ venue.FundingHistory = (*Reader)(nil)

func (*Reader) FundingFinalizationDelay() time.Duration { return 24*time.Hour + 5*time.Minute }

func NewReader(base string, client *http.Client, markets Markets) *Reader {
	if base == "" {
		base = phoenix.DefaultRESTURL
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &Reader{base: strings.TrimRight(base, "/"), http: client, markets: markets}
}

type settlement struct {
	Timestamp  string `json:"timestamp"`
	Symbol     string `json:"symbol"`
	Payment    string `json:"fundingPayment"`
	Rate       string `json:"fundingRatePercentage"`
	Size       string `json:"positionSize"`
	Side       string `json:"positionSide"`
	PDA        *uint8 `json:"traderPdaIndex"`
	Subaccount *uint8 `json:"subaccountIndex"`
}
type response struct {
	Events    []settlement `json:"events"`
	HasMore   *bool        `json:"hasMore"`
	Next      *string      `json:"nextCursor"`
	Authority *string      `json:"authority"`
	PDA       *uint8       `json:"traderPdaIndex"`
}

func decimal(s string) (*big.Rat, error) {
	if s == "" || len(s) > 96 || strings.ContainsAny(s, "/eE+ \n\t") {
		return nil, errors.New("invalid Phoenix settlement decimal")
	}
	v, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, errors.New("invalid Phoenix settlement decimal")
	}
	return v, nil
}

func (r *Reader) FundingPayments(ctx context.Context, owner, asset string, since, until time.Time) ([]venue.FundingPayment, error) {
	if !account.ValidOwner(owner) || asset == "" || since.IsZero() || until.Before(since) || until.Sub(since) > 7*24*time.Hour || r.markets == nil {
		return nil, errors.New("invalid Phoenix funding window/source")
	}
	symbol := ""
	for _, market := range r.markets.Snapshot().Metadata.Markets {
		if market.Asset == asset {
			if symbol != "" && symbol != market.Symbol {
				return nil, errors.New("ambiguous Phoenix funding market")
			}
			symbol = market.Symbol
		}
	}
	if symbol == "" {
		return nil, errors.New("Phoenix funding native market unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	query := url.Values{"traderPdaIndex": {"0"}, "symbol": {symbol}, "startTime": {strconv.FormatInt(since.UnixMilli(), 10)}, "endTime": {strconv.FormatInt(until.UnixMilli(), 10)}, "limit": {"100"}}
	seenCursors := map[string]bool{}
	seen := map[string]string{}
	result := []venue.FundingPayment{}
	for range 4 {
		r.mu.Lock()
		blocked := time.Now().Before(r.retryAt)
		r.mu.Unlock()
		if blocked {
			return nil, errors.New("Phoenix funding reads backing off")
		}
		read, cancelRead := context.WithTimeout(ctx, 5*time.Second)
		req, err := http.NewRequestWithContext(read, http.MethodGet, r.base+"/v1/trader/"+url.PathEscape(owner)+"/funding-history?"+query.Encode(), nil)
		if err != nil {
			cancelRead()
			return nil, errors.New("invalid Phoenix funding endpoint")
		}
		res, err := r.http.Do(req)
		if err != nil {
			cancelRead()
			return nil, errors.New("Phoenix funding history unavailable")
		}
		if res.StatusCode == 429 {
			delay := 5 * time.Second
			if seconds, err := strconv.ParseUint(res.Header.Get("Retry-After"), 10, 32); err == nil {
				delay = max(delay, time.Duration(seconds)*time.Second)
			} else if at, err := http.ParseTime(res.Header.Get("Retry-After")); err == nil {
				delay = max(delay, time.Until(at))
			}
			r.mu.Lock()
			r.retryAt = time.Now().Add(delay)
			r.mu.Unlock()
		}
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
		_ = res.Body.Close()
		cancelRead()
		if res.StatusCode != 200 || readErr != nil || len(raw) > 1<<20 {
			return nil, errors.New("Phoenix funding history response unavailable/oversized")
		}
		var page response
		if err := json.Unmarshal(raw, &page); err != nil || page.Events == nil || page.HasMore == nil || len(page.Events) > 100 {
			return nil, errors.New("invalid Phoenix settlement history contract")
		}
		if (page.Authority != nil && *page.Authority != owner) || (page.PDA != nil && *page.PDA != 0) {
			return nil, errors.New("Phoenix funding owner scope mismatch")
		}
		for _, row := range page.Events {
			if row.Symbol != symbol {
				continue
			} // An account-wide response cannot be attached to one symbol.
			if (row.PDA != nil && *row.PDA != 0) || (row.Subaccount != nil && *row.Subaccount != 0) {
				return nil, errors.New("Phoenix funding cross scope mismatch")
			}
			// Rise types::trader_http::FundingHistoryEvent accepts RFC3339 and
			// explicitly rejects integer hourly timestamps. No resolution override.
			paid, err := time.Parse(time.RFC3339Nano, row.Timestamp)
			if err != nil {
				return nil, errors.New("Phoenix funding row is not a settlement timestamp")
			}
			if paid.Before(since) || paid.After(until) {
				continue
			}
			amount, err := decimal(row.Payment)
			if err != nil {
				return nil, err
			}
			rate, err := decimal(row.Rate)
			if err != nil {
				return nil, err
			}
			size, err := decimal(row.Size)
			if err != nil || size.Sign() < 0 {
				return nil, errors.New("invalid Phoenix funding position size")
			}
			side := strings.ToLower(row.Side)
			if side != "long" && side != "short" {
				return nil, errors.New("invalid Phoenix funding position side")
			}
			identity := owner + "\x00" + symbol + "\x00" + paid.UTC().Format(time.RFC3339Nano) + "\x00" + side + "\x00" + size.RatString()
			value := amount.RatString() + "\x00" + rate.RatString()
			if prior, ok := seen[identity]; ok {
				if prior != value {
					return nil, errors.New("conflicting Phoenix funding settlement duplicate")
				}
				continue
			}
			seen[identity] = value
			usd, _ := amount.Float64()
			if math.IsInf(usd, 0) || math.IsNaN(usd) || (usd == 0 && amount.Sign() != 0) {
				return nil, errors.New("unrepresentable Phoenix funding amount")
			}
			hash := sha256.Sum256([]byte(identity))
			result = append(result, venue.FundingPayment{ExternalID: "phoenix-settlement:" + hex.EncodeToString(hash[:]), Venue: "phoenix", Account: owner, Asset: asset, MarketKey: symbol, AmountUSD: usd, PaidAt: paid.UTC(), PositionSize: row.Size, PositionSide: side})
		}
		if !*page.HasMore {
			return result, nil
		}
		if page.Next == nil || *page.Next == "" || len(*page.Next) > 1024 || seenCursors[*page.Next] {
			return nil, errors.New("invalid Phoenix funding cursor")
		}
		seenCursors[*page.Next] = true
		query.Set("cursor", *page.Next)
		query.Del("startTime") // SDK cursor/startTime are mutually exclusive.
	}
	return nil, errors.New("Phoenix funding coverage exceeds bounded pagination")
}
