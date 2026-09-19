package httpapi

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/notification"
	"review-studio.local/core/internal/projectaccess"
	reviewdomain "review-studio.local/core/internal/review"
	sharedomain "review-studio.local/core/internal/share"
)

// requestListBounds reads optional bounded pagination from the query string.
// Omitted or malformed values fall through to the review service's defaults,
// which clamp every page into the supported maximum.
func requestListBounds(request *http.Request) (int, int) {
	limit := 0
	if raw := strings.TrimSpace(request.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	offset := 0
	if raw := strings.TrimSpace(request.URL.Query().Get("offset")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			offset = parsed
		}
	}
	return limit, offset
}

type annotationRequest struct {
	Kind            string                           `json:"kind"`
	TimeStartUs     *int64                           `json:"timeStartUs"`
	TimeEndUs       *int64                           `json:"timeEndUs"`
	Geometry        *reviewdomain.AnnotationGeometry `json:"geometry"`
	GeometryVersion int                              `json:"geometryVersion"`
}

type commentThreadRequest struct {
	ReviewItemID   string            `json:"reviewItemId"`
	AssetVersionID string            `json:"assetVersionId"`
	Body           string            `json:"body"`
	Annotation     annotationRequest `json:"annotation"`
	AttachmentIDs  []string          `json:"attachmentIds"`
}

type commentBodyRequest struct {
	Body          string   `json:"body"`
	AttachmentIDs []string `json:"attachmentIds"`
}

type commentAuthorResponse struct {
	Kind        string `json:"kind"`
	DisplayName string `json:"displayName"`
}

type annotationResponse struct {
	Kind            string                           `json:"kind"`
	TimeStartUs     *int64                           `json:"timeStartUs"`
	TimeEndUs       *int64                           `json:"timeEndUs"`
	Geometry        *reviewdomain.AnnotationGeometry `json:"geometry"`
	GeometryVersion int                              `json:"geometryVersion"`
}

type commentResponse struct {
	ID          string                      `json:"id"`
	Author      commentAuthorResponse       `json:"author"`
	Body        string                      `json:"body"`
	Attachments []commentAttachmentResponse `json:"attachments"`
	EditedAt    *time.Time                  `json:"editedAt"`
	CreatedAt   time.Time                   `json:"createdAt"`
	CanEdit     bool                        `json:"canEdit"`
	CanDelete   bool                        `json:"canDelete"`
}

type commentAttachmentResponse struct {
	ID               string    `json:"id"`
	OriginalFilename string    `json:"originalFilename"`
	MIMEType         string    `json:"mimeType"`
	SizeBytes        int64     `json:"sizeBytes"`
	Width            *int      `json:"width"`
	Height           *int      `json:"height"`
	URL              string    `json:"url"`
	CreatedAt        time.Time `json:"createdAt"`
}

type commentThreadResponse struct {
	ID             string                 `json:"id"`
	ReviewItemID   string                 `json:"reviewItemId"`
	AssetVersionID string                 `json:"assetVersionId"`
	Status         string                 `json:"status"`
	Author         commentAuthorResponse  `json:"author"`
	ResolvedBy     *commentAuthorResponse `json:"resolvedBy"`
	ResolvedAt     *time.Time             `json:"resolvedAt"`
	Annotation     annotationResponse     `json:"annotation"`
	Comments       []commentResponse      `json:"comments"`
	Revision       int                    `json:"revision"`
	CreatedAt      time.Time              `json:"createdAt"`
	UpdatedAt      time.Time              `json:"updatedAt"`
	CanReply       bool                   `json:"canReply"`
	CanResolve     bool                   `json:"canResolve"`
	CanReopen      bool                   `json:"canReopen"`
}

type commentThreadListResponse struct {
	Items []commentThreadResponse `json:"items"`
}

type commentResponseOptions struct {
	ActorKind    string
	ActorID      string
	PublicItemID string
	Manager      bool
	CanReply     bool
}

