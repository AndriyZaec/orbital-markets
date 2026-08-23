package analytics

import (
	"context"
	"database/sql"
	"time"
)

type VolumeWindow struct {
	GrossVenueVolume  float64 `json:"gross_venue_volume"`
	HedgedTradeVolume float64 `json:"hedged_trade_volume"`
	OpenVolume        float64 `json:"open_volume"`
	CloseVolume       float64 `json:"close_volume"`
}

type VolumeBreakdown struct {
	Key              string  `json:"key"`
	GrossVenueVolume float64 `json:"gross_venue_volume"`
	OpenVolume       float64 `json:"open_volume"`
	CloseVolume      float64 `json:"close_volume"`
}

type TradeMetrics struct {
	OpenPositions     int64 `json:"open_positions"`
	DegradedPositions int64 `json:"degraded_positions"`
	SuccessfulOpens   int64 `json:"successful_opens"`
	FailedOpens       int64 `json:"failed_opens"`
	ClosedTrades      int64 `json:"closed_trades"`
}

type LiveMetrics struct {
	GeneratedAt string `json:"generated_at"`
	Volume      struct {
		AllTime VolumeWindow      `json:"all_time"`
		Last24h VolumeWindow      `json:"last_24h"`
		Last7d  VolumeWindow      `json:"last_7d"`
		ByVenue []VolumeBreakdown `json:"by_venue"`
		ByAsset []VolumeBreakdown `json:"by_asset"`
	} `json:"volume"`
	Trades TradeMetrics `json:"trades"`
}

func LoadLiveMetrics(ctx context.Context, db *sql.DB, now time.Time) (*LiveMetrics, error) {
	metrics := &LiveMetrics{GeneratedAt: now.UTC().Format(time.RFC3339)}
	windows := []struct {
		cutoff *time.Time
		assign func(VolumeWindow)
	}{
		{assign: func(value VolumeWindow) { metrics.Volume.AllTime = value }},
		{cutoff: timePtr(now.Add(-24 * time.Hour)), assign: func(value VolumeWindow) { metrics.Volume.Last24h = value }},
		{cutoff: timePtr(now.Add(-7 * 24 * time.Hour)), assign: func(value VolumeWindow) { metrics.Volume.Last7d = value }},
	}
	for _, window := range windows {
		value, err := queryVolumeWindow(ctx, db, window.cutoff)
		if err != nil {
			return nil, err
		}
		window.assign(value)
	}

	var err error
	metrics.Volume.ByVenue, err = queryVolumeBreakdown(ctx, db, false)
	if err != nil {
		return nil, err
	}
	metrics.Volume.ByAsset, err = queryVolumeBreakdown(ctx, db, true)
	if err != nil {
		return nil, err
	}
	metrics.Trades, err = queryTradeMetrics(ctx, db)
	if err != nil {
		return nil, err
	}
	return metrics, nil
}

func queryVolumeWindow(ctx context.Context, db *sql.DB, cutoff *time.Time) (VolumeWindow, error) {
	openFilter := "filled = 1"
	closeFilter := "resolved = 1 AND filled_amount > 0"
	args := make([]any, 0, 2)
	if cutoff != nil {
		openFilter += " AND filled_at >= ?"
		closeFilter += " AND updated_at >= ?"
		stamp := cutoff.UTC().Format(time.RFC3339)
		args = append(args, stamp, stamp)
	}

	const queryTemplate = `
WITH open_leg AS (
    SELECT position_id, leg, SUM(filled_amount * avg_fill_price) AS volume
    FROM live_fills
    WHERE %s
    GROUP BY position_id, leg
),
open_pair AS (
    SELECT position_id, MIN(volume) AS volume
    FROM open_leg
    GROUP BY position_id
    HAVING COUNT(*) >= 2
),
close_leg AS (
    SELECT position_id, leg, SUM(filled_amount * avg_fill_price) AS volume
    FROM live_close_outcomes
    WHERE %s
    GROUP BY position_id, leg
),
close_pair AS (
    SELECT position_id, MIN(volume) AS volume
    FROM close_leg
    GROUP BY position_id
    HAVING COUNT(*) >= 2
)
SELECT
    COALESCE((SELECT SUM(volume) FROM open_leg), 0),
    COALESCE((SELECT SUM(volume) FROM close_leg), 0),
    COALESCE((SELECT SUM(volume) FROM open_pair), 0),
    COALESCE((SELECT SUM(volume) FROM close_pair), 0);`
	query := formatQuery(queryTemplate, openFilter, closeFilter)

	var openVolume, closeVolume, hedgedOpen, hedgedClose float64
	if err := db.QueryRowContext(ctx, query, args...).Scan(&openVolume, &closeVolume, &hedgedOpen, &hedgedClose); err != nil {
		return VolumeWindow{}, err
	}
	return VolumeWindow{
		GrossVenueVolume:  openVolume + closeVolume,
		HedgedTradeVolume: hedgedOpen + hedgedClose,
		OpenVolume:        openVolume,
		CloseVolume:       closeVolume,
	}, nil
}

