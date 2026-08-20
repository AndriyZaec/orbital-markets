package scanner

import "github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"

const (
	signalAPRThreshold       = 0.01
	signalMinimumSamples     = 24
	signalActiveShare        = 0.20
	signalPersistentShare    = 0.60
	signalDirectionThreshold = 0.70
)

type OpportunitySignalStatus string

const (
	SignalPersistent   OpportunitySignalStatus = "persistent"
	SignalIntermittent OpportunitySignalStatus = "intermittent"
	SignalNew          OpportunitySignalStatus = "new"
	SignalChoppy       OpportunitySignalStatus = "choppy"
	SignalReversed     OpportunitySignalStatus = "reversed"
	SignalFaded        OpportunitySignalStatus = "faded"
	SignalFlat         OpportunitySignalStatus = "flat"
	SignalLimited      OpportunitySignalStatus = "limited"
)

type OpportunitySignal struct {
	Status               OpportunitySignalStatus `json:"status"`
	Activity             float64                 `json:"activity"`
	DirectionConsistency float64                 `json:"direction_consistency"`
	AverageEdge          float64                 `json:"average_edge"`
	Samples              int                     `json:"samples"`
}

type SignalFundingRow struct {
	Venue       string
	Asset       string
	BucketUnix  int64
	FundingRate float64
}

type signalStats struct {
	Samples   int
	Active    int
	LongA     int
	LongB     int
	TotalEdge float64
}

type signalSeriesKey struct {
	Asset string
	Venue string
}

func CalculateOpportunitySignals(opportunities []domain.Opportunity, rows []SignalFundingRow) map[string]OpportunitySignal {
	series := make(map[signalSeriesKey]map[int64]float64)
	for _, row := range rows {
		key := signalSeriesKey{Asset: row.Asset, Venue: row.Venue}
		if series[key] == nil {
			series[key] = make(map[int64]float64)
		}
		series[key][row.BucketUnix] = row.FundingRate
	}

	signals := make(map[string]OpportunitySignal)
	for _, opportunity := range opportunities {
		ratesA := series[signalSeriesKey{Asset: opportunity.Asset, Venue: opportunity.VenuePair.VenueA}]
		ratesB := series[signalSeriesKey{Asset: opportunity.Asset, Venue: opportunity.VenuePair.VenueB}]
		stats := signalStats{}
		for bucket, rateA := range ratesA {
			rateB, ok := ratesB[bucket]
			if !ok {
				continue
			}
			spread := rateA - rateB
			edge := spread
			if edge < 0 {
				edge = -edge
			}
			stats.Samples++
			stats.TotalEdge += edge
			if domain.AnnualizeRate(edge) < signalAPRThreshold {
				continue
			}
			stats.Active++
			if spread > 0 {
				stats.LongB++
			} else {
				stats.LongA++
			}
		}
		if stats.Samples > 0 {
			signals[opportunity.ID] = classifyOpportunitySignal(opportunity, stats)
		}
	}
	return signals
}

func classifyOpportunitySignal(opportunity domain.Opportunity, stats signalStats) OpportunitySignal {
	activity := float64(stats.Active) / float64(stats.Samples)
	consistency := 0.0
	dominantDirection := domain.Direction("")
	if stats.Active > 0 {
		dominant := stats.LongA
		dominantDirection = domain.DirectionLongA
		if stats.LongB > dominant {
			dominant = stats.LongB
			dominantDirection = domain.DirectionLongB
		}
		consistency = float64(dominant) / float64(stats.Active)
	}

	status := SignalFlat
	currentActive := opportunity.AnnualizedGrossEdge >= signalAPRThreshold
	switch {
	case stats.Samples < signalMinimumSamples:
		status = SignalLimited
	case !currentActive && activity >= signalActiveShare:
		status = SignalFaded
	case !currentActive:
		status = SignalFlat
	case activity < signalActiveShare:
		status = SignalNew
	case consistency < signalDirectionThreshold:
		status = SignalChoppy
	case dominantDirection != opportunity.Direction:
		status = SignalReversed
	case activity >= signalPersistentShare:
		status = SignalPersistent
	default:
		status = SignalIntermittent
	}

	return OpportunitySignal{
		Status:               status,
		Activity:             activity,
		DirectionConsistency: consistency,
		AverageEdge:          domain.AnnualizeRate(stats.TotalEdge / float64(stats.Samples)),
		Samples:              stats.Samples,
	}
}
