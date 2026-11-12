// Package orders owns durable Phoenix order attempts and signature/history
// reconciliation. RPC acceptance/confirmation is never interpreted as a fill.
package orders

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
)

var (
	ErrNotFound  = errors.New("Phoenix order attempt not found")
	ErrUncertain = errors.New("Phoenix order outcome is uncertain; reconcile without resubmitting")
	ErrConflict  = errors.New("Phoenix order attempt correlation conflict")
)

type Before struct {
	BaseLots                   string
	Generation, Slot, Sequence uint64
	ObservedAt                 time.Time
}

// Quantities and event identities remain exact in the journal. Float summaries
// are derived only at the existing orchestration transport boundary.
type Fill struct {
	BaseLots, BaseQuantity, QuoteQuantity, ExchangeFee string
	Slot                                               uint64
	FillIDs                                            []string
	Status                                             string
	Accounting                                         *ObservedAccounting `json:"accounting,omitempty"`
}

type ObservedAccounting struct {
	Slot               uint64                  `json:"slot"`
	NetworkFeeLamports string                  `json:"network_fee_lamports"`
	Events             helper.AccountingResult `json:"events"`
}

func (f Fill) TotalTradingFee() (*big.Rat, bool) {
	fee, err := decimal(f.ExchangeFee)
	if err != nil {
		return nil, false
	}
	if f.Accounting == nil || f.Accounting.Events.FlightFeeQuoteLots == nil {
		return fee, false
	}
	flight, ok := new(big.Int).SetString(*f.Accounting.Events.FlightFeeQuoteLots, 10)
	if !ok || flight.Sign() < 0 {
		return fee, false
	}
	return new(big.Rat).Add(fee, new(big.Rat).SetFrac(flight, big.NewInt(1_000_000))), true
}

type Attempt struct {
	Request                         domain.SigningRequest
	Digest, Signature, State, Error string
	Before                          Before
	Fill                            *Fill
	CreatedAt                       time.Time
}

type Store struct{ writer, reader *sql.DB }

func NewStore(writer, reader *sql.DB) (*Store, error) {
	if writer == nil || reader == nil {
		return nil, errors.New("Phoenix order databases required")
	}
	return &Store{writer: writer, reader: reader}, nil
}

func requestDigest(q *domain.SigningRequest) (string, []byte, error) {
	raw, err := json.Marshal(q)
	if err != nil {
		return "", nil, err
	}
	if len(raw) > 64*1024 {
		return "", nil, errors.New("Phoenix stored order request exceeds bound")
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), raw, nil
}