func (h *handler) handleListReviewThreads(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	if _, ok := h.requireReviewSessionProjectPermission(
		response,
		request,
		session,
		request.PathValue("reviewId"),
		projectaccess.PermissionProjectRead,
	); !ok {
		return
	}
	limit, offset := requestListBounds(request)
	items, err := h.reviews.ListThreads(request.Context(), reviewdomain.ThreadListFilter{
		WorkspaceID:     session.Workspace.ID,
		ReviewSessionID: request.PathValue("reviewId"),
		ReviewItemID:    strings.TrimSpace(request.URL.Query().Get("reviewItemId")),
		Limit:           limit,
		Offset:          offset,
	})
	if err != nil {
		h.handleReviewError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, toCommentThreadListResponse(
		items,
		commentResponseOptions{
			ActorKind: "user",
			ActorID:   session.User.ID,
			Manager:   true,
			CanReply:  true,
		},
	))
}

func (h *handler) handleCreateReviewThread(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	var body commentThreadRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	if _, ok := h.requireReviewSessionProjectPermission(
		response,
		request,
		session,
		request.PathValue("reviewId"),
		projectaccess.PermissionReviewsComment,
	); !ok {
		return
	}
	item, err := h.reviews.CreateThread(request.Context(), reviewdomain.CreateThreadInput{
		WorkspaceID:     session.Workspace.ID,
		ReviewSessionID: request.PathValue("reviewId"),
		ReviewItemID:    body.ReviewItemID,
		AssetVersionID:  body.AssetVersionID,
		AuthorKind:      "user",
		AuthorUserID:    session.User.ID,
		Body:            body.Body,
		Annotation:      toAnnotationInput(body.Annotation),
		AttachmentIDs:   body.AttachmentIDs,
	})
	if err != nil {
		h.handleReviewError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "review.thread_created",
		ResourceType: "comment_thread",
		ResourceID:   item.ID,
		After: map[string]any{
			"reviewSessionId": item.ReviewSessionID,
			"reviewItemId":    item.ReviewItemID,
			"annotationKind":  item.Annotation.Kind,
		},
	})
	writeJSON(response, http.StatusCreated, toCommentThreadResponse(
		item,
		commentResponseOptions{
			ActorKind: "user",
			ActorID:   session.User.ID,
			Manager:   true,
			CanReply:  true,
		},
	))
}

func (h *handler) handleAddReviewThreadComment(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	var body commentBodyRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	if _, ok := h.requireReviewSessionProjectPermission(
		response,
		request,
		session,
		request.PathValue("reviewId"),
		projectaccess.PermissionReviewsComment,
	); !ok {
		return
	}
	item, err := h.reviews.AddComment(request.Context(), reviewdomain.ThreadCommentInput{
		WorkspaceID:     session.Workspace.ID,
		ReviewSessionID: request.PathValue("reviewId"),
		ThreadID:        request.PathValue("threadId"),
		AuthorKind:      "user",
		AuthorUserID:    session.User.ID,
		Body:            body.Body,
		AttachmentIDs:   body.AttachmentIDs,
	})
	if err != nil {
		h.handleReviewError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "review.comment_added",
		ResourceType: "comment_thread",
		ResourceID:   item.ID,
	})
	writeJSON(response, http.StatusCreated, toCommentThreadResponse(
		item,
		commentResponseOptions{
			ActorKind: "user",
			ActorID:   session.User.ID,
			Manager:   true,
			CanReply:  true,
		},
	))
}

func (h *handler) handleUpdateReviewComment(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	var body commentBodyRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	if _, ok := h.requireReviewSessionProjectPermission(
		response,
		request,
		session,
		request.PathValue("reviewId"),
		projectaccess.PermissionReviewsComment,
	); !ok {
		return
	}
	item, err := h.reviews.UpdateComment(request.Context(), reviewdomain.UpdateCommentInput{
		WorkspaceID:     session.Workspace.ID,
		ReviewSessionID: request.PathValue("reviewId"),
		ThreadID:        request.PathValue("threadId"),
		CommentID:       request.PathValue("commentId"),
		AuthorKind:      "user",
		AuthorUserID:    session.User.ID,
		Body:            body.Body,
	})
	if err != nil {
		h.handleReviewError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "review.comment_updated",
		ResourceType: "comment",
		ResourceID:   request.PathValue("commentId"),
	})
	writeJSON(response, http.StatusOK, toCommentThreadResponse(
		item,
		commentResponseOptions{
			ActorKind: "user",
			ActorID:   session.User.ID,
			Manager:   true,
			CanReply:  true,
		},
	))
}

