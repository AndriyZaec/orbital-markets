package executor

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *Store) UpsertAgentAuthorization(
	ctx context.Context,
	venue, ownerAccount, agentAddress string,
) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_authorizations (venue, owner_account, agent_address, authorized_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (venue, owner_account) DO UPDATE SET
			agent_address = excluded.agent_address,
			authorized_at = excluded.authorized_at`,
		venue, ownerAccount, agentAddress, time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) AgentAuthorizationMatches(
	ctx context.Context,
	venue, ownerAccount, agentAddress string,
) (bool, error) {
	var expected string
	err := s.db.QueryRowContext(ctx, `
		SELECT agent_address
		FROM agent_authorizations
		WHERE venue = ? AND owner_account = ?`,
		venue, ownerAccount,
	).Scan(&expected)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return expected == agentAddress, nil
}
