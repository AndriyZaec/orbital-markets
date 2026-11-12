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
	expected, err := s.RegisteredAgent(ctx, venue, ownerAccount)
	return expected != "" && expected == agentAddress, err
}

// RegisteredAgent returns the last confirmed local registration, not fresh
// on-chain authority. Trading admission must verify the chain independently.
func (s *Store) RegisteredAgent(ctx context.Context, venue, ownerAccount string) (string, error) {
	var expected string
	err := s.reader.QueryRowContext(ctx, `
		SELECT agent_address
		FROM agent_authorizations
		WHERE venue = ? AND owner_account = ?`,
		venue, ownerAccount,
	).Scan(&expected)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return expected, nil
}

func (s *Store) DeleteAgentAuthorization(ctx context.Context, venue, ownerAccount, agentAddress string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM agent_authorizations
		WHERE venue = ? AND owner_account = ? AND agent_address = ?`,
		venue, ownerAccount, agentAddress,
	)
	return err
}