func (h *handler) handleDeleteReviewComment(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	if _, ok := h.requireReviewSessionProjectPermission(
		response,
		request,
		session,
		request.PathValue("reviewId"),
		projectaccess.PermissionReviewsComment,
	); !ok {
		return
	}
	err := h.reviews.DeleteComment(request.Context(), reviewdomain.DeleteCommentInput{
		WorkspaceID:     session.Workspace.ID,
		ReviewSessionID: request.PathValue("reviewId"),
		ThreadID:        request.PathValue("threadId"),
		CommentID:       request.PathValue("commentId"),
		AuthorKind:      "user",
		AuthorUserID:    session.User.ID,
	})
	if err != nil {
		h.handleReviewError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "review.comment_deleted",
		ResourceType: "comment",
		ResourceID:   request.PathValue("commentId"),
	})
	response.WriteHeader(http.StatusNoContent)
}

func (h *handler) handleResolveReviewThread(
	response http.ResponseWriter,
	request *http.Request,
) {
	h.handleReviewThreadState(response, request, "resolved")
}

func (h *handler) handleReopenReviewThread(
	response http.ResponseWriter,
	request *http.Request,
) {
	h.handleReviewThreadState(response, request, "open")
}

func (h *handler) handleReviewThreadState(
	response http.ResponseWriter,
	request *http.Request,
	status string,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	var body revisionRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	if _, ok := h.requireReviewSessionProjectPermission(
		response,
		request,
		session,
		request.PathValue("reviewId"),
		projectaccess.PermissionReviewsComment,
	); !ok {
		return
	}
	input := reviewdomain.ThreadStateInput{
		WorkspaceID:     session.Workspace.ID,
		ReviewSessionID: request.PathValue("reviewId"),
		ThreadID:        request.PathValue("threadId"),
		UserID:          session.User.ID,
		Revision:        body.Revision,
	}
	var item reviewdomain.CommentThread
	var err error
	if status == "resolved" {
		item, err = h.reviews.ResolveThread(request.Context(), input)
	} else {
		item, err = h.reviews.ReopenThread(request.Context(), input)
	}
	if err != nil {
		h.handleReviewError(response, request, err)
		return
	}
	action := "review.thread_reopened"
	if status == "resolved" {
		action = "review.thread_resolved"
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       action,
		ResourceType: "comment_thread",
		ResourceID:   item.ID,
	})
	writeJSON(response, http.StatusOK, toCommentThreadResponse(
		item,
		commentResponseOptions{
			ActorKind: "user",
			ActorID:   session.User.ID,
			Manager:   true,
			CanReply:  true,
		},
	))
}

func (h *handler) handleListPublicThreads(
	response http.ResponseWriter,
	request *http.Request,
) {
	share, publicItem, ok := h.publicShareItem(response, request)
	if !ok {
		return
	}
	limit, offset := requestListBounds(request)
	items, err := h.reviews.ListThreads(request.Context(), reviewdomain.ThreadListFilter{
		WorkspaceID:     share.WorkspaceID,
		ReviewSessionID: share.ReviewSessionID,
		ReviewItemID:    publicItem.ID,
		Limit:           limit,
		Offset:          offset,
	})
	if err != nil {
		h.handleReviewError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, toCommentThreadListResponse(
		items,
		commentResponseOptions{
			ActorKind:    "share_visitor",
			ActorID:      share.Visitor.ID,
			PublicItemID: publicItem.ID,
			CanReply:     share.AllowComment,
		},
	))
}

