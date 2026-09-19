package projectaccess

import (
	"context"
	"time"
)

type Repository interface {
	ActiveMembership(
		ctx context.Context,
		workspaceID string,
		projectID string,
		userID string,
		now time.Time,
	) (Membership, error)
}
