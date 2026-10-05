package analytics

import (
	"context"
	"database/sql"
	"math"
	"sort"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

type WeeklyAPRRow struct {
	WeekStart        string  `json:"week_start"`
	Ticker           string  `json:"ticker"`
	VenueLong        string  `json:"venue_long"`
	VenueShort       string  `json:"venue_short"`
	MaxAPR           float64 `json:"max_apr"`
	WeeklyAverageAPR float64 `json:"weekly_average_apr"`
}

type WeeklyAPRReport struct {
	GeneratedAt string         `json:"generated_at"`
	Rows        []WeeklyAPRRow `json:"rows"`
}

type hourlyVenueFunding struct {
	venue string
	rate  float64
}

type weeklyAPRKey struct {
	weekStart string
	ticker    string
	venueA    string
	venueB    string
}

type weeklyAPRAccumulator struct {
	peakSpread float64
	spreadSum  float64
	samples    int
}

const weeklyAPRQuery = `
	SELECT asset, venue, bucket_unix, funding_avg
	FROM market_snapshots_1h INDEXED BY idx_snapshots_1h_weekly_apr
	WHERE bucket_unix >= ? AND bucket_unix <= ?
	ORDER BY bucket_unix, asset, venue`

func LoadWeeklyAPR(ctx context.Context, db *sql.DB, now time.Time, weeks int) (*WeeklyAPRReport, error) {
	report := &WeeklyAPRReport{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Rows:        []WeeklyAPRRow{},
	}
	if weeks <= 0 {
		return report, nil
	}

	currentWeek := startOfUTCWeek(now)
	start := currentWeek.AddDate(0, 0, -7*(weeks-1))
	rows, err := db.QueryContext(ctx, weeklyAPRQuery, start.Unix(), now.UTC().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groups := make(map[weeklyAPRKey]weeklyAPRAccumulator)
	var currentAsset string
	var currentBucket int64
	var funding []hourlyVenueFunding
	flush := func() {
		weekStart := startOfUTCWeek(time.Unix(currentBucket, 0)).Format("2006-01-02")
		for i := range funding {
			for j := i + 1; j < len(funding); j++ {
				key := weeklyAPRKey{
					weekStart: weekStart,
					ticker:    currentAsset,
					venueA:    funding[i].venue,
					venueB:    funding[j].venue,
				}
				accumulateWeeklyAPR(groups, key, funding[i].rate-funding[j].rate)
			}
		}
	}
	for rows.Next() {
		var asset, venueName string
		var bucketUnix int64
		var rate float64
		if err := rows.Scan(&asset, &venueName, &bucketUnix, &rate); err != nil {
			return nil, err
		}
		if asset != currentAsset || bucketUnix != currentBucket {
			if len(funding) > 0 {
				flush()
			}
			currentAsset = asset
			currentBucket = bucketUnix
			funding = funding[:0]
		}
		if math.IsNaN(rate) || math.IsInf(rate, 0) {
			continue
		}
		funding = append(funding, hourlyVenueFunding{venue: venueName, rate: rate})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(funding) > 0 {
		flush()
	}

	report.Rows = weeklyAPRRows(groups)
	return report, nil
}

func accumulateWeeklyAPR(groups map[weeklyAPRKey]weeklyAPRAccumulator, key weeklyAPRKey, spread float64) {
	accumulator := groups[key]
	if accumulator.samples == 0 || math.Abs(spread) > math.Abs(accumulator.peakSpread) {
		accumulator.peakSpread = spread
	}
	accumulator.spreadSum += spread
	accumulator.samples++
	groups[key] = accumulator
}

func weeklyAPRRows(groups map[weeklyAPRKey]weeklyAPRAccumulator) []WeeklyAPRRow {
	result := make([]WeeklyAPRRow, 0, len(groups))
	for key, accumulator := range groups {
		averageSpread := accumulator.spreadSum / float64(accumulator.samples)
		row := WeeklyAPRRow{
			WeekStart: key.weekStart,
			Ticker:    key.ticker,
			MaxAPR:    domain.AnnualizeRate(math.Abs(accumulator.peakSpread)),
		}
		if accumulator.peakSpread > 0 {
			row.VenueLong = key.venueB
			row.VenueShort = key.venueA
			row.WeeklyAverageAPR = domain.AnnualizeRate(averageSpread)
		} else {
			row.VenueLong = key.venueA
			row.VenueShort = key.venueB
			row.WeeklyAverageAPR = domain.AnnualizeRate(-averageSpread)
		}
		result = append(result, row)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].WeekStart != result[j].WeekStart {
			return result[i].WeekStart > result[j].WeekStart
		}
		if result[i].MaxAPR != result[j].MaxAPR {
			return result[i].MaxAPR > result[j].MaxAPR
		}
		if result[i].Ticker != result[j].Ticker {
			return result[i].Ticker < result[j].Ticker
		}
		return result[i].VenueLong < result[j].VenueLong
	})
	return result
}

func startOfUTCWeek(value time.Time) time.Time {
	utc := value.UTC()
	day := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	daysSinceMonday := (int(day.Weekday()) + 6) % 7
	return day.AddDate(0, 0, -daysSinceMonday)
}
