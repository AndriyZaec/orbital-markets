package executor

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

const (
	minHedgeableFillPct = 0.50
	maxHedgeMismatchPct = 0.05
	maxWaitBetweenLegs  = 5 * time.Second
)

// LiveExecutor orchestrates real two-leg spread execution across venues.
type LiveExecutor struct {
	venues map[string]venue.VenueClient // keyed by venue name
	logger *slog.Logger
}

func NewLiveExecutor(logger *slog.Logger, venues ...venue.VenueClient) *LiveExecutor {
	m := make(map[string]venue.VenueClient, len(venues))
	for _, v := range venues {
		m[v.Name()] = v
	}
	return &LiveExecutor{
		venues: m,
		logger: logger,
	}
}

// Execute runs the full two-leg live execution loop.
//
// Flow:
//  1. Run live admission gate
//  2. Determine riskier leg (submit first)
//  3. Submit leg 1, wait for fill
//  4. Check minimum hedgeable fill threshold
//  5. Compute leg 2 size from actual leg 1 fill
//  6. Submit leg 2 within 5s deadline
//  7. Check hedge mismatch
//  8. On failure: retry once → unwind leg 1 → degraded
func (e *LiveExecutor) Execute(
	ctx context.Context,
	opp domain.Opportunity,
	plan *domain.ExecutionPlan,
) *ExecutionResult {
	started := time.Now()

	result := &ExecutionResult{
		OpportunityID: opp.ID,
		PlanID:        plan.ID,
		Asset:         opp.Asset,
		State:         ExecStateFailed,
		StartedAt:     started,
	}

	// 1. Admission gate
	admission := domain.CheckLiveAdmission(opp, plan.Leverage.Leverage)
	if !admission.Allowed {
		result.Reasons = append(result.Reasons, admission.Reasons...)
		result.CompletedAt = time.Now()
		e.logger.Warn("live executor: admission denied",
			"asset", opp.Asset,
			"reasons", admission.Reasons,
		)
		return result
	}

	// 2. Determine legs and riskier-first ordering
	leg1Params, leg2Params := e.orderLegs(plan)

	client1, ok := e.venues[leg1Params.venueName]
	if !ok {
		result.Reasons = append(result.Reasons, fmt.Sprintf("no client for venue: %s", leg1Params.venueName))
		result.CompletedAt = time.Now()
		return result
	}
	client2, ok := e.venues[leg2Params.venueName]
	if !ok {
		result.Reasons = append(result.Reasons, fmt.Sprintf("no client for venue: %s", leg2Params.venueName))
		result.CompletedAt = time.Now()
		return result
	}

	// 3. Submit leg 1
	e.logger.Info("live executor: submitting leg 1",
		"venue", leg1Params.venueName,
		"symbol", leg1Params.open.Symbol,
		"side", leg1Params.open.Side,
		"amount", leg1Params.open.Amount,
	)

	leg1OrderID := fmt.Sprintf("orbital-leg1-%d", time.Now().UnixNano())
	leg1Params.open.ClientOrderID = leg1OrderID

	submitResult1, err := client1.SubmitOpen(ctx, leg1Params.open)
	result.Leg1 = toLegResult(leg1Params, submitResult1, err)

	if err != nil || submitResult1 == nil || !submitResult1.Accepted {
		result.Reasons = append(result.Reasons, "leg 1 submit failed")
		result.CompletedAt = time.Now()
		e.logger.Error("live executor: leg 1 submit failed",
			"venue", leg1Params.venueName,
			"err", errOrSubmitErr(err, submitResult1),
		)
		return result
	}

	// Wait for leg 1 fill
	fill1, err := client1.WaitForFill(ctx, leg1OrderID)
	if err != nil || fill1 == nil {
		result.Reasons = append(result.Reasons, "leg 1 fill wait failed")
		result.CompletedAt = time.Now()
		return result
	}

	result.Leg1 = mergeFill(result.Leg1, fill1)

	// 4. Check minimum hedgeable fill
	fillRatio := 0.0
	if leg1Params.open.Amount > 0 {
		fillRatio = fill1.FilledAmount / leg1Params.open.Amount
	}
	result.Leg1.FillRatio = fillRatio

	if fillRatio < minHedgeableFillPct {
		result.Reasons = append(result.Reasons,
			fmt.Sprintf("leg 1 fill ratio %.1f%% below 50%% threshold", fillRatio*100))
		result.CompletedAt = time.Now()

		e.logger.Warn("live executor: leg 1 underfilled, aborting",
			"fill_ratio", fillRatio,
		)

		// Try to unwind leg 1 if anything filled
		if fill1.FilledAmount > 0 {
			e.unwindLeg(ctx, client1, leg1Params, fill1.FilledAmount, result)
		}
		return result
	}

	e.logger.Info("live executor: leg 1 filled",
		"venue", leg1Params.venueName,
		"filled", fill1.FilledAmount,
		"ratio", fmt.Sprintf("%.1f%%", fillRatio*100),
		"avg_price", fill1.AvgFillPrice,
	)

	// 5. Compute leg 2 target from actual leg 1 fill
	leg2Params.open.Amount = fill1.FilledAmount

	// 6. Submit leg 2 within 5s deadline
	leg2Ctx, leg2Cancel := context.WithTimeout(ctx, maxWaitBetweenLegs)
	defer leg2Cancel()

	leg2Result := e.submitAndConfirmLeg2(leg2Ctx, client2, leg2Params, result)

	// 7. Check hedge mismatch
	if leg2Result.filled {
		mismatch := math.Abs(leg2Result.filledAmount-leg2Params.open.Amount) / leg2Params.open.Amount
		if mismatch <= maxHedgeMismatchPct {
			// Success
			result.State = ExecStateOpen
			result.CompletedAt = time.Now()
			e.logger.Info("live executor: hedge open",
				"asset", opp.Asset,
				"mismatch", fmt.Sprintf("%.2f%%", mismatch*100),
			)
			return result
		}

		// Mismatch too high — retry once
		e.logger.Warn("live executor: hedge mismatch too high, retrying",
			"mismatch", fmt.Sprintf("%.2f%%", mismatch*100),
		)
		result.Recovery = append(result.Recovery, RecoveryAction{
			Action: "retry_leg2",
			Detail: fmt.Sprintf("mismatch %.2f%%", mismatch*100),
		})

		// Remaining amount
		remaining := leg2Params.open.Amount - leg2Result.filledAmount
		if remaining > 0 {
			leg2Params.open.Amount = remaining
			retryCtx, retryCancel := context.WithTimeout(ctx, maxWaitBetweenLegs)
			defer retryCancel()
			retryResult := e.submitAndConfirmLeg2(retryCtx, client2, leg2Params, result)

			if retryResult.filled {
				totalFilled := leg2Result.filledAmount + retryResult.filledAmount
				totalMismatch := math.Abs(totalFilled-fill1.FilledAmount) / fill1.FilledAmount
				if totalMismatch <= maxHedgeMismatchPct {
					result.State = ExecStateOpen
					result.Recovery[len(result.Recovery)-1].Success = true
					result.CompletedAt = time.Now()
					return result
				}
			}
		}
	}

	// 8. Recovery failed — unwind leg 1
	e.logger.Warn("live executor: leg 2 failed, unwinding leg 1",
		"asset", opp.Asset,
	)
	e.unwindLeg(ctx, client1, leg1Params, fill1.FilledAmount, result)
	result.State = ExecStateDegraded
	result.Reasons = append(result.Reasons, "hedge could not be established")
	result.CompletedAt = time.Now()
	return result
}

