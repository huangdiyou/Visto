package media

import (
	"context"
	"errors"
)

var ErrObjectNotFound = errors.New("media object not found")

type Repository interface {
	Get(
		ctx context.Context,
		workspaceID string,
		storageObjectID string,
	) (Metadata, error)
	ListRoot(
		ctx context.Context,
		workspaceID string,
		rootID string,
	) ([]Metadata, error)
	Upsert(ctx context.Context, metadata Metadata) error
}
