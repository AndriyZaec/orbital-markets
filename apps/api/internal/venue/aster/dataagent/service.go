package dataagent

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/google/uuid"
)

const (
	agentName      = "OrbitalData"
	agentLifetime  = 365 * 24 * time.Hour
	approvalMaxAge = 60 * time.Second
)

var (
	addressPattern   = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	signaturePattern = regexp.MustCompile(`^0x[0-9a-fA-F]{130}$`)

	ErrInvalidInput           = errors.New("invalid Aster data-agent request")
	ErrConflict               = errors.New("an Aster data-agent probe already exists and cannot be replaced")
	ErrStale                  = errors.New("Aster data-agent approval expired without submission; probe stopped")
	ErrRejected               = errors.New("Aster rejected the read-only data-agent approval; probe stopped")
	ErrNotSent                = errors.New("Aster data-agent approval was not sent; probe stopped")
	ErrUncertain              = errors.New("Aster data-agent approval outcome is uncertain; do not retry")
	ErrNotApproved            = errors.New("Aster data-agent probe is not approved")
	ErrCredentialUnreadable   = errors.New("Aster data-agent credential cannot be decrypted; restore ASTER_DATA_AGENT_MASTER_KEY")
	ErrReadRejected           = errors.New("Aster rejected the data-agent read")
	ErrExecutionAgentMismatch = errors.New("current local Aster execution-agent authorization does not match")
	ErrUnavailable            = errors.New("Aster data-agent probe is temporarily unavailable")
	ErrApprovalRejected       = errors.New("Aster approval rejected")
	ErrApprovalNotSent        = errors.New("Aster approval not sent")
	ErrApprovalAmbiguous      = errors.New("Aster approval outcome ambiguous")
)

type repository interface {
	SavePending(context.Context, Record, int64) error
	LoadByOwner(context.Context, string) (Record, error)
	LoadMetadata(context.Context, string) (Record, error)
	LoadApprovedByOwner(context.Context, string) (Record, error)
	StatusByOwner(context.Context, string) (ProbeStatus, error)
	BeginSubmission(context.Context, string) (Record, error)
	Transition(context.Context, string, Status, Status, string, time.Time) error
	SaveAcceptedAuthorization(context.Context, Record, Status, time.Time) error
	SaveResult(context.Context, string, Report) error
}

type venueClient interface {
	Approve(context.Context, Approval, string) error
	Probe(context.Context, string, string, string, []byte, int64) Report
}

type ExecutionAuthorizationChecker interface {
	AgentAuthorizationMatches(context.Context, string, string, string) (bool, error)
}

type Service struct {
	store     repository
	venue     venueClient
	reader    dataReader
	execution ExecutionAuthorizationChecker
	now       func() time.Time
}

func ParseMasterKey(encoded string) ([]byte, error) {
	if encoded == "" {
		return nil, fmt.Errorf("ASTER_DATA_AGENT_MASTER_KEY is not configured; set it to standard base64 encoding of exactly 32 random bytes")
	}
	key, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(key) != 32 || base64.StdEncoding.EncodeToString(key) != encoded {
		return nil, fmt.Errorf("ASTER_DATA_AGENT_MASTER_KEY must be standard base64 encoding of exactly 32 bytes")
	}
	return key, nil
}

func NewService(store repository, venue venueClient, execution ExecutionAuthorizationChecker, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	reader, _ := venue.(dataReader)
	return &Service{store: store, venue: venue, reader: reader, execution: execution, now: now}
}

func (s *Service) Prepare(ctx context.Context, account, executionAgent string) (Prepared, error) {
	account, executionAgent, err := normalizeAccounts(account, executionAgent)
	if err != nil {
		return Prepared{}, err
	}
	now := s.now()
	existing, err := s.store.LoadByOwner(ctx, account)
	if err == nil {
		defer clear(existing.PrivateKey)
		if existing.Status == StatusSubmitting || existing.Status == StatusUncertain {
			return Prepared{}, stateError(existing.Status)
		}
		approval := newApproval(account, existing.AgentAddress, now)
		return Prepared{ProbeID: existing.ProbeID, Approval: approval}, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Prepared{}, publicLoadError(err)
	}
	privateKey, err := secp256k1.GeneratePrivateKeyFromRand(rand.Reader)
	if err != nil {
		return Prepared{}, ErrUnavailable
	}
	defer privateKey.Zero()
	serializedKey := privateKey.Serialize()
	defer clear(serializedKey)
	approval := newApproval(account, addressFromPrivateKey(serializedKey), now)
	record := Record{
		ProbeID: uuid.NewString(), Owner: account, ExecutionAgent: executionAgent,
		AgentAddress: approval.AgentAddress, PrivateKey: serializedKey, ApprovalNonce: approval.Nonce,
		RequestedExpiry: approval.Expired, Status: StatusPending,
	}
	if err := s.store.SavePending(ctx, record, now.Add(-approvalMaxAge).UnixMicro()); err != nil {
		if errors.Is(err, errStateConflict) {
			return Prepared{}, ErrConflict
		}
		return Prepared{}, ErrUnavailable
	}
	return Prepared{ProbeID: record.ProbeID, Approval: approval}, nil
}