// legParams bundles the open/close params and venue name for one leg.
type legParams struct {
	venueName string
	open      venue.OpenParams
	close     venue.CloseParams
}

type leg2Outcome struct {
	filled       bool
	filledAmount float64
}

func (e *LiveExecutor) submitAndConfirmLeg2(
	ctx context.Context,
	client venue.VenueClient,
	params legParams,
	result *ExecutionResult,
) leg2Outcome {
	leg2OrderID := fmt.Sprintf("orbital-leg2-%d", time.Now().UnixNano())
	params.open.ClientOrderID = leg2OrderID

	e.logger.Info("live executor: submitting leg 2",
		"venue", params.venueName,
		"symbol", params.open.Symbol,
		"side", params.open.Side,
		"amount", params.open.Amount,
	)

	submitResult2, err := client.SubmitOpen(ctx, params.open)
	result.Leg2 = toLegResult(params, submitResult2, err)

	if err != nil || submitResult2 == nil || !submitResult2.Accepted {
		result.Reasons = append(result.Reasons, "leg 2 submit failed")
		e.logger.Error("live executor: leg 2 submit failed",
			"venue", params.venueName,
			"err", errOrSubmitErr(err, submitResult2),
		)
		return leg2Outcome{}
	}

	fill2, err := client.WaitForFill(ctx, leg2OrderID)
	if err != nil || fill2 == nil {
		result.Reasons = append(result.Reasons, "leg 2 fill wait failed")
		return leg2Outcome{}
	}

	result.Leg2 = mergeFill(result.Leg2, fill2)
	result.Leg2.FillRatio = fill2.FilledAmount / params.open.Amount

	return leg2Outcome{
		filled:       fill2.Status == venue.OrderStatusFilled || fill2.Status == venue.OrderStatusPartialFill,
		filledAmount: fill2.FilledAmount,
	}
}

