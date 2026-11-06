package executor

import "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/orders"

// PhoenixOrderJournal shares the execution databases without exposing handles
// to account-feed orchestration or transporting signed packets through the DB.
func (s *Store) PhoenixOrderJournal() (*orders.Store, error) {
	return orders.NewStore(s.db, s.reader)
}