func queryVolumeBreakdown(ctx context.Context, db *sql.DB, byAsset bool) ([]VolumeBreakdown, error) {
	key := "f.venue"
	closeKey := "c.venue"
	joinOpen := ""
	joinClose := ""
	if byAsset {
		key = "p.asset"
		closeKey = "p.asset"
		joinOpen = " JOIN live_positions p ON p.id = f.position_id"
		joinClose = " JOIN live_positions p ON p.id = c.position_id"
	}
	query := formatQuery(`
WITH volume AS (
    SELECT %s AS key, SUM(f.filled_amount * f.avg_fill_price) AS open_volume, 0 AS close_volume
    FROM live_fills f%s
    WHERE f.filled = 1
    GROUP BY %s
    UNION ALL
    SELECT %s AS key, 0 AS open_volume, SUM(c.filled_amount * c.avg_fill_price) AS close_volume
    FROM live_close_outcomes c%s
    WHERE c.resolved = 1 AND c.filled_amount > 0
    GROUP BY %s
)
SELECT key, COALESCE(SUM(open_volume), 0), COALESCE(SUM(close_volume), 0)
FROM volume
GROUP BY key
ORDER BY (SUM(open_volume) + SUM(close_volume)) DESC, key;`, key, joinOpen, key, closeKey, joinClose, closeKey)

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]VolumeBreakdown, 0)
	for rows.Next() {
		var item VolumeBreakdown
		if err := rows.Scan(&item.Key, &item.OpenVolume, &item.CloseVolume); err != nil {
			return nil, err
		}
		item.GrossVenueVolume = item.OpenVolume + item.CloseVolume
		result = append(result, item)
	}
	return result, rows.Err()
}

func queryTradeMetrics(ctx context.Context, db *sql.DB) (TradeMetrics, error) {
	var metrics TradeMetrics
	err := db.QueryRowContext(ctx, `
SELECT
    COUNT(CASE WHEN state IN ('open', 'closing') THEN 1 END),
    COUNT(CASE WHEN state = 'degraded' THEN 1 END),
    COUNT(CASE WHEN opened_at IS NOT NULL THEN 1 END),
    COUNT(CASE WHEN state = 'failed' THEN 1 END),
    COUNT(CASE WHEN state = 'closed' THEN 1 END)
FROM live_positions;`).Scan(
		&metrics.OpenPositions,
		&metrics.DegradedPositions,
		&metrics.SuccessfulOpens,
		&metrics.FailedOpens,
		&metrics.ClosedTrades,
	)
	return metrics, err
}

func formatQuery(template string, args ...any) string {
	result := template
	for _, arg := range args {
		value, ok := arg.(string)
		if !ok {
			continue
		}
		result = replaceFirst(result, "%s", value)
	}
	return result
}

func replaceFirst(value, old, replacement string) string {
	for i := 0; i+len(old) <= len(value); i++ {
		if value[i:i+len(old)] == old {
			return value[:i] + replacement + value[i+len(old):]
		}
	}
	return value
}

func timePtr(value time.Time) *time.Time {
	return &value
}
