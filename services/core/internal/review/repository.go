package review

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound                = errors.New("review session not found")
	ErrNameConflict            = errors.New("review session name already exists")
	ErrRevisionConflict        = errors.New("review session revision conflict")
	ErrInvalidProject          = errors.New("review project is invalid")
	ErrInvalidItem             = errors.New("review item is invalid")
	ErrInvalidState            = errors.New("review session state is invalid")
	ErrInvalidAuthor           = errors.New("review comment author is invalid")
	ErrInvalidAnnotation       = errors.New("review annotation is invalid")
	ErrCommentNotFound         = errors.New("review comment not found")
	ErrCommentForbidden        = errors.New("review comment action is forbidden")
	ErrDecisionForbidden       = errors.New("review decision action is forbidden")
	ErrInvalidParticipant      = errors.New("review participant is invalid")
	ErrAttachmentNotFound      = errors.New("review comment attachment not found")
	ErrInvalidAttachment       = errors.New("review comment attachment is invalid")
	ErrAttachmentQuotaExceeded = errors.New("review comment attachment quota exceeded")
)

type sessionRecord struct {
	Session
	Now time.Time
}

type threadRecord struct {
	CommentThread
	CommentID     string
	Body          string
	AttachmentIDs []string
	Now           time.Time
}

type threadCommentRecord struct {
	WorkspaceID     string
	ReviewSessionID string
	ReviewItemID    string
	ThreadID        string
	CommentID       string
	Author          CommentAuthor
	Body            string
	AttachmentIDs   []string
	Now             time.Time
}

type attachmentRecord struct {
	CommentAttachment
	Now time.Time
}

type decisionRecord struct {
	Decision
	Now time.Time
}

type Repository interface {
	List(context.Context, string, ListFilter) (ListPage, error)
	Get(context.Context, string, string) (Session, error)
	Create(context.Context, sessionRecord) (Session, error)
	Update(context.Context, UpdateSessionInput, time.Time) (Session, error)
	SetStatus(context.Context, SessionStateInput, string, time.Time) (Session, error)
	ListThreads(context.Context, ThreadListFilter) ([]CommentThread, error)
	CreatePendingAttachment(context.Context, attachmentRecord) (CommentAttachment, error)
	ListExpiredPendingAttachments(context.Context, time.Time, int) ([]CommentAttachment, error)
	DeleteExpiredPendingAttachment(context.Context, string, time.Time) error
	CommentAttachment(context.Context, CommentAttachmentLookup) (CommentAttachment, error)
	CreateThread(context.Context, threadRecord) (CommentThread, error)
	AddComment(context.Context, threadCommentRecord) (CommentThread, error)
	UpdateComment(context.Context, UpdateCommentInput, time.Time) (CommentThread, error)
	DeleteComment(context.Context, DeleteCommentInput, time.Time) error
	SetThreadStatus(context.Context, ThreadStateInput, string, time.Time) (CommentThread, error)
	ListDecisions(context.Context, DecisionListFilter) ([]Decision, error)
	CreateDecision(context.Context, decisionRecord) (Decision, error)
}
