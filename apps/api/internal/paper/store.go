package paper

import "sync"

type Store struct {
	mu        sync.RWMutex
	positions map[string]*Position
}

func NewStore() *Store {
	return &Store{
		positions: make(map[string]*Position),
	}
}

func (s *Store) Add(pos *Position) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.positions[pos.ID] = pos
}

func (s *Store) Get(id string) *Position {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.positions[id]
	if !ok {
		return nil
	}
	// Return a copy
	cp := *p
	return &cp
}

func (s *Store) Update(pos *Position) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.positions[pos.ID] = pos
}

func (s *Store) List() []*Position {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Position, 0, len(s.positions))
	for _, p := range s.positions {
		cp := *p
		out = append(out, &cp)
	}
	return out
}

// OpenPositions returns positions in open or degraded state.
func (s *Store) OpenPositions() []*Position {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Position
	for _, p := range s.positions {
		if p.State == StateOpen || p.State == StateDegraded {
			out = append(out, p)
		}
	}
	return out
}
