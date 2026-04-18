package api

import "net/http"

type breakEvenBlock struct {
	AvgEstimatedHours float64 `json:"avg_estimated_break_even_hours"`
	ReachedCount      int64   `json:"reached_count"`
	NotReachedCount   int64   `json:"not_reached_count"`
	ReachedRate       float64 `json:"reached_rate"`
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	default:
		return 0
	}
}

func (s *Server) handlePaperAnalytics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := s.store.Queries()

	var warnings []string

	// Summary is required — fail if it errors
	summary, err := q.AnalyticsSummary(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Grouped blocks are optional — collect what succeeds
	byAsset, err := q.AnalyticsByAsset(ctx)
	if err != nil {
		warnings = append(warnings, "by_asset: "+err.Error())
	}

	byRiskTier, err := q.AnalyticsByRiskTier(ctx)
	if err != nil {
		warnings = append(warnings, "by_risk_tier: "+err.Error())
	}

	byCloseReason, err := q.AnalyticsByCloseReason(ctx)
	if err != nil {
		warnings = append(warnings, "by_close_reason: "+err.Error())
	}

	// Break-even rate: based on closed trades only.
	// break_even_reached means the position's funding carry exceeded entry costs
	// at least once during its lifetime — NOT the same as closing profitably.
	beTotal := summary.BreakEvenReachedCount + summary.BreakEvenNotReachedCount
	var beRate float64
	if beTotal > 0 {
		beRate = float64(summary.BreakEvenReachedCount) / float64(beTotal)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"mode":     "paper",
		"partial":  len(warnings) > 0,
		"warnings": warnings,
		"summary": map[string]any{
			"total_trades":        summary.TotalTrades,
			"closed_trades":       summary.ClosedTrades,
			"open_trades":         summary.OpenTrades,
			"failed_trades":       summary.FailedTrades,
			"profitable_trades":   summary.ProfitableTrades,
			"unprofitable_trades": summary.UnprofitableTrades,
			"pnl": map[string]any{
				"price_pnl":      toFloat(summary.TotalPricePnl),
				"funding_pnl":    toFloat(summary.TotalFundingPnl),
				"total_pnl":      toFloat(summary.TotalPnl),
				"realized_pnl":   toFloat(summary.TotalRealizedPnl),
				"unrealized_pnl": toFloat(summary.TotalUnrealizedPnl),
			},
			"avg_hold_hours": toFloat(summary.AvgHoldHours),
			"break_even": breakEvenBlock{
				AvgEstimatedHours: toFloat(summary.AvgEstBreakEvenHours),
				ReachedCount:      summary.BreakEvenReachedCount,
				NotReachedCount:   summary.BreakEvenNotReachedCount,
				ReachedRate:       beRate,
			},
		},
		"by_asset":        byAsset,
		"by_risk_tier":    byRiskTier,
		"by_close_reason": byCloseReason,
	})
}
