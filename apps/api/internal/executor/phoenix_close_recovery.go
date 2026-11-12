package executor

import "context"

// PendingPhoenixCloses is bounded to the latest pending attempt per leg. Native
// CID maps it back to the immutable transaction journal after process restart.
func (s *Store) PendingPhoenixCloses(ctx context.Context, positionID string) ([]CloseOutcome, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT position_id,leg,venue,client_order_id,order_id,requested_amount,accepted FROM live_close_outcomes
	 WHERE position_id=? AND venue='phoenix' AND resolved=0 AND id IN (SELECT MAX(id) FROM live_close_outcomes WHERE position_id=? GROUP BY leg) ORDER BY leg LIMIT 2`, positionID, positionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []CloseOutcome
	for rows.Next() {
		var row CloseOutcome
		if err := rows.Scan(&row.PositionID, &row.Leg, &row.Venue, &row.ClientOrderID, &row.OrderID, &row.RequestedAmount, &row.Accepted); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