func (h *handler) handleCreatePublicThread(
	response http.ResponseWriter,
	request *http.Request,
) {
	share, publicItem, ok := h.publicShareItem(response, request)
	if !ok {
		return
	}
	if !share.AllowComment {
		h.handlePublicShareError(response, request, sharedomain.ErrCommentDenied)
		return
	}
	if share.RequireNickname && !share.Visitor.Identified {
		h.handlePublicShareError(response, request, sharedomain.ErrIdentityRequired)
		return
	}
	if share.Visitor.ID == "" {
		h.handlePublicShareError(response, request, sharedomain.ErrSessionNotFound)
		return
	}
	// spend the tiered guest text-change budget before the request
	// body is read and before any write, audit record, or notification.
	if !h.consumeGuestTextChangeBudget(
		response,
		request,
		share.ID,
		share.Visitor.ID,
	) {
		return
	}
	var body commentThreadRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	item, err := h.reviews.CreateThread(request.Context(), reviewdomain.CreateThreadInput{
		WorkspaceID:     share.WorkspaceID,
		ReviewSessionID: share.ReviewSessionID,
		ReviewItemID:    publicItem.ID,
		AssetVersionID:  publicItem.AssetVersionID,
		AuthorKind:      "share_visitor",
		AuthorVisitorID: share.Visitor.ID,
		Body:            body.Body,
		Annotation:      toAnnotationInput(body.Annotation),
		AttachmentIDs:   body.AttachmentIDs,
	})
	if err != nil {
		h.handleReviewError(response, request, err)
		return
	}
	h.recordAccess(request, share, "commented", "comment_thread", item.ID)
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  share.WorkspaceID,
		ActorType:    "visitor",
		ActorID:      share.Visitor.ID,
		Action:       "review.thread_created",
		ResourceType: "comment_thread",
		ResourceID:   item.ID,
		After: map[string]any{
			"reviewSessionId": item.ReviewSessionID,
			"reviewItemId":    item.ReviewItemID,
			"annotationKind":  item.Annotation.Kind,
		},
	})
	visitorName := publicVisitorName(share)
	h.notifyWorkspaceManagers(request, notification.CreateForWorkspaceInput{
		WorkspaceID:  share.WorkspaceID,
		Type:         "review.thread_created",
		ResourceType: "review_session",
		ResourceID:   share.ReviewSessionID,
		Title:        "访客提交了新反馈",
		Body: fmt.Sprintf(
			"%s 在“%s”中创建了一条反馈。",
			visitorName,
			share.ReviewName,
		),
	})
	writeJSON(response, http.StatusCreated, toCommentThreadResponse(
		item,
		commentResponseOptions{
			ActorKind:    "share_visitor",
			ActorID:      share.Visitor.ID,
			PublicItemID: publicItem.ID,
			CanReply:     share.AllowComment,
		},
	))
}

func (h *handler) handleAddPublicThreadComment(
	response http.ResponseWriter,
	request *http.Request,
) {
	share, publicItem, ok := h.publicShareItem(response, request)
	if !ok {
		return
	}
	if !share.AllowComment {
		h.handlePublicShareError(response, request, sharedomain.ErrCommentDenied)
		return
	}
	if share.RequireNickname && !share.Visitor.Identified {
		h.handlePublicShareError(response, request, sharedomain.ErrIdentityRequired)
		return
	}
	if share.Visitor.ID == "" {
		h.handlePublicShareError(response, request, sharedomain.ErrSessionNotFound)
		return
	}
	// spend the tiered guest text-change budget before the request
	// body is read and before any write, audit record, or notification.
	if !h.consumeGuestTextChangeBudget(
		response,
		request,
		share.ID,
		share.Visitor.ID,
	) {
		return
	}
	var body commentBodyRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	item, err := h.reviews.AddComment(request.Context(), reviewdomain.ThreadCommentInput{
		WorkspaceID:     share.WorkspaceID,
		ReviewSessionID: share.ReviewSessionID,
		ReviewItemID:    publicItem.ID,
		ThreadID:        request.PathValue("threadId"),
		AuthorKind:      "share_visitor",
		AuthorVisitorID: share.Visitor.ID,
		Body:            body.Body,
		AttachmentIDs:   body.AttachmentIDs,
	})
	if err != nil {
		h.handleReviewError(response, request, err)
		return
	}
	h.recordAccess(request, share, "commented", "comment_thread", item.ID)
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  share.WorkspaceID,
		ActorType:    "visitor",
		ActorID:      share.Visitor.ID,
		Action:       "review.comment_added",
		ResourceType: "comment_thread",
		ResourceID:   item.ID,
	})
	h.notifyWorkspaceManagers(request, notification.CreateForWorkspaceInput{
		WorkspaceID:  share.WorkspaceID,
		Type:         "review.comment_added",
		ResourceType: "review_session",
		ResourceID:   share.ReviewSessionID,
		Title:        "访客回复了反馈",
		Body: fmt.Sprintf(
			"%s 在“%s”中回复了一条反馈。",
			publicVisitorName(share),
			share.ReviewName,
		),
	})
	writeJSON(response, http.StatusCreated, toCommentThreadResponse(
		item,
		commentResponseOptions{
			ActorKind:    "share_visitor",
			ActorID:      share.Visitor.ID,
			PublicItemID: publicItem.ID,
			CanReply:     share.AllowComment,
		},
	))
}

