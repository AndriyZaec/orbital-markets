package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

var errDurableSessionOwned = errors.New("durable session recovery owned by another server")

const sessionRecoveryLease = 90 * time.Second

type durableLegPlan struct {
	Venue  string      `json:"venue"`
	Symbol string      `json:"symbol"`
	Side   domain.Side `json:"side"`
	Price  float64     `json:"price"`
}

type durableLiveSession struct {
	ID                         string                            `json:"id"`
	Plan                       *domain.ExecutionPlan             `json:"plan"`
	Leg1                       durableLegPlan                    `json:"leg_1"`
	Leg2                       durableLegPlan                    `json:"leg_2"`
	Accounts                   map[string]string                 `json:"accounts,omitempty"`
	Agents                     map[string]string                 `json:"agents,omitempty"`
	AccountPacifica            string                            `json:"account_pacifica"`
	AccountHyperliquid         string                            `json:"account_hyperliquid"`
	AgentPacifica              string                            `json:"agent_pacifica,omitempty"`
	AgentHyperliquid           string                            `json:"agent_hyperliquid,omitempty"`
	State                      sessionState                      `json:"state"`
	Leg1OpenReqID              string                            `json:"leg_1_open_request_id"`
	Leg1UnwindReqID            string                            `json:"leg_1_unwind_request_id"`
	PacificaLeverageReqID      string                            `json:"pacifica_leverage_request_id,omitempty"`
	HyperliquidLeverageReqID   string                            `json:"hyperliquid_leverage_request_id,omitempty"`
	Leg2OpenReqID              string                            `json:"leg_2_open_request_id"`
	Leg2RetryReqID             string                            `json:"leg_2_retry_request_id"`
	Leg1OpenReq                *domain.SigningRequest            `json:"leg_1_open_request,omitempty"`
	Leg1UnwindReq              *domain.SigningRequest            `json:"leg_1_unwind_request,omitempty"`
	PacificaLeverageReq        *domain.SigningRequest            `json:"pacifica_leverage_request,omitempty"`
	HyperliquidLeverageReq     *domain.SigningRequest            `json:"hyperliquid_leverage_request,omitempty"`
	PacificaLeverageApplied    bool                              `json:"pacifica_leverage_applied,omitempty"`
	HyperliquidLeverageApplied bool                              `json:"hyperliquid_leverage_applied,omitempty"`
	LeverageRequests           map[string]*domain.SigningRequest `json:"leverage_requests,omitempty"`
	LeverageApplied            map[string]bool                   `json:"leverage_applied,omitempty"`
	Leg2OpenReq                *domain.SigningRequest            `json:"leg_2_open_request,omitempty"`
	Leg2RetryReq               *domain.SigningRequest            `json:"leg_2_retry_request,omitempty"`
	ArmedUnwindSigned          *domain.SignedAction              `json:"armed_unwind_signed,omitempty"`
	ArmedUnwindReq             *domain.SigningRequest            `json:"armed_unwind_request,omitempty"`
	Leg1Fill                   *normFill                         `json:"leg_1_fill,omitempty"`
	Leg2Fill                   *normFill                         `json:"leg_2_fill,omitempty"`
	Leg2Attempts               int                               `json:"leg_2_attempts"`
	Recovery                   []executor.RecoveryAction         `json:"recovery,omitempty"`
	BaselineLeg1Size           float64                           `json:"baseline_leg_1_size"`
	BaselineLeg2Size           float64                           `json:"baseline_leg_2_size"`
	CreatedAt                  string                            `json:"created_at"`
	UpdatedAt                  string                            `json:"updated_at"`
}

func signingRequestID(request *domain.SigningRequest, fallback string) string {
	if request != nil && request.ID != "" {
		return request.ID
	}
	return fallback
}

