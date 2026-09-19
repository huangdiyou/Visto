package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	_ "golang.org/x/image/webp"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/media"
	"review-studio.local/core/internal/projectaccess"
	reviewdomain "review-studio.local/core/internal/review"
	sharedomain "review-studio.local/core/internal/share"
	"review-studio.local/core/internal/storage"
)

const (
	maxCommentAttachmentBytes  = 10 * 1024 * 1024
	maxCommentAttachmentCount  = 4
	maxCommentAttachmentPixels = 40 * 1000 * 1000
	// Allow multipart framing and the small review item field, while bounding
	// parser buffering and temporary-file use before FormFile is called.
	maxCommentAttachmentRequestBytes = maxCommentAttachmentBytes + 2*1024*1024
	// Attachment uploads buffer and decode the whole image in memory before
	// storage, so admission is bounded per share+client and globally concurrent.
	maxCommentAttachmentUploadsPerWindow  = 20
	commentAttachmentUploadWindow         = 5 * time.Minute
	maxConcurrentCommentAttachmentUploads = 4
)

var allowedCommentAttachmentMIMEs = map[string]struct{}{
	"image/gif":  {},
	"image/jpeg": {},
	"image/png":  {},
	"image/webp": {},
}

func (h *handler) handleUploadReviewCommentAttachment(
	response http.ResponseWriter,
	request *http.Request,
) {
	request.Body = http.MaxBytesReader(response, request.Body, maxCommentAttachmentRequestBytes)
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	reviewSession, ok := h.requireReviewSessionProjectPermission(
		response,
		request,
		session,
		request.PathValue("reviewId"),
		projectaccess.PermissionReviewsComment,
	)
	if !ok {
		return
	}
	// SAR-F62: the authenticated entry buffers and decodes the whole image in
	// memory before any storage write, so it needs the same per-subject rate
	// limit and global concurrency admission as the public share entry.
	if !h.consumeRateLimit(
		response,
		request,
		"comment-attachment",
		"user:"+session.User.ID,
		maxCommentAttachmentUploadsPerWindow,
		commentAttachmentUploadWindow,
	) {
		return
	}
	releaseAttachmentSlot, acquired := h.acquireAttachmentUploadSlot()
	if !acquired {
		response.Header().Set("Retry-After", "5")
		writeError(
			response,
			http.StatusServiceUnavailable,
			requestID(response),
			"attachment.busy",
			"附件上传繁忙，请稍后再试",
		)
		return
	}
	defer releaseAttachmentSlot()
	result, uploadErr := h.uploadCommentAttachment(request, commentAttachmentUploadContext{
		WorkspaceID:     session.Workspace.ID,
		ReviewSessionID: reviewSession.ID,
		ProjectID:       reviewSession.ProjectID,
		ReviewItemID:    strings.TrimSpace(request.FormValue("reviewItemId")),
		SourceType:      "user",
		UserID:          session.User.ID,
	})
	if uploadErr != nil {
		h.handleCommentAttachmentUploadError(response, request, uploadErr)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "review.comment_attachment_uploaded",
		ResourceType: "comment_attachment",
		ResourceID:   result.Attachment.ID,
		After: map[string]any{
			"reviewSessionId": result.Attachment.ReviewSessionID,
			"reviewItemId":    result.Attachment.ReviewItemID,
			"mimeType":        result.Attachment.MIMEType,
			"sizeBytes":       result.Attachment.SizeBytes,
			"status":          result.Attachment.Status,
		},
	})
	writeJSON(response, http.StatusCreated, toCommentAttachmentResponse(
		result.Attachment,
		reviewSession.ID,
		result.Attachment.ReviewItemID,
		commentResponseOptions{},
	))
}

