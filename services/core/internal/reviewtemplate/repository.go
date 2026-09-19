package reviewtemplate

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound         = errors.New("review template not found")
	ErrInvalidInput     = errors.New("review template input is invalid")
	ErrRevisionConflict = errors.New("review template revision conflict")
	ErrNameConflict     = errors.New("review template name already exists")
)

type Repository interface {
	List(context.Context, string) ([]Template, error)
	Get(context.Context, string, string) (Template, error)
	Create(context.Context, Template, time.Time) (Template, error)
	Update(context.Context, UpdateInput, time.Time) (Template, error)
	Delete(context.Context, DeleteInput, time.Time) error
}
