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
	ID                 string                 `json:"id"`
	Plan               *domain.ExecutionPlan  `json:"plan"`
	Leg1               durableLegPlan         `json:"leg_1"`
	Leg2               durableLegPlan         `json:"leg_2"`
	AccountPacifica    string                 `json:"account_pacifica"`
	AccountHyperliquid string                 `json:"account_hyperliquid"`
	State              sessionState           `json:"state"`
	Leg1OpenReqID      string                 `json:"leg_1_open_request_id"`
	Leg1UnwindReqID    string                 `json:"leg_1_unwind_request_id"`
	Leg2OpenReqID      string                 `json:"leg_2_open_request_id"`
	Leg1OpenReq        *domain.SigningRequest `json:"leg_1_open_request,omitempty"`
	Leg1UnwindReq      *domain.SigningRequest `json:"leg_1_unwind_request,omitempty"`
	Leg2OpenReq        *domain.SigningRequest `json:"leg_2_open_request,omitempty"`
	ArmedUnwindSigned  *domain.SignedAction   `json:"armed_unwind_signed,omitempty"`
	ArmedUnwindReq     *domain.SigningRequest `json:"armed_unwind_request,omitempty"`
	Leg1Fill           *normFill              `json:"leg_1_fill,omitempty"`
	Leg2Fill           *normFill              `json:"leg_2_fill,omitempty"`
	BaselineLeg1Size   float64                `json:"baseline_leg_1_size"`
	BaselineLeg2Size   float64                `json:"baseline_leg_2_size"`
	CreatedAt          string                 `json:"created_at"`
	UpdatedAt          string                 `json:"updated_at"`
}

func marshalLiveSession(session *LiveSession) ([]byte, error) {
	durable := durableLiveSession{
		ID: session.ID, Plan: session.Plan,
		Leg1:            durableLegPlan{Venue: session.Leg1.venue, Symbol: session.Leg1.symbol, Side: session.Leg1.side, Price: session.Leg1.price},
		Leg2:            durableLegPlan{Venue: session.Leg2.venue, Symbol: session.Leg2.symbol, Side: session.Leg2.side, Price: session.Leg2.price},
		AccountPacifica: session.AccountPacifica, AccountHyperliquid: session.AccountHyperliquid,
		State:         session.State,
		Leg1OpenReqID: session.Leg1OpenReqID, Leg1UnwindReqID: session.Leg1UnwindReqID, Leg2OpenReqID: session.Leg2OpenReqID,
		Leg1OpenReq: session.Leg1OpenReq, Leg1UnwindReq: session.Leg1UnwindReq, Leg2OpenReq: session.Leg2OpenReq,
		ArmedUnwindSigned: session.ArmedUnwindSigned, ArmedUnwindReq: session.ArmedUnwindReq,
		Leg1Fill: session.Leg1Fill, Leg2Fill: session.Leg2Fill,
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
	return &LiveSession{
		ID: durable.ID, Plan: durable.Plan,
		Leg1:            legPlan{venue: durable.Leg1.Venue, symbol: durable.Leg1.Symbol, side: durable.Leg1.Side, price: durable.Leg1.Price},
		Leg2:            legPlan{venue: durable.Leg2.Venue, symbol: durable.Leg2.Symbol, side: durable.Leg2.Side, price: durable.Leg2.Price},
		AccountPacifica: durable.AccountPacifica, AccountHyperliquid: durable.AccountHyperliquid,
		State:         durable.State,
		Leg1OpenReqID: durable.Leg1OpenReqID, Leg1UnwindReqID: durable.Leg1UnwindReqID, Leg2OpenReqID: durable.Leg2OpenReqID,
		Leg1OpenReq: durable.Leg1OpenReq, Leg1UnwindReq: durable.Leg1UnwindReq, Leg2OpenReq: durable.Leg2OpenReq,
		ArmedUnwindSigned: durable.ArmedUnwindSigned, ArmedUnwindReq: durable.ArmedUnwindReq,
		Leg1Fill: durable.Leg1Fill, Leg2Fill: durable.Leg2Fill,
		BaselineLeg1Size: durable.BaselineLeg1Size,
		BaselineLeg2Size: durable.BaselineLeg2Size,
		CreatedAt:        createdAt, UpdatedAt: updatedAt,
	}, nil
}

const timeFormat = "2006-01-02T15:04:05.999999999Z07:00"

func parseSessionTime(value string) (time.Time, error) {
	return time.Parse(timeFormat, value)
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
		AccountPacifica:    strings.TrimSpace(session.AccountPacifica),
		AccountHyperliquid: strings.ToLower(strings.TrimSpace(session.AccountHyperliquid)),
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