func (e *LiveExecutor) unwindLeg(
	ctx context.Context,
	client venue.VenueClient,
	params legParams,
	amount float64,
	result *ExecutionResult,
) {
	unwindID := fmt.Sprintf("orbital-unwind-%d", time.Now().UnixNano())

	e.logger.Info("live executor: unwinding leg",
		"venue", params.venueName,
		"symbol", params.open.Symbol,
		"amount", amount,
	)

	closeParams := params.close
	closeParams.Amount = amount
	closeParams.ClientOrderID = unwindID

	submitResult, err := client.SubmitClose(ctx, closeParams)
	action := RecoveryAction{Action: "unwind_leg1"}

	if err != nil || submitResult == nil || !submitResult.Accepted {
		action.Success = false
		action.Detail = fmt.Sprintf("unwind submit failed: %s", errOrSubmitErr(err, submitResult))
		result.Recovery = append(result.Recovery, action)
		return
	}

	fill, err := client.WaitForFill(ctx, unwindID)
	if err != nil || fill == nil {
		action.Success = false
		action.Detail = "unwind fill wait failed"
		result.Recovery = append(result.Recovery, action)
		return
	}

	action.Success = fill.Status == venue.OrderStatusFilled
	action.Detail = fmt.Sprintf("unwound %.4f of %.4f", fill.FilledAmount, amount)
	result.Recovery = append(result.Recovery, action)
}

// orderLegs determines which leg is riskier and should go first.
// Riskier = thinner depth (higher spread) or smaller venue.
// For v1: the venue with wider spread gets submitted first.
func (e *LiveExecutor) orderLegs(plan *domain.ExecutionPlan) (legParams, legParams) {
	leg1 := legParams{
		venueName: plan.Leg1.Venue,
		open: venue.OpenParams{
			Symbol:         plan.Leg1.Asset,
			Side:           plan.Leg1.Side,
			Amount:         plan.Notional,
			Price:          plan.Leg1.ExpectedPrice,
			Leverage:       plan.Leverage.Leverage,
			MarginRequired: plan.Leverage.MarginRequired / 2,
		},
		close: venue.CloseParams{
			Symbol:       plan.Leg1.Asset,
			PositionSide: plan.Leg1.Side,
			Price:        plan.Leg1.ExpectedPrice,
		},
	}
	leg2 := legParams{
		venueName: plan.Leg2.Venue,
		open: venue.OpenParams{
			Symbol:         plan.Leg2.Asset,
			Side:           plan.Leg2.Side,
			Amount:         plan.Notional,
			Price:          plan.Leg2.ExpectedPrice,
			Leverage:       plan.Leverage.Leverage,
			MarginRequired: plan.Leverage.MarginRequired / 2,
		},
		close: venue.CloseParams{
			Symbol:       plan.Leg2.Asset,
			PositionSide: plan.Leg2.Side,
			Price:        plan.Leg2.ExpectedPrice,
		},
	}

	// Riskier leg first: higher slippage estimate = thinner book
	if plan.Leg1.Slippage >= plan.Leg2.Slippage {
		return leg1, leg2
	}
	return leg2, leg1
}

func toLegResult(params legParams, submit *venue.SubmitResult, err error) LegResult {
	r := LegResult{
		Venue:        params.venueName,
		Symbol:       params.open.Symbol,
		Side:         string(params.open.Side),
		RequestedAmt: params.open.Amount,
	}
	if err != nil {
		r.Error = err.Error()
		return r
	}
	if submit != nil {
		r.Submitted = true
		r.Accepted = submit.Accepted
		r.OrderID = submit.OrderID
		r.ClientOrderID = submit.ClientOrderID
		r.Error = submit.Error
	}
	return r
}

func mergeFill(r LegResult, fill *venue.FillResult) LegResult {
	if fill == nil {
		return r
	}
	r.Filled = fill.Status == venue.OrderStatusFilled
	r.FilledAmount = fill.FilledAmount
	r.AvgFillPrice = fill.AvgFillPrice
	r.Fee = fill.TotalFee
	if fill.Error != "" {
		r.Error = fill.Error
	}
	return r
}

func errOrSubmitErr(err error, submit *venue.SubmitResult) string {
	if err != nil {
		return err.Error()
	}
	if submit != nil {
		return submit.Error
	}
	return "unknown"
}