func (h *handler) handleUploadPublicCommentAttachment(
	response http.ResponseWriter,
	request *http.Request,
) {
	request.Body = http.MaxBytesReader(response, request.Body, maxCommentAttachmentRequestBytes)
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
	// Attachment uploads buffer and decode the whole image in memory before any
	// storage write, so admission is bounded per share+client and globally
	// concurrent before a single request byte is read.
	clientIdentity := h.requestRateLimitClientIdentity(request)
	if clientIdentity == "" {
		clientIdentity = "unknown"
	}
	if !h.consumeRateLimit(
		response,
		request,
		"comment-attachment",
		"share:"+share.ID+"|client:"+clientIdentity,
		maxCommentAttachmentUploadsPerWindow,
		commentAttachmentUploadWindow,
	) {
		return
	}
	releaseAttachmentSlot, acquired := h.acquireAttachmentUploadSlot()
	if !acquired {
		response.Header().Set("Retry-After", "5")
		writeError(
			response,
			http.StatusServiceUnavailable,
			requestID(response),
			"attachment.busy",
			"附件上传繁忙，请稍后再试",
		)
		return
	}
	defer releaseAttachmentSlot()
	reviewSession, err := h.reviews.Get(
		request.Context(),
		share.WorkspaceID,
		share.ReviewSessionID,
	)
	if err != nil {
		h.handleReviewError(response, request, err)
		return
	}
	result, uploadErr := h.uploadCommentAttachment(request, commentAttachmentUploadContext{
		WorkspaceID:     share.WorkspaceID,
		ReviewSessionID: share.ReviewSessionID,
		ProjectID:       reviewSession.ProjectID,
		ReviewItemID:    publicItem.ID,
		SourceType:      "share_visitor",
		VisitorID:       share.Visitor.ID,
	})
	if uploadErr != nil {
		h.handleCommentAttachmentUploadError(response, request, uploadErr)
		return
	}
	h.recordAccess(request, share, "commented", "comment_attachment", result.Attachment.ID)
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  share.WorkspaceID,
		ActorType:    "visitor",
		ActorID:      share.Visitor.ID,
		Action:       "review.comment_attachment_uploaded",
		ResourceType: "comment_attachment",
		ResourceID:   result.Attachment.ID,
		After: map[string]any{
			"reviewSessionId": result.Attachment.ReviewSessionID,
			"reviewItemId":    result.Attachment.ReviewItemID,
			"mimeType":        result.Attachment.MIMEType,
			"sizeBytes":       result.Attachment.SizeBytes,
			"status":          result.Attachment.Status,
		},
	})
	writeJSON(response, http.StatusCreated, toCommentAttachmentResponse(
		result.Attachment,
		share.ReviewSessionID,
		publicItem.ID,
		commentResponseOptions{PublicItemID: publicItem.ID},
	))
}

func (h *handler) handleReviewCommentAttachmentContent(
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
	item, err := h.reviews.CommentAttachment(request.Context(), reviewdomain.CommentAttachmentLookup{
		WorkspaceID:     session.Workspace.ID,
		ReviewSessionID: request.PathValue("reviewId"),
		ReviewItemID:    request.PathValue("itemId"),
		ID:              request.PathValue("attachmentId"),
		ActorKind:       "user",
		ActorUserID:     session.User.ID,
	})
	if err != nil {
		h.handleReviewError(response, request, err)
		return
	}
	h.streamCommentAttachment(response, request, item)
}

func (h *handler) handlePublicCommentAttachmentContent(
	response http.ResponseWriter,
	request *http.Request,
) {
	share, publicItem, ok := h.publicShareItem(response, request)
	if !ok {
		return
	}
	item, err := h.reviews.CommentAttachment(request.Context(), reviewdomain.CommentAttachmentLookup{
		WorkspaceID:     share.WorkspaceID,
		ReviewSessionID: share.ReviewSessionID,
		ReviewItemID:    publicItem.ID,
		ID:              request.PathValue("attachmentId"),
		ActorKind:       "share_visitor",
		ActorVisitorID:  share.Visitor.ID,
	})
	if err != nil {
		h.handleReviewError(response, request, err)
		return
	}
	h.streamCommentAttachment(response, request, item)
}

// acquireAttachmentUploadSlot reserves one concurrent comment-attachment upload
// slot. Comment attachments are read, decoded and buffered in memory before any
// storage write, so a global bound keeps parallel uploads from exhausting memory.
func (h *handler) acquireAttachmentUploadSlot() (func(), bool) {
	if h.attachmentUploadSlots == nil {
		return func() {}, true
	}
	select {
	case h.attachmentUploadSlots <- struct{}{}:
		released := false
		return func() {
			if released {
				return
			}
			released = true
			<-h.attachmentUploadSlots
		}, true
	default:
		return nil, false
	}
}

type commentAttachmentUploadContext struct {
	WorkspaceID     string
	ReviewSessionID string
	ProjectID       string
	ReviewItemID    string
	SourceType      string
	UserID          string
	VisitorID       string
}

type commentAttachmentUploadResult struct {
	Attachment reviewdomain.CommentAttachment
}

