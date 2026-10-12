package liquidity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

var rateField = regexp.MustCompile(`(?:^|;)\s*([rqt])=(\d+)`)

func rateNumber(header, field string) int {
	for _, match := range rateField.FindAllStringSubmatch(header, -1) {
		if match[1] == field {
			n, _ := strconv.Atoi(match[2])
			return n
		}
	}
	return 0
}

func (s *Service) observeLimits(name string, response *http.Response) {
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusTeapot {
		delay := time.Minute
		if response.StatusCode == http.StatusTeapot {
			delay = 2 * time.Minute
		}
		if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 {
			delay = max(delay, time.Duration(seconds)*time.Second)
		} else if at, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil {
			delay = max(delay, time.Until(at))
		}
		s.pause(name, delay)
		return
	}
	if name == "pacifica" && response.Header.Get("ratelimit") != "" {
		remaining := rateNumber(response.Header.Get("ratelimit"), "r")
		quota := rateNumber(response.Header.Get("ratelimit-policy"), "q")
		// Header values are in tenths of credits. Stop optional reads when
		// remaining quota reaches half; other modules share this IP quota.
		if quota > 0 && remaining <= quota/2 {
			s.pause(name, time.Minute)
		}
	}
	if name == "aster" {
		used, _ := strconv.Atoi(response.Header.Get("X-MBX-USED-WEIGHT-1M"))
		if used >= 1200 {
			s.pause(name, time.Minute)
		}
	}
}

func (s *Service) fetchBook(ctx context.Context, market Market) (book, error) {
	q := s.quotas[market.Venue]
	select {
	case q.slot <- struct{}{}:
		defer func() { <-q.slot }()
	case <-ctx.Done():
		return book{}, ctx.Err()
	}
	if !s.allow(market.Venue, time.Now()) {
		return book{}, ErrUnavailable
	}
	method, path := http.MethodGet, ""
	var body []byte
	switch market.Venue {
	case "hyperliquid":
		method, path = http.MethodPost, "/info"
		body, _ = json.Marshal(map[string]string{"type": "l2Book", "coin": market.Key})
	case "pacifica":
		path = "/api/v1/book?" + url.Values{"symbol": {market.Key}, "agg_level": {"1"}}.Encode()
	case "aster":
		path = "/fapi/v3/depth?" + url.Values{"symbol": {market.Key}, "limit": {"50"}}.Encode()
	default:
		return book{}, ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, method, s.urls[market.Venue]+path, bytes.NewReader(body))
	if err != nil {
		return book{}, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := s.client.Do(req)
	if err != nil {
		return book{}, err
	}
	defer response.Body.Close()
	s.observeLimits(market.Venue, response)
	if response.StatusCode != http.StatusOK {
		return book{}, fmt.Errorf("%s book HTTP %d", market.Venue, response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return book{}, err
	}
	return decodeRESTBook(market.Venue, market.Key, raw)
}

func decimalLevel(price, amount string) (level, error) {
	p, err := strconv.ParseFloat(price, 64)
	if err != nil {
		return level{}, err
	}
	q, err := strconv.ParseFloat(amount, 64)
	if err != nil {
		return level{}, err
	}
	if !finitePositive(p) || !finitePositive(q) {
		return level{}, ErrUnavailable
	}
	return level{p, q}, nil
}

func decodeRESTBook(name, key string, raw []byte) (book, error) {
	var result book
	switch name {
	case "aster":
		var data struct {
			Time int64      `json:"E"`
			Bids [][]string `json:"bids"`
			Asks [][]string `json:"asks"`
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			return book{}, err
		}
		result.at = time.UnixMilli(data.Time)
		for i, rows := range [][][]string{data.Bids, data.Asks} {
			var parsed []level
			for _, row := range rows {
				if len(row) != 2 {
					return book{}, ErrUnavailable
				}
				next, err := decimalLevel(row[0], row[1])
				if err != nil {
					return book{}, err
				}
				parsed = append(parsed, next)
			}
			if i == 0 {
				result.bids = parsed
			} else {
				result.asks = parsed
			}
		}
	case "hyperliquid":
		var data struct {
			Coin   string `json:"coin"`
			Time   int64  `json:"time"`
			Levels [][]struct {
				Price    string `json:"px"`
				Quantity string `json:"sz"`
			} `json:"levels"`
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			return book{}, err
		}
		if data.Coin != key || len(data.Levels) != 2 {
			return book{}, ErrUnavailable
		}
		result.at = time.UnixMilli(data.Time)
		for i, rows := range data.Levels {
			var parsed []level
			for _, row := range rows {
				next, err := decimalLevel(row.Price, row.Quantity)
				if err != nil {
					return book{}, err
				}
				parsed = append(parsed, next)
			}
			if i == 0 {
				result.bids = parsed
			} else {
				result.asks = parsed
			}
		}
	case "pacifica":
		var data struct {
			Success bool `json:"success"`
			Data    struct {
				Symbol string `json:"s"`
				Time   int64  `json:"t"`
				Levels [][]struct {
					Price    string `json:"p"`
					Quantity string `json:"a"`
				} `json:"l"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			return book{}, err
		}
		if !data.Success || data.Data.Symbol != key || len(data.Data.Levels) != 2 {
			return book{}, ErrUnavailable
		}
		result.at = time.UnixMilli(data.Data.Time)
		for i, rows := range data.Data.Levels {
			var parsed []level
			for _, row := range rows {
				next, err := decimalLevel(row.Price, row.Quantity)
				if err != nil {
					return book{}, err
				}
				parsed = append(parsed, next)
			}
			if i == 0 {
				result.bids = parsed
			} else {
				result.asks = parsed
			}
		}
	default:
		return book{}, ErrUnavailable
	}
	if !validBook(result, time.Now()) {
		return book{}, ErrUnavailable
	}
	return result, nil
}