func marshalLiveSession(session *LiveSession) ([]byte, error) {
	pacificaLeverage := session.LeverageRequests["pacifica"]
	hyperliquidLeverage := session.LeverageRequests["hyperliquid"]
	durable := durableLiveSession{
		ID: session.ID, Plan: session.Plan,
		Leg1:            durableLegPlan{Venue: session.Leg1.venue, Symbol: session.Leg1.symbol, Side: session.Leg1.side, Price: session.Leg1.price},
		Leg2:            durableLegPlan{Venue: session.Leg2.venue, Symbol: session.Leg2.symbol, Side: session.Leg2.side, Price: session.Leg2.price},
		Accounts:        cloneVenueBindingMap(session.Bindings.Accounts),
		Agents:          cloneVenueBindingMap(session.Bindings.Agents),
		AccountPacifica: session.Bindings.Accounts["pacifica"], AccountHyperliquid: session.Bindings.Accounts["hyperliquid"],
		AgentPacifica: session.Bindings.Agents["pacifica"], AgentHyperliquid: session.Bindings.Agents["hyperliquid"],
		State:         session.State,
		Leg1OpenReqID: session.Leg1OpenReqID, Leg1UnwindReqID: session.Leg1UnwindReqID,
		PacificaLeverageReqID: signingRequestID(pacificaLeverage, ""), HyperliquidLeverageReqID: signingRequestID(hyperliquidLeverage, ""),
		Leg2OpenReqID: session.Leg2OpenReqID, Leg2RetryReqID: session.Leg2RetryReqID,
		Leg1OpenReq: session.Leg1OpenReq, Leg1UnwindReq: session.Leg1UnwindReq,
		PacificaLeverageReq: pacificaLeverage, HyperliquidLeverageReq: hyperliquidLeverage,
		PacificaLeverageApplied: session.LeverageApplied["pacifica"], HyperliquidLeverageApplied: session.LeverageApplied["hyperliquid"],
		LeverageRequests: cloneSigningRequestMap(session.LeverageRequests),
		LeverageApplied:  cloneBoolMap(session.LeverageApplied),
		Leg2OpenReq:      session.Leg2OpenReq, Leg2RetryReq: session.Leg2RetryReq,
		ArmedUnwindSigned: session.ArmedUnwindSigned, ArmedUnwindReq: session.ArmedUnwindReq,
		Leg1Fill: session.Leg1Fill, Leg2Fill: session.Leg2Fill,
		Leg2Attempts: session.Leg2Attempts, Recovery: session.Recovery,
		BaselineLeg1Size: session.BaselineLeg1Size,
		BaselineLeg2Size: session.BaselineLeg2Size,
		CreatedAt:        session.CreatedAt.UTC().Format(timeFormat), UpdatedAt: session.UpdatedAt.UTC().Format(timeFormat),
	}
	return json.Marshal(durable)
}

func unmarshalLiveSession(payload []byte) (*LiveSession, error) {
	var durable durableLiveSession
	if err := json.Unmarshal(payload, &durable); err != nil {
		return nil, err
	}
	createdAt, err := parseSessionTime(durable.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("created_at: %w", err)
	}
	updatedAt, err := parseSessionTime(durable.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("updated_at: %w", err)
	}
	accounts := cloneVenueBindingMap(durable.Accounts)
	agents := cloneVenueBindingMap(durable.Agents)
	if err := mergeLegacyVenueBindings("accounts", accounts, map[string]string{
		"pacifica": durable.AccountPacifica, "hyperliquid": durable.AccountHyperliquid,
	}); err != nil {
		return nil, err
	}
	if err := mergeLegacyVenueBindings("agents", agents, map[string]string{
		"pacifica": durable.AgentPacifica, "hyperliquid": durable.AgentHyperliquid,
	}); err != nil {
		return nil, err
	}
	leverageRequests := cloneSigningRequestMap(durable.LeverageRequests)
	if err := mergeLegacyLeverageRequest(leverageRequests, "pacifica", durable.PacificaLeverageReq); err != nil {
		return nil, err
	}
	if err := mergeLegacyLeverageRequest(leverageRequests, "hyperliquid", durable.HyperliquidLeverageReq); err != nil {
		return nil, err
	}
	leverageApplied := cloneBoolMap(durable.LeverageApplied)
	if durable.PacificaLeverageApplied {
		leverageApplied["pacifica"] = true
	}
	if durable.HyperliquidLeverageApplied {
		leverageApplied["hyperliquid"] = true
	}
	session := &LiveSession{
		ID: durable.ID, Plan: durable.Plan,
		Leg1: legPlan{venue: durable.Leg1.Venue, symbol: durable.Leg1.Symbol, side: durable.Leg1.Side, price: durable.Leg1.Price},
		Leg2: legPlan{venue: durable.Leg2.Venue, symbol: durable.Leg2.Symbol, side: durable.Leg2.Side, price: durable.Leg2.Price},
		Bindings: liveVenueBindings{
			Accounts: accounts,
			Agents:   agents,
		},
		State:         durable.State,
		Leg1OpenReqID: durable.Leg1OpenReqID, Leg1UnwindReqID: durable.Leg1UnwindReqID,
		Leg2OpenReqID: durable.Leg2OpenReqID, Leg2RetryReqID: durable.Leg2RetryReqID,
		Leg1OpenReq: durable.Leg1OpenReq, Leg1UnwindReq: durable.Leg1UnwindReq,
		LeverageRequests: leverageRequests,
		LeverageApplied:  leverageApplied,
		Leg2OpenReq:      durable.Leg2OpenReq, Leg2RetryReq: durable.Leg2RetryReq,
		ArmedUnwindSigned: durable.ArmedUnwindSigned, ArmedUnwindReq: durable.ArmedUnwindReq,
		Leg1Fill: durable.Leg1Fill, Leg2Fill: durable.Leg2Fill,
		Leg2Attempts: durable.Leg2Attempts, Recovery: durable.Recovery,
		BaselineLeg1Size: durable.BaselineLeg1Size,
		BaselineLeg2Size: durable.BaselineLeg2Size,
		CreatedAt:        createdAt, UpdatedAt: updatedAt,
	}
	backfillSigningRequestAccounts(session)
	return session, nil
}

