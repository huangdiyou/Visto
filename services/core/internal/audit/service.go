package audit

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	defaultListLimit = 100

	AccessEventOpened            = "opened"
	AccessEventVerified          = "verified"
	AccessEventIdentified        = "identified"
	AccessEventViewed            = "viewed"
	AccessEventDownloaded        = "downloaded"
	AccessEventCommented         = "commented"
	AccessEventDecisionSubmitted = "decision_submitted"
)

type Service struct {
	repository Repository
	clock      Clock
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, clock: time.Now}
}

func (service *Service) RecordAccess(
	ctx context.Context,
	input RecordAccessInput,
) (AccessEvent, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ShareID = strings.TrimSpace(input.ShareID)
	input.EventType = strings.TrimSpace(input.EventType)
	if input.WorkspaceID == "" || input.ShareID == "" ||
		!validAccessEvent(input.EventType) {
		return AccessEvent{}, ErrInvalidInput
	}
	id, err := newID()
	if err != nil {
		return AccessEvent{}, err
	}
	now := service.clock().UTC()
	return service.repository.CreateAccess(ctx, accessRecord{
		AccessEvent: AccessEvent{
			ID:               id,
			WorkspaceID:      input.WorkspaceID,
			ShareID:          input.ShareID,
			ShareLinkID:      optionalString(input.ShareLinkID),
			VisitorID:        optionalString(input.VisitorID),
			VisitorSessionID: optionalString(input.VisitorSessionID),
			EventType:        input.EventType,
			ResourceType:     optionalString(input.ResourceType),
			ResourceID:       optionalString(input.ResourceID),
			IPHash:           optionalString(input.IPHash),
			UserAgentSummary: strings.TrimSpace(input.UserAgentSummary),
			OccurredAt:       now,
		},
		MetadataJSON: "{}",
	})
}

func (service *Service) ListAccess(
	ctx context.Context,
	workspaceID string,
	shareID string,
	limit int,
) ([]AccessEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = defaultListLimit
	}
	return service.repository.ListAccess(ctx, workspaceID, shareID, limit)
}

func (service *Service) RecordLog(
	ctx context.Context,
	input RecordLogInput,
) (AuditLog, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ActorType = strings.TrimSpace(input.ActorType)
	input.Action = strings.TrimSpace(input.Action)
	input.ResourceType = strings.TrimSpace(input.ResourceType)
	input.ResourceID = strings.TrimSpace(input.ResourceID)
	if input.WorkspaceID == "" || !validActorType(input.ActorType) ||
		input.Action == "" || input.ResourceType == "" || input.ResourceID == "" {
		return AuditLog{}, ErrInvalidInput
	}
	id, err := newID()
	if err != nil {
		return AuditLog{}, err
	}
	beforeJSON, err := encodeSnapshot(input.Before)
	if err != nil {
		return AuditLog{}, err
	}
	afterJSON, err := encodeSnapshot(input.After)
	if err != nil {
		return AuditLog{}, err
	}
	now := service.clock().UTC()
	return service.repository.CreateLog(ctx, logRecord{
		AuditLog: AuditLog{
			ID:           id,
			WorkspaceID:  input.WorkspaceID,
			ActorType:    input.ActorType,
			ActorID:      optionalString(input.ActorID),
			Action:       input.Action,
			ResourceType: input.ResourceType,
			ResourceID:   input.ResourceID,
			RequestID:    strings.TrimSpace(input.RequestID),
			IPHash:       optionalString(input.IPHash),
			OccurredAt:   now,
		},
		BeforeJSON: beforeJSON,
		AfterJSON:  afterJSON,
	})
}

func (service *Service) ListLogs(
	ctx context.Context,
	workspaceID string,
	filter LogListFilter,
) ([]AuditLog, error) {
	if filter.Limit <= 0 || filter.Limit > 200 {
		filter.Limit = defaultListLimit
	}
	return service.repository.ListLogs(ctx, workspaceID, filter)
}

func validAccessEvent(value string) bool {
	switch value {
	case AccessEventOpened, AccessEventVerified, AccessEventIdentified,
		AccessEventViewed, AccessEventDownloaded, AccessEventCommented,
		AccessEventDecisionSubmitted:
		return true
	default:
		return false
	}
}

func validActorType(value string) bool {
	switch value {
	case "user", "visitor", "system", "node":
		return true
	default:
		return false
	}
}

func encodeSnapshot(value map[string]any) (string, error) {
	if len(value) == 0 {
		return "{}", nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func optionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}