func (s *Store) Get(ctx context.Context, owner, id string) (Attempt, error) {
	var a Attempt
	var request, before, fill, at string
	err := s.reader.QueryRowContext(ctx, `SELECT request_json,request_digest,signature,before_json,state,error,fill_json,created_at FROM phoenix_order_attempts WHERE owner_account=? AND request_id=?`, owner, id).
		Scan(&request, &a.Digest, &a.Signature, &before, &a.State, &a.Error, &fill, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	if err != nil {
		return a, err
	}
	if err = json.Unmarshal([]byte(request), &a.Request); err != nil {
		return a, err
	}
	if err = json.Unmarshal([]byte(before), &a.Before); err != nil {
		return a, err
	}
	if fill != "" {
		if err = json.Unmarshal([]byte(fill), &a.Fill); err != nil {
			return a, err
		}
	}
	a.CreatedAt, err = time.Parse(time.RFC3339Nano, at)
	if err != nil || a.Request.Account != owner || a.Request.ID != id {
		return Attempt{}, ErrConflict
	}
	digest, _, err := requestDigest(&a.Request)
	if err != nil || digest != a.Digest {
		return Attempt{}, ErrConflict
	}
	return a, nil
}

// Claim commits the local signature before any network send. Only the insert
// winner may send. Existing request/CID/signature identities are never replaced.
func (s *Store) Claim(ctx context.Context, a Attempt) (bool, error) {
	digest, raw, err := requestDigest(&a.Request)
	if err != nil {
		return false, err
	}
	before, err := json.Marshal(a.Before)
	if err != nil {
		return false, err
	}
	at := a.CreatedAt.UTC().Format(time.RFC3339Nano)
	result, err := s.writer.ExecContext(ctx, `INSERT INTO phoenix_order_attempts(request_id,owner_account,client_order_id,signature,request_json,request_digest,before_json,state,created_at,updated_at)
	 SELECT ?,?,?,?,?,?,?,'pending',?,? WHERE ?!='open' OR NOT EXISTS(SELECT 1 FROM phoenix_order_attempts WHERE owner_account=? AND state IN ('pending','submitted')) ON CONFLICT DO NOTHING`,
		a.Request.ID, a.Request.Account, a.Request.ClientOrderID, a.Signature, string(raw), digest, string(before), at, at, a.Request.Action, a.Request.Account)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (s *Store) Submitted(ctx context.Context, a Attempt, failure string) error {
	state := "submitted"
	if failure != "" {
		state = "failed"
	}
	_, err := s.writer.ExecContext(ctx, `UPDATE phoenix_order_attempts SET state=?,error=?,updated_at=? WHERE request_id=? AND owner_account=? AND signature=? AND state='pending'`, state, failure, time.Now().UTC().Format(time.RFC3339Nano), a.Request.ID, a.Request.Account, a.Signature)
	return err
}

func (s *Store) Resolve(ctx context.Context, a Attempt, state string, fill *Fill, failure string) error {
	if state != "filled" && state != "no_fill" && state != "failed" {
		return ErrConflict
	}
	raw := ""
	if fill != nil {
		bytes, err := json.Marshal(fill)
		if err != nil {
			return err
		}
		if len(bytes) > 64*1024 {
			return ErrConflict
		}
		raw = string(bytes)
	}
	_, err := s.writer.ExecContext(ctx, `UPDATE phoenix_order_attempts SET state=?,fill_json=?,error=?,updated_at=? WHERE request_id=? AND owner_account=? AND signature=? AND state IN ('pending','submitted')`, state, raw, failure, time.Now().UTC().Format(time.RFC3339Nano), a.Request.ID, a.Request.Account, a.Signature)
	return err
}

func (s *Store) Unresolved(ctx context.Context, owner string) (bool, error) {
	var count int
	err := s.reader.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM phoenix_order_attempts WHERE owner_account=? AND state IN ('pending','submitted'))`, owner).Scan(&count)
	return count != 0, err
}

func (s *Store) ByClientID(ctx context.Context, owner, cid string) (Attempt, error) {
	var id string
	err := s.reader.QueryRowContext(ctx, `SELECT request_id FROM phoenix_order_attempts WHERE owner_account=? AND client_order_id=?`, owner, cid).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Attempt{}, ErrNotFound
	}
	if err != nil {
		return Attempt{}, err
	}
	return s.Get(ctx, owner, id)
}

func (s *Store) Pending(ctx context.Context, owner string, limit int) ([]Attempt, error) {
	if limit < 1 || limit > 10 {
		return nil, ErrConflict
	}
	rows, err := s.reader.QueryContext(ctx, `SELECT request_id FROM phoenix_order_attempts WHERE owner_account=? AND state IN ('pending','submitted') ORDER BY created_at,request_id LIMIT ?`, owner, limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	result := make([]Attempt, 0, len(ids))
	for _, id := range ids {
		a, err := s.Get(ctx, owner, id)
		if err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, nil
}

func (s *Store) AttachAccounting(ctx context.Context, a Attempt, observed ObservedAccounting) error {
	if a.Fill == nil || a.State != "filled" || observed.Slot != a.Fill.Slot {
		return ErrConflict
	}
	before, err := json.Marshal(a.Fill)
	if err != nil {
		return err
	}
	copy := *a.Fill
	copy.Accounting = &observed
	after, err := json.Marshal(copy)
	if err != nil {
		return err
	}
	if len(after) > 64*1024 {
		return ErrConflict
	}
	_, err = s.writer.ExecContext(ctx, `UPDATE phoenix_order_attempts SET fill_json=?,updated_at=? WHERE owner_account=? AND request_id=? AND signature=? AND state='filled' AND fill_json=?`, string(after), time.Now().UTC().Format(time.RFC3339Nano), a.Request.Account, a.Request.ID, a.Signature, string(before))
	return err
}

func (s *Store) Unaccounted(ctx context.Context, owner string) ([]Attempt, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT request_id FROM phoenix_order_attempts WHERE owner_account=? AND state='filled' AND json_extract(fill_json,'$.accounting.events.flight_fee_quote_lots') IS NULL ORDER BY created_at LIMIT 4`, owner)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	var result []Attempt
	for _, id := range ids {
		a, err := s.Get(ctx, owner, id)
		if err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, nil
}
