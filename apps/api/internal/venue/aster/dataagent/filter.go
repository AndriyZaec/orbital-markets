package dataagent

import (
	"context"
	"strings"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
	asteraccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/account"
)

type Probe interface {
	Prepare(context.Context, string, string) (Prepared, error)
	Authorize(context.Context, string, string, string, string, Approval) error
	Validate(context.Context, string, string, string, string) (Report, error)
	Status(context.Context, string, string) (ProbeStatus, error)
	Run(context.Context, string, string) (Report, error)
	ReconcileExecutionAgent(context.Context, string, []string) (string, error)
	HasBuilderApproval(context.Context, string, string, string) (bool, error)
}

func ExcludeOwner(reader Reader, owner string) Reader {
	if reader == nil || owner == "" {
		return reader
	}
	return excludedOwnerReader{Reader: reader, owner: owner}
}

type excludedOwnerReader struct {
	Reader
	owner string
}

func ExcludeProbeOwner(probe Probe, owner string) Probe {
	if probe == nil || owner == "" {
		return probe
	}
	return excludedOwnerProbe{Probe: probe, owner: owner}
}

type excludedOwnerProbe struct {
	Probe
	owner string
}

func (p excludedOwnerProbe) denied(owner string) bool {
	return strings.EqualFold(owner, p.owner)
}

func (p excludedOwnerProbe) Prepare(ctx context.Context, owner, executionAgent string) (Prepared, error) {
	if p.denied(owner) {
		return Prepared{}, ErrNotApproved
	}
	return p.Probe.Prepare(ctx, owner, executionAgent)
}

func (p excludedOwnerProbe) Authorize(ctx context.Context, probeID, signature, owner, executionAgent string, approval Approval) error {
	if p.denied(owner) {
		return ErrNotApproved
	}
	return p.Probe.Authorize(ctx, probeID, signature, owner, executionAgent, approval)
}

func (p excludedOwnerProbe) Validate(ctx context.Context, probeID, signature, owner, executionAgent string) (Report, error) {
	if p.denied(owner) {
		return Report{}, ErrNotApproved
	}
	return p.Probe.Validate(ctx, probeID, signature, owner, executionAgent)
}

func (p excludedOwnerProbe) Status(ctx context.Context, owner, executionAgent string) (ProbeStatus, error) {
	if p.denied(owner) {
		return ProbeStatus{}, ErrNotApproved
	}
	return p.Probe.Status(ctx, owner, executionAgent)
}

func (p excludedOwnerProbe) Run(ctx context.Context, owner, executionAgent string) (Report, error) {
	if p.denied(owner) {
		return Report{}, ErrNotApproved
	}
	return p.Probe.Run(ctx, owner, executionAgent)
}

func (p excludedOwnerProbe) ReconcileExecutionAgent(ctx context.Context, owner string, candidates []string) (string, error) {
	if p.denied(owner) {
		return "", ErrNotApproved
	}
	return p.Probe.ReconcileExecutionAgent(ctx, owner, candidates)
}

func (p excludedOwnerProbe) HasBuilderApproval(ctx context.Context, owner, builder, requiredFeeRate string) (bool, error) {
	if p.denied(owner) {
		return false, ErrNotApproved
	}
	return p.Probe.HasBuilderApproval(ctx, owner, builder, requiredFeeRate)
}

func (r excludedOwnerReader) ReadAccount(ctx context.Context, owner string) (AccountObservation, error) {
	if strings.EqualFold(owner, r.owner) {
		return AccountObservation{}, ErrNotApproved
	}
	return r.Reader.ReadAccount(ctx, owner)
}

func (r excludedOwnerReader) ReadLeverageBrackets(ctx context.Context, owner, symbol string) (asteraccount.LeverageBrackets, time.Time, error) {
	if strings.EqualFold(owner, r.owner) {
		return nil, time.Time{}, ErrNotApproved
	}
	return r.Reader.ReadLeverageBrackets(ctx, owner, symbol)
}

func (r excludedOwnerReader) ReadFunding(ctx context.Context, owner string, since, until time.Time) ([]venue.FundingPayment, error) {
	if strings.EqualFold(owner, r.owner) {
		return nil, ErrNotApproved
	}
	return r.Reader.ReadFunding(ctx, owner, since, until)
}

func (r excludedOwnerReader) LookupOrder(ctx context.Context, owner, symbol, clientOrderID string) (OrderStatus, error) {
	if strings.EqualFold(owner, r.owner) {
		return OrderStatus{}, ErrNotApproved
	}
	return r.Reader.LookupOrder(ctx, owner, symbol, clientOrderID)
}
