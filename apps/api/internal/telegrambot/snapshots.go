package telegrambot

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

const snapshotTTL = 2 * time.Minute
const maxOpportunitySnapshots = 1_000

type opportunitySnapshot struct {
	chatID        int64
	createdAt     time.Time
	opportunities []domain.Opportunity
}

type snapshotStore struct {
	mu        sync.Mutex
	snapshots map[string]opportunitySnapshot
	now       func() time.Time
}

func newSnapshotStore() *snapshotStore {
	return &snapshotStore{
		snapshots: make(map[string]opportunitySnapshot),
		now:       time.Now,
	}
}

func (s *snapshotStore) create(chatID int64, opportunities []domain.Opportunity) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, snapshot := range s.snapshots {
		if now.Sub(snapshot.createdAt) >= snapshotTTL {
			delete(s.snapshots, id)
		}
	}
	if len(s.snapshots) >= maxOpportunitySnapshots {
		oldestID := ""
		var oldest opportunitySnapshot
		for id, snapshot := range s.snapshots {
			if oldestID == "" || snapshot.createdAt.Before(oldest.createdAt) {
				oldestID = id
				oldest = snapshot
			}
		}
		if oldestID != "" {
			delete(s.snapshots, oldestID)
		}
	}
	id := randomSnapshotID()
	s.snapshots[id] = opportunitySnapshot{
		chatID:        chatID,
		createdAt:     now,
		opportunities: append([]domain.Opportunity(nil), opportunities...),
	}
	return id
}

func (s *snapshotStore) get(id string, chatID int64) (opportunitySnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.snapshots[id]
	if !ok || snapshot.chatID != chatID {
		return opportunitySnapshot{}, false
	}
	if s.now().Sub(snapshot.createdAt) >= snapshotTTL {
		delete(s.snapshots, id)
		return opportunitySnapshot{}, false
	}
	return snapshot, true
}

func randomSnapshotID() string {
	var raw [9]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}