// Authorize submits a read-only approval collected as part of the unified Aster
// authorization flow. Existing credentials remain untouched until Aster accepts.
func (s *Service) Authorize(
	ctx context.Context,
	probeID, signature, account, executionAgent string,
	approval Approval,
) error {
	if probeID == "" || !signaturePattern.MatchString(signature) {
		return ErrInvalidInput
	}
	account, executionAgent, err := normalizeAccounts(account, executionAgent)
	if err != nil {
		return err
	}
	record, err := s.store.LoadByOwner(ctx, account)
	if err != nil {
		return publicLoadError(err)
	}
	defer clear(record.PrivateKey)
	if record.ProbeID != probeID || !validAuthorizationApproval(approval, record, account, s.now()) {
		return ErrInvalidInput
	}

	storedApproval := approvalFromRecord(record)
	if record.Status == StatusApproved && record.ExecutionAgent == executionAgent &&
		approvalsEqual(approval, storedApproval) {
		return nil
	}
	if record.Status == StatusPending && approvalsEqual(approval, storedApproval) {
		submitting, err := s.store.BeginSubmission(ctx, probeID)
		if err != nil {
			return ErrConflict
		}
		if err := s.venue.Approve(ctx, approval, signature); err != nil {
			return s.recordApprovalFailure(ctx, submitting, err, s.now())
		}
		persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return s.store.Transition(persistCtx, probeID, StatusSubmitting, StatusApproved, "", s.now())
	}

	if record.Status != StatusApproved && record.Status != StatusRejected && record.Status != StatusPending {
		return stateError(record.Status)
	}
	if err := s.venue.Approve(ctx, approval, signature); err != nil {
		publicErr := publicApprovalError(err)
		if errors.Is(publicErr, ErrUncertain) {
			persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if transitionErr := s.store.Transition(
				persistCtx, record.ProbeID, record.Status, StatusUncertain, ErrUncertain.Error(), s.now(),
			); transitionErr != nil {
				return ErrUnavailable
			}
		}
		return publicErr
	}
	record.ApprovalNonce = approval.Nonce
	record.RequestedExpiry = approval.Expired
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.store.SaveAcceptedAuthorization(persistCtx, record, record.Status, s.now()); err != nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Service) Validate(ctx context.Context, probeID, signature, account, executionAgent string) (Report, error) {
	if probeID == "" || !signaturePattern.MatchString(signature) {
		return Report{}, ErrInvalidInput
	}
	account, executionAgent, err := normalizeAccounts(account, executionAgent)
	if err != nil {
		return Report{}, err
	}
	record, err := s.store.LoadMetadata(ctx, probeID)
	if err != nil {
		return Report{}, publicLoadError(err)
	}
	if record.Owner != account || record.ExecutionAgent != executionAgent {
		return Report{}, ErrInvalidInput
	}
	now := s.now()
	if now.Sub(time.UnixMicro(record.ApprovalNonce)) > approvalMaxAge {
		if err := s.store.Transition(ctx, probeID, StatusPending, StatusRejected, ErrStale.Error(), now); err != nil {
			return Report{}, stateError(record.Status)
		}
		return Report{}, ErrStale
	}
	if err := s.requireLocalExecutionAgent(ctx, record.Owner, record.ExecutionAgent); err != nil {
		return Report{}, err
	}
	submitting, err := s.store.BeginSubmission(ctx, probeID)
	if err != nil {
		latest, loadErr := s.store.LoadMetadata(ctx, probeID)
		if loadErr != nil {
			return Report{}, publicLoadError(loadErr)
		}
		return Report{}, stateError(latest.Status)
	}
	record = submitting
	approval := approvalFromRecord(record)
	if err := s.venue.Approve(ctx, approval, signature); err != nil {
		return Report{}, s.recordApprovalFailure(ctx, record, err, now)
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.store.Transition(persistCtx, probeID, StatusSubmitting, StatusApproved, "", now); err != nil {
		return Report{}, ErrUnavailable
	}
	return s.runApproved(ctx, record.Owner, record.ExecutionAgent)
}

func (s *Service) Run(ctx context.Context, account, executionAgent string) (Report, error) {
	account, executionAgent, err := normalizeAccounts(account, executionAgent)
	if err != nil {
		return Report{}, err
	}
	return s.runApproved(ctx, account, executionAgent)
}

func (s *Service) Status(ctx context.Context, account, executionAgent string) (ProbeStatus, error) {
	account, executionAgent, err := normalizeAccounts(account, executionAgent)
	if err != nil {
		return ProbeStatus{}, err
	}
	if err := s.requireLocalExecutionAgent(ctx, account, executionAgent); err != nil {
		return ProbeStatus{}, err
	}
	status, err := s.store.StatusByOwner(ctx, account)
	if err != nil {
		return ProbeStatus{}, publicLoadError(err)
	}
	return status, nil
}

func (s *Service) runApproved(ctx context.Context, account, executionAgent string) (Report, error) {
	if err := s.requireLocalExecutionAgent(ctx, account, executionAgent); err != nil {
		return Report{}, err
	}
	record, err := s.store.LoadApprovedByOwner(ctx, account)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			status, statusErr := s.store.StatusByOwner(ctx, account)
			if statusErr != nil {
				return Report{}, publicLoadError(statusErr)
			}
			return Report{}, stateError(status.Status)
		}
		return Report{}, ErrUnavailable
	}
	defer clear(record.PrivateKey)
	report := s.venue.Probe(ctx, record.Owner, record.AgentAddress, executionAgent, record.PrivateKey, record.RequestedExpiry)
	if err := s.store.SaveResult(ctx, record.ProbeID, report); err != nil {
		return Report{}, ErrUnavailable
	}
	return report, nil
}

