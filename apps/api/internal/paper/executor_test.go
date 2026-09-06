package paper

import (
	"context"
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

func TestExecuteRejectsLiveOnlyVenueBeforeCreatingPosition(t *testing.T) {
	store := NewStore()
	executor := NewExecutor(nil, store, nil)
	plan := &domain.ExecutionPlan{
		Leg1: domain.Leg{Venue: "aster"},
		Leg2: domain.Leg{Venue: "pacifica"},
	}

	position, err := executor.Execute(context.Background(), plan)
	if err == nil {
		t.Fatal("Aster paper execution was accepted")
	}
	if position != nil || len(store.List()) != 0 {
		t.Fatalf("rejected paper execution created position: %+v", position)
	}
}