func (h *handler) handleUpdatePublicComment(
	response http.ResponseWriter,
	request *http.Request,
) {
	share, publicItem, ok := h.publicShareItem(response, request)
	if !ok {
		return
	}
	if !share.AllowComment {
		h.handlePublicShareError(response, request, sharedomain.ErrCommentDenied)
		return
	}
	if share.RequireNickname && !share.Visitor.Identified {
		h.handlePublicShareError(response, request, sharedomain.ErrIdentityRequired)
		return
	}
	if share.Visitor.ID == "" {
		h.handlePublicShareError(response, request, sharedomain.ErrSessionNotFound)
		return
	}
	// spend the tiered guest text-change budget before the request
	// body is read and before any write, audit record, or notification.
	if !h.consumeGuestTextChangeBudget(
		response,
		request,
		share.ID,
		share.Visitor.ID,
	) {
		return
	}
	var body commentBodyRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	item, err := h.reviews.UpdateComment(request.Context(), reviewdomain.UpdateCommentInput{
		WorkspaceID:     share.WorkspaceID,
		ReviewSessionID: share.ReviewSessionID,
		ReviewItemID:    publicItem.ID,
		ThreadID:        request.PathValue("threadId"),
		CommentID:       request.PathValue("commentId"),
		AuthorKind:      "share_visitor",
		AuthorVisitorID: share.Visitor.ID,
		Body:            body.Body,
	})
	if err != nil {
		h.handleReviewError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  share.WorkspaceID,
		ActorType:    "visitor",
		ActorID:      share.Visitor.ID,
		Action:       "review.comment_updated",
		ResourceType: "comment",
		ResourceID:   request.PathValue("commentId"),
	})
	writeJSON(response, http.StatusOK, toCommentThreadResponse(
		item,
		commentResponseOptions{
			ActorKind:    "share_visitor",
			ActorID:      share.Visitor.ID,
			PublicItemID: publicItem.ID,
			CanReply:     share.AllowComment,
		},
	))
}

func (h *handler) handleDeletePublicComment(
	response http.ResponseWriter,
	request *http.Request,
) {
	share, publicItem, ok := h.publicShareItem(response, request)
	if !ok {
		return
	}
	if !share.AllowComment {
		h.handlePublicShareError(response, request, sharedomain.ErrCommentDenied)
		return
	}
	if share.RequireNickname && !share.Visitor.Identified {
		h.handlePublicShareError(response, request, sharedomain.ErrIdentityRequired)
		return
	}
	if share.Visitor.ID == "" {
		h.handlePublicShareError(response, request, sharedomain.ErrSessionNotFound)
		return
	}
	// spend the tiered guest text-change budget before the request
	// body is read and before any write, audit record, or notification.
	if !h.consumeGuestTextChangeBudget(
		response,
		request,
		share.ID,
		share.Visitor.ID,
	) {
		return
	}
	err := h.reviews.DeleteComment(request.Context(), reviewdomain.DeleteCommentInput{
		WorkspaceID:     share.WorkspaceID,
		ReviewSessionID: share.ReviewSessionID,
		ReviewItemID:    publicItem.ID,
		ThreadID:        request.PathValue("threadId"),
		CommentID:       request.PathValue("commentId"),
		AuthorKind:      "share_visitor",
		AuthorVisitorID: share.Visitor.ID,
	})
	if err != nil {
		h.handleReviewError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  share.WorkspaceID,
		ActorType:    "visitor",
		ActorID:      share.Visitor.ID,
		Action:       "review.comment_deleted",
		ResourceType: "comment",
		ResourceID:   request.PathValue("commentId"),
	})
	response.WriteHeader(http.StatusNoContent)
}

func publicVisitorName(item sharedomain.PublicShare) string {
	if item.Visitor.DisplayName != nil &&
		strings.TrimSpace(*item.Visitor.DisplayName) != "" {
		return strings.TrimSpace(*item.Visitor.DisplayName)
	}
	return "匿名访客"
}

func toAnnotationInput(item annotationRequest) reviewdomain.AnnotationInput {
	return reviewdomain.AnnotationInput{
		Kind:            item.Kind,
		TimeStartUs:     item.TimeStartUs,
		TimeEndUs:       item.TimeEndUs,
		Geometry:        item.Geometry,
		GeometryVersion: item.GeometryVersion,
	}
}