func (s *Service) recordApprovalFailure(ctx context.Context, record Record, approvalErr error, now time.Time) error {
	status := StatusRejected
	publicErr := ErrRejected
	switch {
	case errors.Is(approvalErr, ErrApprovalAmbiguous):
		status, publicErr = StatusUncertain, ErrUncertain
	case errors.Is(approvalErr, ErrApprovalNotSent):
		publicErr = ErrNotSent
	case errors.Is(approvalErr, ErrApprovalRejected):
		publicErr = ErrRejected
	default:
		status, publicErr = StatusUncertain, ErrUncertain
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.store.Transition(persistCtx, record.ProbeID, StatusSubmitting, status, publicErr.Error(), now); err != nil {
		return ErrUnavailable
	}
	return publicErr
}

func (s *Service) requireLocalExecutionAgent(ctx context.Context, owner, executionAgent string) error {
	if s.execution == nil {
		return ErrExecutionAgentMismatch
	}
	matches, err := s.execution.AgentAuthorizationMatches(ctx, "aster", owner, executionAgent)
	if err != nil {
		return ErrUnavailable
	}
	if !matches {
		return ErrExecutionAgentMismatch
	}
	return nil
}

func normalizeAccounts(account, executionAgent string) (string, string, error) {
	if !addressPattern.MatchString(account) || !addressPattern.MatchString(executionAgent) || strings.EqualFold(account, executionAgent) {
		return "", "", ErrInvalidInput
	}
	return strings.ToLower(account), strings.ToLower(executionAgent), nil
}

func publicLoadError(err error) error {
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	if errors.Is(err, ErrCredentialUnreadable) {
		return ErrCredentialUnreadable
	}
	return ErrUnavailable
}

func stateError(status Status) error {
	switch status {
	case StatusApproved:
		return ErrConflict
	case StatusRejected:
		return ErrRejected
	case StatusUncertain, StatusSubmitting:
		return ErrUncertain
	case StatusPending:
		return ErrNotApproved
	default:
		return ErrConflict
	}
}

func approvalFromRecord(record Record) Approval {
	return newApprovalAt(record.Owner, record.AgentAddress, record.ApprovalNonce, record.RequestedExpiry)
}

func newApproval(owner, agent string, now time.Time) Approval {
	return newApprovalAt(owner, agent, now.UnixMicro(), now.Add(agentLifetime).UnixMilli())
}

func newApprovalAt(owner, agent string, nonce, expiry int64) Approval {
	return Approval{
		User: owner, Nonce: nonce, AgentName: agentName, AgentAddress: agent,
		Expired: expiry, AsterChain: "Mainnet", SignatureChainID: 56,
	}
}

func validAuthorizationApproval(approval Approval, record Record, owner string, now time.Time) bool {
	issuedAt := time.UnixMicro(approval.Nonce)
	return approval.User == owner && approval.AgentAddress == record.AgentAddress &&
		approval.AgentName == agentName && approval.IPWhitelist == "" &&
		!approval.CanSpotTrade && !approval.CanPerpTrade && !approval.CanWithdraw &&
		approval.AsterChain == "Mainnet" && approval.SignatureChainID == 56 &&
		approval.Expired == issuedAt.Add(agentLifetime).UnixMilli() &&
		!issuedAt.After(now.Add(time.Second)) && now.Sub(issuedAt) <= approvalMaxAge
}

func approvalsEqual(left, right Approval) bool {
	return left == right
}

func publicApprovalError(err error) error {
	switch {
	case errors.Is(err, ErrApprovalRejected):
		return ErrRejected
	case errors.Is(err, ErrApprovalNotSent):
		return ErrNotSent
	default:
		return ErrUncertain
	}
}
