package executor_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	appdb "github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

func TestAgentAuthorizationPersistsAndCannotMoveBetweenOwners(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "agents.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	store := executor.NewStore(database, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	const (
		venue = "hyperliquid"
		owner = "0x14791697260e4c9a71f18484c9f997b308e59325"
		agent = "0x19e7e376e7c213b7e7e7e46cc70a5dd086daff2a"
	)
	if err := store.UpsertAgentAuthorization(ctx, venue, owner, agent); err != nil {
		t.Fatal(err)
	}
	matches, err := store.AgentAuthorizationMatches(ctx, venue, owner, agent)
	if err != nil || !matches {
		t.Fatalf("persisted authorization match = %v, err = %v", matches, err)
	}
	matches, err = store.AgentAuthorizationMatches(
		ctx, venue, "0x0000000000000000000000000000000000000001", agent,
	)
	if err != nil {
		t.Fatal(err)
	}
	if matches {
		t.Fatal("agent authorization moved to another owner")
	}
}