func (h *handler) uploadCommentAttachment(
	request *http.Request,
	context commentAttachmentUploadContext,
) (commentAttachmentUploadResult, error) {
	if strings.TrimSpace(context.ReviewItemID) == "" {
		return commentAttachmentUploadResult{}, reviewdomain.ErrInvalidItem
	}
	reader, fileHeader, err := request.FormFile("file")
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return commentAttachmentUploadResult{}, fmt.Errorf("%w: request exceeds 12 MB", reviewdomain.ErrInvalidAttachment)
		}
		return commentAttachmentUploadResult{}, fmt.Errorf("%w: file is required", reviewdomain.ErrInvalidAttachment)
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, maxCommentAttachmentBytes+1))
	if err != nil {
		return commentAttachmentUploadResult{}, err
	}
	if int64(len(body)) > maxCommentAttachmentBytes {
		return commentAttachmentUploadResult{}, fmt.Errorf("%w: file exceeds 10 MB", reviewdomain.ErrInvalidAttachment)
	}
	if len(body) == 0 {
		return commentAttachmentUploadResult{}, fmt.Errorf("%w: file is empty", reviewdomain.ErrInvalidAttachment)
	}
	mimeType := detectCommentAttachmentMIME(body)
	if _, ok := allowedCommentAttachmentMIMEs[mimeType]; !ok {
		return commentAttachmentUploadResult{}, fmt.Errorf("%w: unsupported image type", reviewdomain.ErrInvalidAttachment)
	}
	width, height, decodedMIME, decodeErr := decodeImageDimensions(body)
	if decodeErr != nil || decodedMIME != mimeType {
		return commentAttachmentUploadResult{}, fmt.Errorf("%w: invalid image content", reviewdomain.ErrInvalidAttachment)
	}
	rootID, policy, targetErr := h.reviewAttachmentUploadTarget(
		request.Context(),
		context.WorkspaceID,
		context.ProjectID,
	)
	if targetErr != nil {
		return commentAttachmentUploadResult{}, targetErr
	}
	adapter, err := h.storage.Adapter(
		request.Context(),
		context.WorkspaceID,
		rootID,
	)
	if err != nil {
		return commentAttachmentUploadResult{}, err
	}
	objectID, err := uuid.NewV7()
	if err != nil {
		return commentAttachmentUploadResult{}, err
	}
	objectKey, err := storage.NormalizeObjectKey(path.Join(
		"review-attachments",
		context.ReviewSessionID,
		objectID.String(),
		safeCommentAttachmentFilename(fileHeader.Filename, mimeType),
	))
	if err != nil {
		return commentAttachmentUploadResult{}, err
	}
	if _, err := adapter.Put(
		request.Context(),
		objectKey,
		bytes.NewReader(body),
		int64(len(body)),
		mimeType,
	); err != nil {
		return commentAttachmentUploadResult{}, err
	}
	attachment, err := h.reviews.CreatePendingAttachment(
		request.Context(),
		reviewdomain.CreatePendingAttachmentInput{
			WorkspaceID:          context.WorkspaceID,
			ReviewSessionID:      context.ReviewSessionID,
			ReviewItemID:         context.ReviewItemID,
			SourceType:           context.SourceType,
			UploadedByUserID:     context.UserID,
			ShareVisitorID:       context.VisitorID,
			AuthorizedRootID:     rootID,
			ObjectKey:            objectKey,
			OriginalFilename:     fileHeader.Filename,
			MIMEType:             mimeType,
			SizeBytes:            int64(len(body)),
			Width:                width,
			Height:               height,
			UploadSecurityPolicy: policy,
			Status:               "ready",
			ResultCode:           "basic_image_gate",
		},
	)
	if err != nil {
		_ = adapter.Delete(request.Context(), objectKey)
		return commentAttachmentUploadResult{}, err
	}
	return commentAttachmentUploadResult{Attachment: attachment}, nil
}

func (h *handler) reviewAttachmentUploadTarget(
	ctx context.Context,
	workspaceID string,
	projectID string,
) (string, string, error) {
	target, err := h.projectUploadTarget(ctx, workspaceID, &projectID)
	if err != nil {
		return "", "", err
	}
	if target == nil {
		return "", "", media.ErrLibraryUploadTargetNeeded
	}
	return target.AuthorizedRootID,
		normalizeUploadSecurityPolicy(target.UploadSecurityPolicy),
		nil
}

