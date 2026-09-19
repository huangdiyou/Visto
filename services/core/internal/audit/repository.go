package audit

import (
	"context"
	"errors"
	"time"
)

var ErrInvalidInput = errors.New("audit input is invalid")

type accessRecord struct {
	AccessEvent
	MetadataJSON string
}

type logRecord struct {
	AuditLog
	BeforeJSON string
	AfterJSON  string
}

type Repository interface {
	CreateAccess(context.Context, accessRecord) (AccessEvent, error)
	ListAccess(context.Context, string, string, int) ([]AccessEvent, error)
	CreateLog(context.Context, logRecord) (AuditLog, error)
	ListLogs(context.Context, string, LogListFilter) ([]AuditLog, error)
}

type Clock func() time.Time
