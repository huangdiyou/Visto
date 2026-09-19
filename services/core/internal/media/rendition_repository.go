package media

import (
	"context"
	"errors"
	"time"
)

var ErrRenditionNotFound = errors.New("rendition not found")

type RenditionRepository interface {
	TargetRoot(
		ctx context.Context,
		workspaceID string,
		sourceStorageObjectID string,
	) (*string, error)
	ListRoot(
		ctx context.Context,
		workspaceID string,
		rootID string,
	) ([]Rendition, error)
	Get(
		ctx context.Context,
		workspaceID string,
		renditionID string,
	) (Rendition, error)
	Begin(
		ctx context.Context,
		rendition Rendition,
		now time.Time,
	) (Rendition, error)
	Ready(
		ctx context.Context,
		renditionID string,
		result RenditionResult,
		now time.Time,
	) error
	Fail(
		ctx context.Context,
		renditionID string,
		code string,
		message string,
		now time.Time,
	) error
	Segment(
		ctx context.Context,
		workspaceID string,
		renditionID string,
		fileName string,
	) (RenditionSegment, error)
}