func (h *handler) streamCommentAttachment(
	response http.ResponseWriter,
	request *http.Request,
	item reviewdomain.CommentAttachment,
) {
	adapter, err := h.storage.Adapter(
		request.Context(),
		item.WorkspaceID,
		item.AuthorizedRootID,
	)
	if err != nil {
		h.handleStorageError(response, request, err)
		return
	}
	byteRange, partial, err := parseRangeHeader(
		request.Header.Get("Range"),
		item.SizeBytes,
	)
	if err != nil {
		response.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", item.SizeBytes))
		h.handleStorageError(response, request, err)
		return
	}
	reader, info, err := adapter.OpenRange(
		request.Context(),
		item.ObjectKey,
		byteRange,
	)
	if err != nil {
		h.handleStorageError(response, request, err)
		return
	}
	defer reader.Close()
	length := byteRange.Length
	if length == 0 {
		length = info.SizeBytes - byteRange.Offset
	}
	response.Header().Set("Accept-Ranges", "bytes")
	response.Header().Set("Content-Type", defaultString(item.MIMEType, info.MIMEType))
	response.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	response.Header().Set("Content-Disposition", mime.FormatMediaType(
		"inline",
		map[string]string{"filename": filepath.Base(item.OriginalFilename)},
	))
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	if partial {
		response.Header().Set(
			"Content-Range",
			fmt.Sprintf(
				"bytes %d-%d/%d",
				byteRange.Offset,
				byteRange.Offset+length-1,
				info.SizeBytes,
			),
		)
		response.WriteHeader(http.StatusPartialContent)
	} else {
		response.WriteHeader(http.StatusOK)
	}
	if _, err := io.Copy(response, reader); err != nil &&
		!errors.Is(err, request.Context().Err()) {
		h.logger.Warn(
			"comment attachment stream interrupted",
			"request_id", requestID(response),
			"error", err,
		)
	}
}

func (h *handler) handleCommentAttachmentUploadError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, media.ErrLibraryUploadTargetNeeded),
		errors.Is(err, media.ErrLibraryUploadTargetInvalid):
		h.handleLibraryError(response, request, err)
	case errors.Is(err, storage.ErrPathInvalid),
		errors.Is(err, storage.ErrPathEscapesRoot),
		errors.Is(err, storage.ErrRootNotFound),
		errors.Is(err, storage.ErrProviderUnsupported):
		h.handleStorageError(response, request, err)
	default:
		h.handleReviewError(response, request, err)
	}
}

func detectCommentAttachmentMIME(body []byte) string {
	if len(body) >= 12 &&
		string(body[:4]) == "RIFF" &&
		string(body[8:12]) == "WEBP" {
		return "image/webp"
	}
	detected := http.DetectContentType(body)
	if detected == "image/x-png" {
		return "image/png"
	}
	if _, ok := allowedCommentAttachmentMIMEs[detected]; ok {
		return detected
	}
	return detected
}

func decodeImageDimensions(body []byte) (*int, *int, string, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return nil, nil, "", fmt.Errorf("decode image: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return nil, nil, "", errors.New("image dimensions are invalid")
	}
	if int64(config.Width)*int64(config.Height) > maxCommentAttachmentPixels {
		return nil, nil, "", errors.New("image dimensions exceed the pixel limit")
	}
	_, decodedFormat, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, nil, "", fmt.Errorf("decode image pixels: %w", err)
	}
	if decodedFormat != format {
		return nil, nil, "", errors.New("image format changed while decoding")
	}
	mimeType := "image/" + format
	if format == "jpeg" {
		mimeType = "image/jpeg"
	}
	width := config.Width
	height := config.Height
	return &width, &height, mimeType, nil
}

func safeCommentAttachmentFilename(filename string, mimeType string) string {
	filename = strings.TrimSpace(filepath.Base(strings.ReplaceAll(filename, "\\", "/")))
	if filename == "." || filename == string(filepath.Separator) {
		filename = ""
	}
	extension := strings.ToLower(filepath.Ext(filename))
	if extension == "" {
		extension = commentAttachmentExtension(mimeType)
	}
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	var builder strings.Builder
	for _, char := range base {
		switch {
		case char == '-' || char == '_' || char == '.':
			builder.WriteRune(char)
		case unicode.IsLetter(char) || unicode.IsDigit(char):
			builder.WriteRune(char)
		case unicode.IsSpace(char):
			builder.WriteRune('-')
		}
		if builder.Len() >= 80 {
			break
		}
	}
	safeBase := strings.Trim(builder.String(), ".-_")
	if safeBase == "" {
		safeBase = "image"
	}
	return safeBase + extension
}

func commentAttachmentExtension(mimeType string) string {
	switch mimeType {
	case "image/gif":
		return ".gif"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	default:
		return ".img"
	}
}
