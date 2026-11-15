package domain

import "context"

// The review queue's selection logic is the mc/dispatch package; the domain
// owns only the occupancy read the packet birth checks against.

// Occupancy counts the unarchived packets of one Project; the cap applies
// per Project. A packet whose merge is pending still counts: the slot frees
// when the merge is applied, never at approve time.
func Occupancy(ctx context.Context, q Q, project string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM tasks WHERE archived = 0 AND review_slot_held = 1 AND project = ?`, project).Scan(&n)
	return n, err
}
