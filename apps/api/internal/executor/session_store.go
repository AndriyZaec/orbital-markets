package executor

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// DurableSessionRecord is the persistence envelope for an API-owned live
// session payload. The executor store deliberately does not interpret it.
type DurableSessionRecord struct {
	ID                 string
	State              string
	Payload            []byte
	AccountPacifica    string
	AccountHyperliquid string
	Asset              string
	HasExposure        bool
	ExpiresAt          time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
	DecodeError        string
}

// UpsertDurableSession journals the latest recoverable session state.
func (s *Store) UpsertDurableSession(ctx context.Context, record DurableSessionRecord) error {
	now := time.Now().UTC()
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO live_sessions (
			id, state, payload, account_pacifica, account_hyperliquid, asset,
			has_exposure, expires_at,
			created_at, updated_at, terminal_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT(id) DO UPDATE SET
			state = excluded.state,
			payload = excluded.payload,
			account_pacifica = excluded.account_pacifica,
			account_hyperliquid = excluded.account_hyperliquid,
			asset = excluded.asset,
			has_exposure = excluded.has_exposure,
			expires_at = excluded.expires_at,
			updated_at = excluded.updated_at,
			terminal_at = NULL`,
		record.ID, record.State, string(record.Payload), record.AccountPacifica, record.AccountHyperliquid, record.Asset,
		boolToInt(record.HasExposure),
		record.ExpiresAt.UTC().Format(time.RFC3339Nano),
		createdAt.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
	)
	if err != nil {
		s.logger.Error("live store: upsert durable session", "err", err, "session_id", record.ID)
	}
	return err
}

// ListActiveDurableSessions returns non-terminal sessions for startup recovery.
func (s *Store) ListActiveDurableSessions(ctx context.Context) ([]DurableSessionRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, state, payload, account_pacifica, account_hyperliquid, asset,
			has_exposure, expires_at, created_at, updated_at
		FROM live_sessions
		WHERE terminal_at IS NULL
		ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []DurableSessionRecord
	for rows.Next() {
		var record DurableSessionRecord
		var payload, expiresAt, createdAt, updatedAt string
		var hasExposure int64
		if err := rows.Scan(
			&record.ID, &record.State, &payload,
			&record.AccountPacifica, &record.AccountHyperliquid, &record.Asset, &hasExposure,
			&expiresAt, &createdAt, &updatedAt,
		); err != nil {
			return nil, err
		}
		var decodeErrors []string
		var parseErr error
		if record.ExpiresAt, parseErr = time.Parse(time.RFC3339Nano, expiresAt); parseErr != nil {
			decodeErrors = append(decodeErrors, fmt.Sprintf("expires_at: %v", parseErr))
		}
		if record.CreatedAt, parseErr = time.Parse(time.RFC3339Nano, createdAt); parseErr != nil {
			decodeErrors = append(decodeErrors, fmt.Sprintf("created_at: %v", parseErr))
		}
		if record.UpdatedAt, parseErr = time.Parse(time.RFC3339Nano, updatedAt); parseErr != nil {
			decodeErrors = append(decodeErrors, fmt.Sprintf("updated_at: %v", parseErr))
		}
		record.Payload = []byte(payload)
		record.HasExposure = hasExposure != 0
		record.DecodeError = strings.Join(decodeErrors, "; ")
		records = append(records, record)
	}
	return records, rows.Err()
}

// FinishDurableSession keeps the journal row for audit while removing it from
// the active recovery set.
func (s *Store) FinishDurableSession(ctx context.Context, id, state, detail string) error {
	return s.finishDurableSession(ctx, id, "", state, detail)
}

// FinishDurableSessionOwned finalizes only a session whose recovery lease is
// unclaimed or owned by this server.
func (s *Store) FinishDurableSessionOwned(ctx context.Context, id, owner, state, detail string) error {
	return s.finishDurableSession(ctx, id, owner, state, detail)
}

func (s *Store) finishDurableSession(ctx context.Context, id, owner, state, detail string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, `
		UPDATE live_sessions
		SET state = ?, recovery_detail = ?, updated_at = ?, terminal_at = ?
		WHERE id = ? AND terminal_at IS NULL AND (recovery_owner = '' OR recovery_owner = ?)`,
		state, detail, now, now, id, owner)
	if err != nil {
		s.logger.Error("live store: finish durable session", "err", err, "session_id", id)
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// FlagDurableSession records a recovery blocker without removing a potentially
// exposed session from the active recovery set.
func (s *Store) FlagDurableSession(ctx context.Context, id, state, detail string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		UPDATE live_sessions
		SET state = ?, recovery_detail = ?, updated_at = ?
		WHERE id = ? AND terminal_at IS NULL`, state, detail, now, id)
	if err != nil {
		s.logger.Error("live store: flag durable session", "err", err, "session_id", id)
	}
	return err
}

// ClaimDurableSession obtains a cross-process recovery lease. This prevents
// overlapping server instances from submitting the same armed unwind.
func (s *Store) ClaimDurableSession(ctx context.Context, id, owner string, lease time.Duration) (bool, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE live_sessions
		SET recovery_owner = ?, recovery_lease_until = ?, updated_at = ?
		WHERE id = ? AND terminal_at IS NULL AND (
			recovery_owner = '' OR recovery_owner = ? OR recovery_lease_until IS NULL OR recovery_lease_until < ?
		)`,
		owner, now.Add(lease).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
		id, owner, now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

// UpsertRecoveryBlockedPosition surfaces an unreadable exposed session through
// the existing live-position/operator UI without inventing actionable fills.
func (s *Store) UpsertRecoveryBlockedPosition(ctx context.Context, sessionID, asset, detail string) error {
	positionID := "recovery-" + sessionID
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO live_positions (
			id, plan_id, opportunity_id, asset, venue_a, venue_b, state,
			notional, leverage, started_at, completed_at, updated_at
		) VALUES (?, ?, 'recovery-blocked', ?, 'unknown', 'unknown', ?, 0, 0, ?, ?, ?)`,
		positionID, sessionID, asset, string(ExecStateDegraded), now, now, now)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows == 0 {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO live_events (position_id, event, state, detail, at)
		VALUES (?, 'session_recovery_blocked', ?, ?, ?)`,
		positionID, string(ExecStateDegraded), detail, now)
	return err
}
