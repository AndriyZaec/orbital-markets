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

type weeklyFundingSample struct {
	weekStart string
	ticker    string
	venueA    string
	venueB    string
	rateA     float64
	rateB     float64
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
	rows, err := db.QueryContext(ctx, `
		SELECT a.asset, a.venue, b.venue, a.bucket_unix, a.funding_avg, b.funding_avg
		FROM market_snapshots_1h a
		JOIN market_snapshots_1h b
		  ON b.asset = a.asset
		 AND b.bucket_unix = a.bucket_unix
		 AND b.venue > a.venue
		WHERE a.bucket_unix >= ? AND a.bucket_unix <= ?
		ORDER BY a.bucket_unix, a.asset, a.venue, b.venue`, start.Unix(), now.UTC().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	samples := make([]weeklyFundingSample, 0)
	for rows.Next() {
		var sample weeklyFundingSample
		var bucketUnix int64
		if err := rows.Scan(&sample.ticker, &sample.venueA, &sample.venueB, &bucketUnix, &sample.rateA, &sample.rateB); err != nil {
			return nil, err
		}
		if math.IsNaN(sample.rateA) || math.IsNaN(sample.rateB) || math.IsInf(sample.rateA, 0) || math.IsInf(sample.rateB, 0) {
			continue
		}
		sample.weekStart = startOfUTCWeek(time.Unix(bucketUnix, 0)).Format("2006-01-02")
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	report.Rows = aggregateWeeklyAPR(samples)
	return report, nil
}

func aggregateWeeklyAPR(samples []weeklyFundingSample) []WeeklyAPRRow {
	groups := make(map[weeklyAPRKey]weeklyAPRAccumulator)
	for _, sample := range samples {
		key := weeklyAPRKey{
			weekStart: sample.weekStart,
			ticker:    sample.ticker,
			venueA:    sample.venueA,
			venueB:    sample.venueB,
		}
		accumulator := groups[key]
		spread := sample.rateA - sample.rateB
		if accumulator.samples == 0 || math.Abs(spread) > math.Abs(accumulator.peakSpread) {
			accumulator.peakSpread = spread
		}
		accumulator.spreadSum += spread
		accumulator.samples++
		groups[key] = accumulator
	}

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
