package workspace

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound         = errors.New("membership not found")
	ErrInvalidInput     = errors.New("membership input is invalid")
	ErrRevisionConflict = errors.New("membership revision conflict")
	ErrLastOwner        = errors.New("workspace must retain an active owner")
)

type Repository interface {
	ListMemberships(context.Context, string) ([]Membership, error)
	UpdateMembership(
		context.Context,
		UpdateMembershipInput,
		time.Time,
	) (Membership, error)
}
