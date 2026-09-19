package audit

import "time"

type AccessEvent struct {
	ID               string
	WorkspaceID      string
	ShareID          string
	ShareLinkID      *string
	VisitorID        *string
	VisitorSessionID *string
	VisitorName      string
	EventType        string
	ResourceType     *string
	ResourceID       *string
	IPHash           *string
	UserAgentSummary string
	OccurredAt       time.Time
}

type AuditLog struct {
	ID           string
	WorkspaceID  string
	ActorType    string
	ActorID      *string
	ActorName    string
	Action       string
	ResourceType string
	ResourceID   string
	RequestID    string
	IPHash       *string
	Details      map[string]string
	OccurredAt   time.Time
}

type RecordAccessInput struct {
	WorkspaceID      string
	ShareID          string
	ShareLinkID      string
	VisitorID        string
	VisitorSessionID string
	EventType        string
	ResourceType     string
	ResourceID       string
	IPHash           string
	UserAgentSummary string
}

type RecordLogInput struct {
	WorkspaceID  string
	ActorType    string
	ActorID      string
	Action       string
	ResourceType string
	ResourceID   string
	RequestID    string
	Before       map[string]any
	After        map[string]any
	IPHash       string
}

type LogListFilter struct {
	Limit        int
	ActorID      string
	Action       string
	ResourceType string
	ResourceID   string
}