func toCommentThreadListResponse(
	items []reviewdomain.CommentThread,
	options commentResponseOptions,
) commentThreadListResponse {
	result := make([]commentThreadResponse, 0, len(items))
	for _, item := range items {
		result = append(result, toCommentThreadResponse(item, options))
	}
	return commentThreadListResponse{Items: result}
}

func toCommentThreadResponse(
	item reviewdomain.CommentThread,
	options commentResponseOptions,
) commentThreadResponse {
	result := commentThreadResponse{
		ID:             item.ID,
		ReviewItemID:   item.ReviewItemID,
		AssetVersionID: item.AssetVersionID,
		Status:         item.Status,
		Author:         toCommentAuthorResponse(item.Author),
		ResolvedAt:     item.ResolvedAt,
		Annotation: annotationResponse{
			Kind:            item.Annotation.Kind,
			TimeStartUs:     item.Annotation.TimeStartUs,
			TimeEndUs:       item.Annotation.TimeEndUs,
			Geometry:        item.Annotation.Geometry,
			GeometryVersion: item.Annotation.GeometryVersion,
		},
		Comments:  make([]commentResponse, 0, len(item.Comments)),
		Revision:  item.Revision,
		CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt,
		CanReply:  options.CanReply && item.Status == "open",
		CanResolve: options.Manager &&
			item.Status == "open",
		CanReopen: options.Manager &&
			item.Status == "resolved",
	}
	if item.ResolvedBy != nil {
		author := toCommentAuthorResponse(*item.ResolvedBy)
		result.ResolvedBy = &author
	}
	for _, comment := range item.Comments {
		attachments := make([]commentAttachmentResponse, 0, len(comment.Attachments))
		for _, attachment := range comment.Attachments {
			attachments = append(
				attachments,
				toCommentAttachmentResponse(
					attachment,
					item.ReviewSessionID,
					item.ReviewItemID,
					options,
				),
			)
		}
		result.Comments = append(result.Comments, commentResponse{
			ID:          comment.ID,
			Author:      toCommentAuthorResponse(comment.Author),
			Body:        comment.Body,
			Attachments: attachments,
			EditedAt:    comment.EditedAt,
			CreatedAt:   comment.CreatedAt,
			CanEdit:     canMutateComment(comment, options),
			CanDelete:   canMutateComment(comment, options),
		})
	}
	return result
}

func toCommentAttachmentResponse(
	item reviewdomain.CommentAttachment,
	reviewSessionID string,
	reviewItemID string,
	options commentResponseOptions,
) commentAttachmentResponse {
	return commentAttachmentResponse{
		ID:               item.ID,
		OriginalFilename: item.OriginalFilename,
		MIMEType:         item.MIMEType,
		SizeBytes:        item.SizeBytes,
		Width:            item.Width,
		Height:           item.Height,
		URL:              commentAttachmentURL(item.ID, reviewSessionID, reviewItemID, options),
		CreatedAt:        item.CreatedAt,
	}
}

func commentAttachmentURL(
	attachmentID string,
	reviewSessionID string,
	reviewItemID string,
	options commentResponseOptions,
) string {
	if options.PublicItemID != "" {
		return fmt.Sprintf(
			"/share-api/v1/items/%s/attachments/%s/content",
			url.PathEscape(options.PublicItemID),
			url.PathEscape(attachmentID),
		)
	}
	return fmt.Sprintf(
		"/api/v1/review-sessions/%s/items/%s/attachments/%s/content",
		url.PathEscape(reviewSessionID),
		url.PathEscape(reviewItemID),
		url.PathEscape(attachmentID),
	)
}

func toCommentAuthorResponse(item reviewdomain.CommentAuthor) commentAuthorResponse {
	return commentAuthorResponse{
		Kind:        item.Kind,
		DisplayName: item.DisplayName,
	}
}

func canMutateComment(
	comment reviewdomain.Comment,
	options commentResponseOptions,
) bool {
	if comment.Author.Kind == "system" || options.ActorKind == "" ||
		options.ActorID == "" {
		return false
	}
	return comment.Author.Kind == options.ActorKind &&
		comment.Author.ID == options.ActorID
}