func cloneSigningRequestMap(source map[string]*domain.SigningRequest) map[string]*domain.SigningRequest {
	cloned := make(map[string]*domain.SigningRequest, len(source))
	for venueName, request := range source {
		cloned[venueName] = request
	}
	return cloned
}

func cloneBoolMap(source map[string]bool) map[string]bool {
	cloned := make(map[string]bool, len(source))
	for venueName, value := range source {
		cloned[venueName] = value
	}
	return cloned
}

func mergeLegacyLeverageRequest(
	requests map[string]*domain.SigningRequest,
	venueName string,
	legacy *domain.SigningRequest,
) error {
	if legacy == nil {
		return nil
	}
	if current := requests[venueName]; current != nil {
		if !reflect.DeepEqual(current, legacy) {
			return fmt.Errorf("%s leverage request conflicts with legacy payload", venueName)
		}
		return nil
	}
	requests[venueName] = legacy
	return nil
}

func validateDurableSessionOwnership(record executor.DurableSessionRecord, session *LiveSession) error {
	if session == nil {
		return fmt.Errorf("session payload missing")
	}
	if record.ID != session.ID {
		return fmt.Errorf("session ID does not match durable envelope")
	}
	recordBindings := durableRecordAccountBindings(record)
	if !session.Bindings.matchesAccounts(recordBindings) {
		return fmt.Errorf("session accounts do not match durable envelope")
	}
	if err := session.Bindings.requireAccountsFor(session.venueNames()); err != nil {
		return err
	}
	if session.Plan == nil {
		return fmt.Errorf("session plan missing")
	}
	if record.Asset != session.Plan.Asset {
		return fmt.Errorf("session asset does not match durable envelope")
	}

	session.Bindings.Accounts = cloneVenueBindingMap(recordBindings)
	for venueName, request := range session.LeverageRequests {
		if request != nil && request.Venue != venueName {
			return fmt.Errorf("%s leverage request has venue %s", venueName, request.Venue)
		}
	}
	for _, request := range session.signingRequests() {
		if request == nil {
			continue
		}
		expected := session.Bindings.Accounts[request.Venue]
		if expected == "" || !sameVenueBinding(request.Venue, expected, request.Account) {
			return fmt.Errorf("%s signing request account does not match durable envelope", request.Venue)
		}
	}

	return nil
}

func durableRecordAccountBindings(record executor.DurableSessionRecord) map[string]string {
	if len(record.AccountBindings) > 0 {
		return cloneVenueBindingMap(record.AccountBindings)
	}
	pacifica := strings.TrimSpace(record.AccountPacifica)
	hyperliquid := strings.ToLower(strings.TrimSpace(record.AccountHyperliquid))
	if pacifica == "" || hyperliquid == "" {
		return nil
	}
	return map[string]string{
		"pacifica": pacifica, "hyperliquid": hyperliquid,
	}
}

// Persisted sessions created before account-scoped feeds did not store the
// request account. Backfill it so an armed unwind remains usable after deploy.
func backfillSigningRequestAccounts(session *LiveSession) {
	for _, request := range session.signingRequests() {
		if request != nil && request.Account == "" {
			request.Account = session.Bindings.Accounts[request.Venue]
		}
	}
}

const timeFormat = "2006-01-02T15:04:05.999999999Z07:00"

func parseSessionTime(value string) (time.Time, error) {
	return time.Parse(timeFormat, value)
}

func requireCurrentLiveVenuePair(venues []string) error {
	if len(venues) != 2 || venues[0] == venues[1] ||
		!supportedLiveVenue(venues[0]) || !supportedLiveVenue(venues[1]) {
		return fmt.Errorf("live execution does not support venue pair %v", venues)
	}
	return nil
}

func supportedLiveVenue(venue string) bool {
	return venue == "pacifica" || venue == "hyperliquid" || venue == "aster"
}

func (s *Server) saveLiveSession(ctx context.Context, session *LiveSession) error {
	session.UpdatedAt = time.Now()
	payload, err := marshalLiveSession(session)
	if err != nil {
		return err
	}
	if session.hasPossibleExposure() {
		claimed, err := s.liveStore.ClaimDurableSession(ctx, session.ID, s.recoveryOwner, sessionRecoveryLease)
		if err != nil {
			return err
		}
		if !claimed {
			return errDurableSessionOwned
		}
	}
	if err := s.liveStore.UpsertDurableSession(ctx, executor.DurableSessionRecord{
		ID: session.ID, State: string(session.State), Payload: payload,
		AccountBindings: session.Bindings.Accounts,
		Asset:           session.Plan.Asset,
		HasExposure:     session.hasPossibleExposure(),
		ExpiresAt:       session.CreatedAt.Add(sessionTTL), CreatedAt: session.CreatedAt,
	}); err != nil {
		return err
	}
	return nil
}

func (s *Server) finishLiveSession(ctx context.Context, session *LiveSession, detail string) {
	s.live.sessions.remove(session.ID)
	if err := s.liveStore.FinishDurableSessionOwned(ctx, session.ID, s.recoveryOwner, string(session.State), detail); err != nil {
		s.logger.Error("live session: finish durable record", "err", err, "session_id", session.ID)
	}
}
