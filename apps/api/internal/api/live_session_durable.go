package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	ID                         string                    `json:"id"`
	Plan                       *domain.ExecutionPlan     `json:"plan"`
	Leg1                       durableLegPlan            `json:"leg_1"`
	Leg2                       durableLegPlan            `json:"leg_2"`
	AccountPacifica            string                    `json:"account_pacifica"`
	AccountHyperliquid         string                    `json:"account_hyperliquid"`
	AgentPacifica              string                    `json:"agent_pacifica,omitempty"`
	AgentHyperliquid           string                    `json:"agent_hyperliquid,omitempty"`
	State                      sessionState              `json:"state"`
	Leg1OpenReqID              string                    `json:"leg_1_open_request_id"`
	Leg1UnwindReqID            string                    `json:"leg_1_unwind_request_id"`
	PacificaLeverageReqID      string                    `json:"pacifica_leverage_request_id,omitempty"`
	HyperliquidLeverageReqID   string                    `json:"hyperliquid_leverage_request_id,omitempty"`
	Leg2OpenReqID              string                    `json:"leg_2_open_request_id"`
	Leg2RetryReqID             string                    `json:"leg_2_retry_request_id"`
	Leg1OpenReq                *domain.SigningRequest    `json:"leg_1_open_request,omitempty"`
	Leg1UnwindReq              *domain.SigningRequest    `json:"leg_1_unwind_request,omitempty"`
	PacificaLeverageReq        *domain.SigningRequest    `json:"pacifica_leverage_request,omitempty"`
	HyperliquidLeverageReq     *domain.SigningRequest    `json:"hyperliquid_leverage_request,omitempty"`
	PacificaLeverageApplied    bool                      `json:"pacifica_leverage_applied,omitempty"`
	HyperliquidLeverageApplied bool                      `json:"hyperliquid_leverage_applied,omitempty"`
	Leg2OpenReq                *domain.SigningRequest    `json:"leg_2_open_request,omitempty"`
	Leg2RetryReq               *domain.SigningRequest    `json:"leg_2_retry_request,omitempty"`
	ArmedUnwindSigned          *domain.SignedAction      `json:"armed_unwind_signed,omitempty"`
	ArmedUnwindReq             *domain.SigningRequest    `json:"armed_unwind_request,omitempty"`
	Leg1Fill                   *normFill                 `json:"leg_1_fill,omitempty"`
	Leg2Fill                   *normFill                 `json:"leg_2_fill,omitempty"`
	Leg2Attempts               int                       `json:"leg_2_attempts"`
	Recovery                   []executor.RecoveryAction `json:"recovery,omitempty"`
	BaselineLeg1Size           float64                   `json:"baseline_leg_1_size"`
	BaselineLeg2Size           float64                   `json:"baseline_leg_2_size"`
	CreatedAt                  string                    `json:"created_at"`
	UpdatedAt                  string                    `json:"updated_at"`
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
		AccountPacifica: session.Bindings.Accounts["pacifica"], AccountHyperliquid: session.Bindings.Accounts["hyperliquid"],
		AgentPacifica: session.Bindings.Agents["pacifica"], AgentHyperliquid: session.Bindings.Agents["hyperliquid"],
		State:         session.State,
		Leg1OpenReqID: session.Leg1OpenReqID, Leg1UnwindReqID: session.Leg1UnwindReqID,
		PacificaLeverageReqID: signingRequestID(pacificaLeverage, ""), HyperliquidLeverageReqID: signingRequestID(hyperliquidLeverage, ""),
		Leg2OpenReqID: session.Leg2OpenReqID, Leg2RetryReqID: session.Leg2RetryReqID,
		Leg1OpenReq: session.Leg1OpenReq, Leg1UnwindReq: session.Leg1UnwindReq,
		PacificaLeverageReq: pacificaLeverage, HyperliquidLeverageReq: hyperliquidLeverage,
		PacificaLeverageApplied: session.LeverageApplied["pacifica"], HyperliquidLeverageApplied: session.LeverageApplied["hyperliquid"],
		Leg2OpenReq: session.Leg2OpenReq, Leg2RetryReq: session.Leg2RetryReq,
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
	session := &LiveSession{
		ID: durable.ID, Plan: durable.Plan,
		Leg1: legPlan{venue: durable.Leg1.Venue, symbol: durable.Leg1.Symbol, side: durable.Leg1.Side, price: durable.Leg1.Price},
		Leg2: legPlan{venue: durable.Leg2.Venue, symbol: durable.Leg2.Symbol, side: durable.Leg2.Side, price: durable.Leg2.Price},
		Bindings: liveVenueBindings{
			Accounts: map[string]string{"pacifica": durable.AccountPacifica, "hyperliquid": durable.AccountHyperliquid},
			Agents:   map[string]string{"pacifica": durable.AgentPacifica, "hyperliquid": durable.AgentHyperliquid},
		},
		State:         durable.State,
		Leg1OpenReqID: durable.Leg1OpenReqID, Leg1UnwindReqID: durable.Leg1UnwindReqID,
		Leg2OpenReqID: durable.Leg2OpenReqID, Leg2RetryReqID: durable.Leg2RetryReqID,
		Leg1OpenReq: durable.Leg1OpenReq, Leg1UnwindReq: durable.Leg1UnwindReq,
		LeverageRequests: map[string]*domain.SigningRequest{
			"pacifica": durable.PacificaLeverageReq, "hyperliquid": durable.HyperliquidLeverageReq,
		},
		LeverageApplied: map[string]bool{
			"pacifica": durable.PacificaLeverageApplied, "hyperliquid": durable.HyperliquidLeverageApplied,
		},
		Leg2OpenReq: durable.Leg2OpenReq, Leg2RetryReq: durable.Leg2RetryReq,
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

func validateDurableSessionOwnership(record executor.DurableSessionRecord, session *LiveSession) error {
	if session == nil {
		return fmt.Errorf("session payload missing")
	}
	if record.ID != session.ID {
		return fmt.Errorf("session ID does not match durable envelope")
	}
	if _, _, err := legacyDurableAccountPair(session); err != nil {
		return err
	}
	if !sameVenueBinding("pacifica", record.AccountPacifica, session.Bindings.Accounts["pacifica"]) ||
		!sameVenueBinding("hyperliquid", record.AccountHyperliquid, session.Bindings.Accounts["hyperliquid"]) {
		return fmt.Errorf("session accounts do not match durable envelope")
	}
	if session.Plan == nil {
		return fmt.Errorf("session plan missing")
	}
	if record.Asset != session.Plan.Asset {
		return fmt.Errorf("session asset does not match durable envelope")
	}

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

	session.Bindings.Accounts["pacifica"] = strings.TrimSpace(record.AccountPacifica)
	session.Bindings.Accounts["hyperliquid"] = strings.ToLower(strings.TrimSpace(record.AccountHyperliquid))
	return nil
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

func legacyDurableAccountPair(session *LiveSession) (string, string, error) {
	if session == nil {
		return "", "", fmt.Errorf("session missing")
	}
	venues := session.venueNames()
	if err := requireLegacyDurableVenuePair(venues); err != nil {
		return "", "", err
	}
	pacifica := strings.TrimSpace(session.Bindings.Accounts["pacifica"])
	hyperliquid := strings.ToLower(strings.TrimSpace(session.Bindings.Accounts["hyperliquid"]))
	if pacifica == "" || hyperliquid == "" {
		return "", "", fmt.Errorf("durable session account bindings missing")
	}
	return pacifica, hyperliquid, nil
}

func requireLegacyDurableVenuePair(venues []string) error {
	if len(venues) != 2 ||
		!((venues[0] == "pacifica" && venues[1] == "hyperliquid") ||
			(venues[0] == "hyperliquid" && venues[1] == "pacifica")) {
		return fmt.Errorf("durable storage does not support venue pair %v", venues)
	}
	return nil
}

func (s *Server) saveLiveSession(ctx context.Context, session *LiveSession) error {
	session.UpdatedAt = time.Now()
	pacificaAccount, hyperliquidAccount, err := legacyDurableAccountPair(session)
	if err != nil {
		return err
	}
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
		AccountPacifica:    pacificaAccount,
		AccountHyperliquid: hyperliquidAccount,
		Asset:              session.Plan.Asset,
		HasExposure:        session.hasPossibleExposure(),
		ExpiresAt:          session.CreatedAt.Add(sessionTTL), CreatedAt: session.CreatedAt,
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
