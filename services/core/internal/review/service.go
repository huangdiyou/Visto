package review

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Service struct {
	repository Repository
	clock      func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, clock: time.Now}
}

func (service *Service) List(
	ctx context.Context,
	workspaceID string,
	filter ListFilter,
) (ListPage, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	filter.AssetVersionID = strings.TrimSpace(filter.AssetVersionID)
	filter.ProjectID = strings.TrimSpace(filter.ProjectID)
	filter.UserID = strings.TrimSpace(filter.UserID)
	if workspaceID == "" || filter.UserID == "" {
		return ListPage{}, errors.New("workspace and list actor are required")
	}
	if filter.At.IsZero() {
		filter.At = service.clock().UTC()
	}
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Limit > 200 {
		filter.Limit = 200
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	return service.repository.List(ctx, workspaceID, filter)
}

func (service *Service) Get(
	ctx context.Context,
	workspaceID string,
	id string,
) (Session, error) {
	return service.repository.Get(ctx, workspaceID, id)
}

func (service *Service) Create(
	ctx context.Context,
	input CreateSessionInput,
) (Session, error) {
	if err := normalizeAndValidateCreate(&input); err != nil {
		return Session{}, err
	}
	id, err := newID()
	if err != nil {
		return Session{}, err
	}
	session := Session{
		ID:                id,
		WorkspaceID:       input.WorkspaceID,
		ProjectID:         input.ProjectID,
		CollectionID:      input.CollectionID,
		Name:              input.Name,
		Status:            "draft",
		DueAt:             input.DueAt,
		TemplateID:        input.TemplateID,
		TemplateRevision:  input.TemplateRevision,
		ResponsibleUserID: input.ResponsibleUserID,
		AllowDownload:     input.AllowDownload,
		DecisionRule:      input.DecisionRule,
		CreatedBy:         input.UserID,
		Revision:          1,
	}
	for index, inputItem := range input.Items {
		itemID, idErr := newID()
		if idErr != nil {
			return Session{}, idErr
		}
		session.Items = append(session.Items, Item{
			ID:             itemID,
			AssetID:        inputItem.AssetID,
			AssetVersionID: inputItem.AssetVersionID,
			Position:       index,
			Status:         "pending",
		})
	}
	for _, inputParticipant := range input.Participants {
		participantID, idErr := newID()
		if idErr != nil {
			return Session{}, idErr
		}
		session.Participants = append(session.Participants, Participant{
			ID:          participantID,
			UserID:      inputParticipant.UserID,
			DisplayName: inputParticipant.DisplayName,
			Role:        inputParticipant.Role,
		})
	}
	return service.repository.Create(ctx, sessionRecord{
		Session: session,
		Now:     service.clock().UTC(),
	})
}

func (service *Service) Update(
	ctx context.Context,
	input UpdateSessionInput,
) (Session, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ID = strings.TrimSpace(input.ID)
	input.ResponsibleUserID = normalizeOptionalID(input.ResponsibleUserID)
	if err := validateName(input.Name); err != nil {
		return Session{}, err
	}
	participants, err := normalizeParticipants(input.Participants)
	if err != nil {
		return Session{}, err
	}
	if input.Revision < 1 {
		return Session{}, errors.New("revision must be positive")
	}
	input.Participants = participants
	return service.repository.Update(ctx, input, service.clock().UTC())
}

func (service *Service) Open(
	ctx context.Context,
	input SessionStateInput,
) (Session, error) {
	if input.Revision < 1 {
		return Session{}, errors.New("revision must be positive")
	}
	return service.repository.SetStatus(ctx, input, "open", service.clock().UTC())
}

func (service *Service) Close(
	ctx context.Context,
	input SessionStateInput,
) (Session, error) {
	if input.Revision < 1 {
		return Session{}, errors.New("revision must be positive")
	}
	return service.repository.SetStatus(ctx, input, "closed", service.clock().UTC())
}

func (service *Service) ListDecisions(
	ctx context.Context,
	filter DecisionListFilter,
) ([]Decision, error) {
	filter.WorkspaceID = strings.TrimSpace(filter.WorkspaceID)
	filter.ReviewSessionID = strings.TrimSpace(filter.ReviewSessionID)
	filter.ReviewItemID = strings.TrimSpace(filter.ReviewItemID)
	if filter.WorkspaceID == "" || filter.ReviewSessionID == "" {
		return nil, errors.New("workspace and review session are required")
	}
	filter.Limit, filter.Offset = normalizeListBounds(
		filter.Limit,
		filter.Offset,
		defaultDecisionListLimit,
		maxDecisionListLimit,
	)
	return service.repository.ListDecisions(ctx, filter)
}

// normalizeListBounds clamps a caller-supplied page size and offset into the
// supported range so unbounded or hostile values cannot stream a whole table.
func normalizeListBounds(limit int, offset int, defaultValue int, maxValue int) (int, int) {
	if limit <= 0 {
		limit = defaultValue
	}
	if limit > maxValue {
		limit = maxValue
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func (service *Service) CreateDecision(
	ctx context.Context,
	input CreateDecisionInput,
) (Decision, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ReviewSessionID = strings.TrimSpace(input.ReviewSessionID)
	input.ReviewItemID = strings.TrimSpace(input.ReviewItemID)
	input.ActorKind = strings.TrimSpace(input.ActorKind)
	input.ActorUserID = strings.TrimSpace(input.ActorUserID)
	input.ActorVisitorID = strings.TrimSpace(input.ActorVisitorID)
	input.Decision = strings.TrimSpace(input.Decision)
	input.Note = strings.TrimSpace(input.Note)
	if input.WorkspaceID == "" || input.ReviewSessionID == "" {
		return Decision{}, errors.New("workspace and review session are required")
	}
	if input.Decision != "approved" &&
		input.Decision != "changes_requested" &&
		input.Decision != "rejected" {
		return Decision{}, errors.New("decision is invalid")
	}
	if len(input.Note) > 4000 {
		return Decision{}, errors.New("decision note is too long")
	}
	if err := validateAuthorFields(
		input.ActorKind,
		input.ActorUserID,
		input.ActorVisitorID,
	); err != nil {
		return Decision{}, err
	}
	id, err := newID()
	if err != nil {
		return Decision{}, err
	}
	var reviewItemID *string
	if input.ReviewItemID != "" {
		reviewItemID = &input.ReviewItemID
	}
	var note *string
	if input.Note != "" {
		note = &input.Note
	}
	return service.repository.CreateDecision(ctx, decisionRecord{
		Decision: Decision{
			ID:              id,
			WorkspaceID:     input.WorkspaceID,
			ReviewSessionID: input.ReviewSessionID,
			ReviewItemID:    reviewItemID,
			Actor: CommentAuthor{
				Kind: input.ActorKind,
				ID: authorIDFromFields(
					input.ActorKind,
					input.ActorUserID,
					input.ActorVisitorID,
				),
			},
			Decision: input.Decision,
			Note:     note,
		},
		Now: service.clock().UTC(),
	})
}

func (service *Service) ListThreads(
	ctx context.Context,
	filter ThreadListFilter,
) ([]CommentThread, error) {
	filter.WorkspaceID = strings.TrimSpace(filter.WorkspaceID)
	filter.ReviewSessionID = strings.TrimSpace(filter.ReviewSessionID)
	filter.ReviewItemID = strings.TrimSpace(filter.ReviewItemID)
	if filter.WorkspaceID == "" || filter.ReviewSessionID == "" {
		return nil, errors.New("workspace and review session are required")
	}
	filter.Limit, filter.Offset = normalizeListBounds(
		filter.Limit,
		filter.Offset,
		defaultThreadListLimit,
		maxThreadListLimit,
	)
	return service.repository.ListThreads(ctx, filter)
}

func (service *Service) CreatePendingAttachment(
	ctx context.Context,
	input CreatePendingAttachmentInput,
) (CommentAttachment, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ReviewSessionID = strings.TrimSpace(input.ReviewSessionID)
	input.ReviewItemID = strings.TrimSpace(input.ReviewItemID)
	input.SourceType = strings.TrimSpace(input.SourceType)
	input.UploadedByUserID = strings.TrimSpace(input.UploadedByUserID)
	input.ShareVisitorID = strings.TrimSpace(input.ShareVisitorID)
	input.AuthorizedRootID = strings.TrimSpace(input.AuthorizedRootID)
	input.ObjectKey = strings.TrimSpace(input.ObjectKey)
	input.OriginalFilename = strings.TrimSpace(input.OriginalFilename)
	input.MIMEType = strings.TrimSpace(input.MIMEType)
	input.UploadSecurityPolicy = strings.TrimSpace(input.UploadSecurityPolicy)
	input.Status = strings.TrimSpace(input.Status)
	input.ResultCode = strings.TrimSpace(input.ResultCode)
	if input.Message != nil {
		value := strings.TrimSpace(*input.Message)
		input.Message = &value
	}
	if input.WorkspaceID == "" ||
		input.ReviewSessionID == "" ||
		input.ReviewItemID == "" ||
		input.AuthorizedRootID == "" ||
		input.ObjectKey == "" ||
		input.OriginalFilename == "" ||
		input.MIMEType == "" {
		return CommentAttachment{}, errors.New("comment attachment is incomplete")
	}
	if input.SizeBytes < 1 {
		return CommentAttachment{}, errors.New("comment attachment is empty")
	}
	if err := validateAuthorFields(
		input.SourceType,
		input.UploadedByUserID,
		input.ShareVisitorID,
	); err != nil {
		return CommentAttachment{}, err
	}
	if input.UploadSecurityPolicy == "" {
		input.UploadSecurityPolicy = "standard"
	}
	if input.UploadSecurityPolicy != "quick" &&
		input.UploadSecurityPolicy != "standard" &&
		input.UploadSecurityPolicy != "enhanced" {
		return CommentAttachment{}, ErrInvalidAttachment
	}
	if input.Status == "" {
		input.Status = "ready"
	}
	if input.Status != "ready" &&
		input.Status != "quarantined" &&
		input.Status != "rejected" {
		return CommentAttachment{}, ErrInvalidAttachment
	}
	id, err := newID()
	if err != nil {
		return CommentAttachment{}, err
	}
	var uploadedByUserID *string
	var shareVisitorID *string
	if input.SourceType == "user" {
		uploadedByUserID = &input.UploadedByUserID
	} else {
		shareVisitorID = &input.ShareVisitorID
	}
	return service.repository.CreatePendingAttachment(ctx, attachmentRecord{
		CommentAttachment: CommentAttachment{
			ID:                   id,
			WorkspaceID:          input.WorkspaceID,
			ReviewSessionID:      input.ReviewSessionID,
			ReviewItemID:         input.ReviewItemID,
			SourceType:           input.SourceType,
			UploadedByUserID:     uploadedByUserID,
			ShareVisitorID:       shareVisitorID,
			AuthorizedRootID:     input.AuthorizedRootID,
			ObjectKey:            input.ObjectKey,
			OriginalFilename:     input.OriginalFilename,
			MIMEType:             input.MIMEType,
			SizeBytes:            input.SizeBytes,
			Width:                input.Width,
			Height:               input.Height,
			UploadSecurityPolicy: input.UploadSecurityPolicy,
			Status:               input.Status,
			ResultCode:           input.ResultCode,
			Message:              input.Message,
		},
		Now: service.clock().UTC(),
	})
}

// ExpiredPendingAttachments returns unattached files that may be removed from
// storage. Deleting storage precedes the row deletion so a transient storage
// failure remains eligible for the next cleanup pass.
func (service *Service) ExpiredPendingAttachments(
	ctx context.Context,
	before time.Time,
	limit int,
) ([]CommentAttachment, error) {
	if limit < 1 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	return service.repository.ListExpiredPendingAttachments(ctx, before.UTC(), limit)
}

func (service *Service) RemoveExpiredPendingAttachment(
	ctx context.Context,
	id string,
	before time.Time,
) error {
	if strings.TrimSpace(id) == "" {
		return ErrAttachmentNotFound
	}
	return service.repository.DeleteExpiredPendingAttachment(ctx, id, before.UTC())
}

func (service *Service) CommentAttachment(
	ctx context.Context,
	input CommentAttachmentLookup,
) (CommentAttachment, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ReviewSessionID = strings.TrimSpace(input.ReviewSessionID)
	input.ReviewItemID = strings.TrimSpace(input.ReviewItemID)
	input.ID = strings.TrimSpace(input.ID)
	input.ActorKind = strings.TrimSpace(input.ActorKind)
	input.ActorUserID = strings.TrimSpace(input.ActorUserID)
	input.ActorVisitorID = strings.TrimSpace(input.ActorVisitorID)
	if input.WorkspaceID == "" ||
		input.ReviewSessionID == "" ||
		input.ReviewItemID == "" ||
		input.ID == "" {
		return CommentAttachment{}, ErrAttachmentNotFound
	}
	if err := validateAuthorFields(
		input.ActorKind,
		input.ActorUserID,
		input.ActorVisitorID,
	); err != nil {
		return CommentAttachment{}, err
	}
	return service.repository.CommentAttachment(ctx, input)
}

func (service *Service) CreateThread(
	ctx context.Context,
	input CreateThreadInput,
) (CommentThread, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ReviewSessionID = strings.TrimSpace(input.ReviewSessionID)
	input.ReviewItemID = strings.TrimSpace(input.ReviewItemID)
	input.AssetVersionID = strings.TrimSpace(input.AssetVersionID)
	input.AuthorKind = strings.TrimSpace(input.AuthorKind)
	input.AuthorUserID = strings.TrimSpace(input.AuthorUserID)
	input.AuthorVisitorID = strings.TrimSpace(input.AuthorVisitorID)
	input.Body = strings.TrimSpace(input.Body)
	attachmentIDs, err := normalizeAttachmentIDs(input.AttachmentIDs)
	if err != nil {
		return CommentThread{}, err
	}
	input.AttachmentIDs = attachmentIDs
	if input.WorkspaceID == "" ||
		input.ReviewSessionID == "" ||
		input.ReviewItemID == "" ||
		input.AssetVersionID == "" {
		return CommentThread{}, errors.New(
			"review session, item, and asset version are required",
		)
	}
	if err := validateCommentBody(input.Body); err != nil {
		return CommentThread{}, err
	}
	if err := validateCommentAuthor(input); err != nil {
		return CommentThread{}, err
	}
	if err := validateAnnotation(&input.Annotation); err != nil {
		return CommentThread{}, err
	}
	threadID, err := newID()
	if err != nil {
		return CommentThread{}, err
	}
	commentID, err := newID()
	if err != nil {
		return CommentThread{}, err
	}
	annotationID, err := newID()
	if err != nil {
		return CommentThread{}, err
	}
	now := service.clock().UTC()
	return service.repository.CreateThread(ctx, threadRecord{
		CommentThread: CommentThread{
			ID:              threadID,
			WorkspaceID:     input.WorkspaceID,
			ReviewSessionID: input.ReviewSessionID,
			ReviewItemID:    input.ReviewItemID,
			AssetVersionID:  input.AssetVersionID,
			Status:          "open",
			Author: CommentAuthor{
				Kind: input.AuthorKind,
				ID:   authorID(input),
			},
			Annotation: Annotation{
				ID:              annotationID,
				Kind:            input.Annotation.Kind,
				TimeStartUs:     input.Annotation.TimeStartUs,
				TimeEndUs:       input.Annotation.TimeEndUs,
				Geometry:        input.Annotation.Geometry,
				GeometryVersion: input.Annotation.GeometryVersion,
			},
			Revision: 1,
		},
		CommentID:     commentID,
		Body:          input.Body,
		AttachmentIDs: input.AttachmentIDs,
		Now:           now,
	})
}

func (service *Service) AddComment(
	ctx context.Context,
	input ThreadCommentInput,
) (CommentThread, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ReviewSessionID = strings.TrimSpace(input.ReviewSessionID)
	input.ReviewItemID = strings.TrimSpace(input.ReviewItemID)
	input.ThreadID = strings.TrimSpace(input.ThreadID)
	input.AuthorKind = strings.TrimSpace(input.AuthorKind)
	input.AuthorUserID = strings.TrimSpace(input.AuthorUserID)
	input.AuthorVisitorID = strings.TrimSpace(input.AuthorVisitorID)
	input.Body = strings.TrimSpace(input.Body)
	attachmentIDs, err := normalizeAttachmentIDs(input.AttachmentIDs)
	if err != nil {
		return CommentThread{}, err
	}
	input.AttachmentIDs = attachmentIDs
	if input.WorkspaceID == "" || input.ReviewSessionID == "" ||
		input.ThreadID == "" {
		return CommentThread{}, errors.New("review session and thread are required")
	}
	if err := validateCommentBody(input.Body); err != nil {
		return CommentThread{}, err
	}
	if err := validateAuthorFields(
		input.AuthorKind,
		input.AuthorUserID,
		input.AuthorVisitorID,
	); err != nil {
		return CommentThread{}, err
	}
	commentID, err := newID()
	if err != nil {
		return CommentThread{}, err
	}
	return service.repository.AddComment(ctx, threadCommentRecord{
		WorkspaceID:     input.WorkspaceID,
		ReviewSessionID: input.ReviewSessionID,
		ReviewItemID:    input.ReviewItemID,
		ThreadID:        input.ThreadID,
		CommentID:       commentID,
		Author: CommentAuthor{
			Kind: input.AuthorKind,
			ID: authorIDFromFields(
				input.AuthorKind,
				input.AuthorUserID,
				input.AuthorVisitorID,
			),
		},
		Body:          input.Body,
		AttachmentIDs: input.AttachmentIDs,
		Now:           service.clock().UTC(),
	})
}

func (service *Service) UpdateComment(
	ctx context.Context,
	input UpdateCommentInput,
) (CommentThread, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ReviewSessionID = strings.TrimSpace(input.ReviewSessionID)
	input.ReviewItemID = strings.TrimSpace(input.ReviewItemID)
	input.ThreadID = strings.TrimSpace(input.ThreadID)
	input.CommentID = strings.TrimSpace(input.CommentID)
	input.AuthorKind = strings.TrimSpace(input.AuthorKind)
	input.AuthorUserID = strings.TrimSpace(input.AuthorUserID)
	input.AuthorVisitorID = strings.TrimSpace(input.AuthorVisitorID)
	input.Body = strings.TrimSpace(input.Body)
	if input.WorkspaceID == "" || input.ReviewSessionID == "" ||
		input.ThreadID == "" || input.CommentID == "" {
		return CommentThread{}, errors.New(
			"review session, thread, and comment are required",
		)
	}
	if err := validateCommentBody(input.Body); err != nil {
		return CommentThread{}, err
	}
	if err := validateAuthorFields(
		input.AuthorKind,
		input.AuthorUserID,
		input.AuthorVisitorID,
	); err != nil {
		return CommentThread{}, err
	}
	return service.repository.UpdateComment(ctx, input, service.clock().UTC())
}

func (service *Service) DeleteComment(
	ctx context.Context,
	input DeleteCommentInput,
) error {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ReviewSessionID = strings.TrimSpace(input.ReviewSessionID)
	input.ReviewItemID = strings.TrimSpace(input.ReviewItemID)
	input.ThreadID = strings.TrimSpace(input.ThreadID)
	input.CommentID = strings.TrimSpace(input.CommentID)
	input.AuthorKind = strings.TrimSpace(input.AuthorKind)
	input.AuthorUserID = strings.TrimSpace(input.AuthorUserID)
	input.AuthorVisitorID = strings.TrimSpace(input.AuthorVisitorID)
	if input.WorkspaceID == "" || input.ReviewSessionID == "" ||
		input.ThreadID == "" || input.CommentID == "" {
		return errors.New("review session, thread, and comment are required")
	}
	if err := validateAuthorFields(
		input.AuthorKind,
		input.AuthorUserID,
		input.AuthorVisitorID,
	); err != nil {
		return err
	}
	return service.repository.DeleteComment(ctx, input, service.clock().UTC())
}

func (service *Service) ResolveThread(
	ctx context.Context,
	input ThreadStateInput,
) (CommentThread, error) {
	return service.setThreadStatus(ctx, input, "resolved")
}

func (service *Service) ReopenThread(
	ctx context.Context,
	input ThreadStateInput,
) (CommentThread, error) {
	return service.setThreadStatus(ctx, input, "open")
}

func (service *Service) setThreadStatus(
	ctx context.Context,
	input ThreadStateInput,
	status string,
) (CommentThread, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ReviewSessionID = strings.TrimSpace(input.ReviewSessionID)
	input.ThreadID = strings.TrimSpace(input.ThreadID)
	input.UserID = strings.TrimSpace(input.UserID)
	if input.WorkspaceID == "" || input.ReviewSessionID == "" ||
		input.ThreadID == "" || input.UserID == "" {
		return CommentThread{}, errors.New("review session, thread, and user are required")
	}
	if input.Revision < 1 {
		return CommentThread{}, errors.New("revision must be positive")
	}
	return service.repository.SetThreadStatus(
		ctx,
		input,
		status,
		service.clock().UTC(),
	)
}

func validateCommentAuthor(input CreateThreadInput) error {
	return validateAuthorFields(
		input.AuthorKind,
		input.AuthorUserID,
		input.AuthorVisitorID,
	)
}

func validateAuthorFields(kind string, userID string, visitorID string) error {
	switch kind {
	case "user":
		if userID == "" || visitorID != "" {
			return ErrInvalidAuthor
		}
	case "share_visitor":
		if visitorID == "" || userID != "" {
			return ErrInvalidAuthor
		}
	default:
		return ErrInvalidAuthor
	}
	return nil
}

func authorID(input CreateThreadInput) string {
	return authorIDFromFields(
		input.AuthorKind,
		input.AuthorUserID,
		input.AuthorVisitorID,
	)
}

func authorIDFromFields(kind string, userID string, visitorID string) string {
	if kind == "user" {
		return userID
	}
	return visitorID
}

func validateCommentBody(value string) error {
	if length := len([]rune(value)); length < 1 || length > 4000 {
		return errors.New("comment body must contain 1 to 4000 characters")
	}
	return nil
}

func normalizeAttachmentIDs(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) > 4 {
		return nil, errors.New("comment cannot contain more than 4 attachments")
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil, ErrInvalidAttachment
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result, nil
}

func validateAnnotation(input *AnnotationInput) error {
	input.Kind = strings.TrimSpace(input.Kind)
	if input.GeometryVersion == 0 {
		input.GeometryVersion = 1
	}
	if input.GeometryVersion != 1 {
		return ErrInvalidAnnotation
	}
	switch input.Kind {
	case "time_point":
		if input.TimeStartUs == nil || *input.TimeStartUs < 0 ||
			input.TimeEndUs != nil || input.Geometry != nil {
			return ErrInvalidAnnotation
		}
	case "time_range":
		if input.TimeStartUs == nil || input.TimeEndUs == nil ||
			*input.TimeStartUs < 0 ||
			*input.TimeEndUs <= *input.TimeStartUs ||
			input.Geometry != nil {
			return ErrInvalidAnnotation
		}
	case "point":
		if input.TimeStartUs != nil || input.TimeEndUs != nil ||
			!validPointGeometry(input.Geometry) {
			return ErrInvalidAnnotation
		}
	case "region":
		if input.TimeStartUs != nil || input.TimeEndUs != nil ||
			!validRegionGeometry(input.Geometry) {
			return ErrInvalidAnnotation
		}
	case "drawing":
		if input.TimeStartUs != nil || input.TimeEndUs != nil ||
			!validDrawingGeometry(input.Geometry) {
			return ErrInvalidAnnotation
		}
	default:
		return ErrInvalidAnnotation
	}
	return nil
}

func validPointGeometry(item *AnnotationGeometry) bool {
	return item != nil &&
		item.Shape == "point" &&
		validNormalized(item.X) &&
		validNormalized(item.Y) &&
		item.Width == nil &&
		item.Height == nil &&
		len(item.Elements) == 0
}

func validRegionGeometry(item *AnnotationGeometry) bool {
	if item == nil ||
		item.Shape != "rect" ||
		!validNormalized(item.X) ||
		!validNormalized(item.Y) ||
		item.Width == nil ||
		item.Height == nil ||
		!validPositiveNormalized(*item.Width) ||
		!validPositiveNormalized(*item.Height) ||
		len(item.Elements) != 0 {
		return false
	}
	const epsilon = 1e-9
	return item.X+*item.Width <= 1+epsilon &&
		item.Y+*item.Height <= 1+epsilon
}

func validDrawingGeometry(item *AnnotationGeometry) bool {
	if item == nil || item.Shape != "drawing" ||
		item.Width != nil || item.Height != nil ||
		len(item.Elements) == 0 || len(item.Elements) > 20 {
		return false
	}
	for _, element := range item.Elements {
		if !validDrawingElement(element) {
			return false
		}
	}
	return true
}

func validDrawingElement(item AnnotationDrawingElement) bool {
	if !validDrawingColor(item.Color) ||
		math.IsNaN(item.StrokeWidth) ||
		math.IsInf(item.StrokeWidth, 0) ||
		item.StrokeWidth < 1 ||
		item.StrokeWidth > 24 {
		return false
	}
	switch item.Tool {
	case "brush":
		if len(item.Points) < 2 || len(item.Points) > 200 {
			return false
		}
		for _, point := range item.Points {
			if !validDrawingPoint(point) {
				return false
			}
		}
		return true
	case "arrow":
		if len(item.Points) != 2 {
			return false
		}
		return validDrawingPoint(item.Points[0]) &&
			validDrawingPoint(item.Points[1])
	case "rect":
		if item.X == nil ||
			item.Y == nil ||
			item.Width == nil ||
			item.Height == nil ||
			!validNormalized(*item.X) ||
			!validNormalized(*item.Y) ||
			!validPositiveNormalized(*item.Width) ||
			!validPositiveNormalized(*item.Height) {
			return false
		}
		const epsilon = 1e-9
		return *item.X+*item.Width <= 1+epsilon &&
			*item.Y+*item.Height <= 1+epsilon
	default:
		return false
	}
}

func validDrawingPoint(item AnnotationPoint) bool {
	return validNormalized(item.X) && validNormalized(item.Y)
}

func validDrawingColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, char := range value[1:] {
		if (char < '0' || char > '9') &&
			(char < 'a' || char > 'f') &&
			(char < 'A' || char > 'F') {
			return false
		}
	}
	return true
}

func validNormalized(value float64) bool {
	return !math.IsNaN(value) &&
		!math.IsInf(value, 0) &&
		value >= 0 &&
		value <= 1
}

func validPositiveNormalized(value float64) bool {
	return validNormalized(value) && value > 0
}

func normalizeAndValidateCreate(input *CreateSessionInput) error {
	input.Name = strings.TrimSpace(input.Name)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.UserID = strings.TrimSpace(input.UserID)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.TemplateID = normalizeOptionalID(input.TemplateID)
	input.ResponsibleUserID = normalizeOptionalID(input.ResponsibleUserID)
	input.DecisionRule = strings.TrimSpace(input.DecisionRule)
	if input.DecisionRule == "" {
		input.DecisionRule = "any_reviewer"
	}
	if input.DecisionRule != "any_reviewer" &&
		input.DecisionRule != "all_reviewers" &&
		input.DecisionRule != "responsible_only" {
		return errors.New("review decision rule is invalid")
	}
	if (input.TemplateID == nil) != (input.TemplateRevision == nil) {
		return errors.New("review template snapshot is incomplete")
	}
	if input.TemplateRevision != nil && *input.TemplateRevision < 1 {
		return errors.New("review template revision must be positive")
	}
	if err := validateName(input.Name); err != nil {
		return err
	}
	if input.ProjectID == "" {
		return errors.New("project is required")
	}
	if len(input.Items) == 0 || len(input.Items) > 100 {
		return errors.New("review must contain between 1 and 100 items")
	}
	seenVersions := make(map[string]struct{}, len(input.Items))
	for index := range input.Items {
		input.Items[index].AssetID = strings.TrimSpace(input.Items[index].AssetID)
		input.Items[index].AssetVersionID = strings.TrimSpace(
			input.Items[index].AssetVersionID,
		)
		if input.Items[index].AssetID == "" ||
			input.Items[index].AssetVersionID == "" {
			return errors.New("asset and asset version are required")
		}
		if _, exists := seenVersions[input.Items[index].AssetVersionID]; exists {
			return errors.New("review contains a duplicate asset version")
		}
		seenVersions[input.Items[index].AssetVersionID] = struct{}{}
	}
	participants, err := normalizeParticipants(input.Participants)
	if err != nil {
		return err
	}
	input.Participants = participants
	return nil
}

func normalizeParticipants(input []ParticipantInput) ([]ParticipantInput, error) {
	if len(input) > 50 {
		return nil, errors.New("review cannot contain more than 50 participants")
	}
	result := make([]ParticipantInput, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	seenUsers := make(map[string]struct{}, len(input))
	for _, participant := range input {
		participant.UserID = normalizeOptionalID(participant.UserID)
		participant.DisplayName = strings.TrimSpace(participant.DisplayName)
		participant.Role = strings.TrimSpace(participant.Role)
		if participant.DisplayName == "" || len([]rune(participant.DisplayName)) > 80 {
			return nil, errors.New("participant name must contain 1 to 80 characters")
		}
		if participant.Role == "" {
			participant.Role = "reviewer"
		}
		if participant.Role != "reviewer" && participant.Role != "observer" {
			return nil, errors.New("participant role is invalid")
		}
		key := strings.ToLower(participant.DisplayName)
		if _, exists := seen[key]; exists {
			return nil, errors.New("participant names must be unique")
		}
		seen[key] = struct{}{}
		if participant.UserID != nil {
			if _, exists := seenUsers[*participant.UserID]; exists {
				return nil, errors.New("participant users must be unique")
			}
			seenUsers[*participant.UserID] = struct{}{}
		}
		result = append(result, participant)
	}
	return result, nil
}

func normalizeOptionalID(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func validateName(value string) error {
	length := len([]rune(value))
	if length < 1 || length > 120 {
		return errors.New("review name must contain 1 to 120 characters")
	}
	return nil
}

func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}
