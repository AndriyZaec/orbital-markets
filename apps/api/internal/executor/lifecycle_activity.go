package executor

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// LifecycleActivityType identifies a user-visible position lifecycle transition.
type LifecycleActivityType string

const (
	LifecycleActivityOpened LifecycleActivityType = "opened"
	LifecycleActivityClosed LifecycleActivityType = "closed"
)

// LifecycleActivity is an opened or closed transition with its full position snapshot.
type LifecycleActivity struct {
	ID       string                `json:"id"`
	Type     LifecycleActivityType `json:"type"`
	At       string                `json:"at"`
	Position LivePosition          `json:"position"`
}

// LifecycleActivityCursor is the exclusive boundary for descending activity reads.
type LifecycleActivityCursor struct {
	At string
	ID string
}

// LifecycleActivityQuery scopes and paginates lifecycle activity.
type LifecycleActivityQuery struct {
	Accounts map[string]string
	Types    []LifecycleActivityType
	Before   *LifecycleActivityCursor
	Limit    int
}

// ListLifecycleActivity returns lifecycle rows ordered by timestamp and activity ID descending.
func (s *Store) ListLifecycleActivity(ctx context.Context, query LifecycleActivityQuery) ([]LifecycleActivity, error) {
	accountMatch, accountArgs, err := lifecycleAccountMatch(query.Accounts)
	if err != nil {
		return nil, err
	}
	if query.Limit <= 0 {
		return []LifecycleActivity{}, nil
	}

	types := make(map[LifecycleActivityType]bool, len(query.Types))
	for _, activityType := range query.Types {
		switch activityType {
		case LifecycleActivityOpened, LifecycleActivityClosed:
			types[activityType] = true
		default:
			return nil, fmt.Errorf("unsupported lifecycle activity type %q", activityType)
		}
	}
	if len(types) == 0 {
		return nil, fmt.Errorf("at least one lifecycle activity type required")
	}

	var selections []string
	var args []any
	if types[LifecycleActivityOpened] {
		selections = append(selections, `SELECT id || ':opened' AS activity_id, 'opened' AS activity_type,
			opened_at AS activity_at, `+livePositionCols+` FROM live_positions
			WHERE state != 'closed' AND opened_at IS NOT NULL AND (`+accountMatch+`)`)
		args = append(args, accountArgs...)
	}
	if types[LifecycleActivityClosed] {
		selections = append(selections, `SELECT id || ':closed' AS activity_id, 'closed' AS activity_type,
			completed_at AS activity_at, `+livePositionCols+` FROM live_positions
			WHERE state = 'closed' AND completed_at IS NOT NULL AND (`+accountMatch+`)`)
		args = append(args, accountArgs...)
	}

	statement := `SELECT * FROM (` + strings.Join(selections, ` UNION ALL `) + `)`
	if query.Before != nil {
		if query.Before.At == "" || query.Before.ID == "" {
			return nil, fmt.Errorf("incomplete lifecycle activity cursor")
		}
		statement += ` WHERE activity_at < ? OR (activity_at = ? AND activity_id < ?)`
		args = append(args, query.Before.At, query.Before.At, query.Before.ID)
	}
	statement += ` ORDER BY activity_at DESC, activity_id DESC LIMIT ?`
	args = append(args, query.Limit)

	rows, err := s.reader.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	activities := make([]LifecycleActivity, 0, query.Limit)
	for rows.Next() {
		activity, err := scanLifecycleActivity(rows)
		if err != nil {
			return nil, err
		}
		activities = append(activities, activity)
	}
	return activities, rows.Err()
}

func lifecycleAccountMatch(accounts map[string]string) (string, []any, error) {
	if len(accounts) < 2 || len(accounts) > 3 {
		return "", nil, fmt.Errorf("two or three account bindings required")
	}
	venues := make([]string, 0, len(accounts))
	for venue := range accounts {
		venues = append(venues, venue)
	}
	sort.Strings(venues)

	var matches []string
	var args []any
	for i := 0; i < len(venues); i++ {
		for j := i + 1; j < len(venues); j++ {
			pair := map[string]string{
				venues[i]: accounts[venues[i]],
				venues[j]: accounts[venues[j]],
			}
			normalized, _, key, err := canonicalAccountBindings(pair)
			if err != nil {
				return "", nil, err
			}
			matches = append(matches, `account_bindings_key = ?`)
			args = append(args, key)
			if normalized["pacifica"] != "" && normalized["hyperliquid"] != "" {
				matches = append(matches, `(account_bindings_key = '' AND account_pacifica = ? AND account_hyperliquid = ?)`)
				args = append(args, normalized["pacifica"], normalized["hyperliquid"])
			}
		}
	}
	return strings.Join(matches, " OR "), args, nil
}

func scanLifecycleActivity(scanner interface{ Scan(...any) error }) (LifecycleActivity, error) {
	var activity LifecycleActivity
	var position LivePosition
	var bindingsJSON, bindingsKey string
	var openedAt, completedAt, monitorAt sql.NullString
	targets := []any{&activity.ID, &activity.Type, &activity.At}
	targets = append(targets, livePositionScanTargets(
		&position, &bindingsJSON, &bindingsKey, &openedAt, &completedAt, &monitorAt,
	)...)
	if err := scanner.Scan(targets...); err != nil {
		return LifecycleActivity{}, err
	}
	finishLivePositionScan(&position, bindingsJSON, bindingsKey, openedAt, completedAt, monitorAt)
	activity.Position = position
	return activity, nil
}
