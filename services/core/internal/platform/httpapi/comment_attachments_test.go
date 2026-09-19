package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"review-studio.local/core/internal/platform/database"
	"review-studio.local/core/internal/ratelimit"
	"review-studio.local/core/internal/storage"
)

func TestCommentAttachmentImageValidationRequiresDecodableContent(t *testing.T) {
	truncated := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR")
	if mimeType := detectCommentAttachmentMIME(truncated); mimeType != "image/png" {
		t.Fatalf("truncated PNG MIME = %q, want image/png before decode validation", mimeType)
	}
	if _, _, _, err := decodeImageDimensions(truncated); err == nil {
		t.Fatal("truncated image bytes must fail image decoding")
	}

	valid := testPNGBytes()
	if mimeType := detectCommentAttachmentMIME(valid); mimeType != "image/png" {
		t.Fatalf("detected MIME = %q, want image/png", mimeType)
	}
	width, height, mimeType, err := decodeImageDimensions(valid)
	if err != nil {
		t.Fatalf("decode valid PNG: %v", err)
	}
	if width == nil || height == nil || *width != 1 || *height != 1 || mimeType != "image/png" {
		t.Fatalf("unexpected decoded image: width=%v height=%v MIME=%q", width, height, mimeType)
	}
}

func TestReviewAttachmentUploadTargetUsesProjectSelection(t *testing.T) {
	config := testConfig(t)
	httpHandler := NewHandler(config)
	setup := performJSONRequest(
		httpHandler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName":"Studio",
			"ownerName":"Owner",
			"ownerEmail":"owner-attachments@example.com",
			"password":"local-password-123",
			"locale":"zh-CN",
			"timezone":"Asia/Shanghai"
		}`,
		nil,
	)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup failed: %d %s", setup.Code, setup.Body.String())
	}
	ownerCookie := findSessionCookie(t, setup.Result().Cookies())
	var ownerSession sessionResponse
	if err := json.NewDecoder(setup.Body).Decode(&ownerSession); err != nil {
		t.Fatalf("decode owner session: %v", err)
	}

	createProject := performJSONRequest(
		httpHandler,
		http.MethodPost,
		"/api/v1/projects",
		projectCreateBodyWithStorage(t, httpHandler, ownerCookie, "Attachment Project", nil),
		ownerCookie,
	)
	if createProject.Code != http.StatusCreated {
		t.Fatalf("create project failed: %d %s", createProject.Code, createProject.Body.String())
	}
	var project projectResponse
	if err := json.NewDecoder(createProject.Body).Decode(&project); err != nil {
		t.Fatalf("decode project: %v", err)
	}

	selectionsResponse := performJSONRequest(
		httpHandler,
		http.MethodGet,
		"/api/v1/projects/"+project.ID+"/storage-selections",
		"",
		ownerCookie,
	)
	if selectionsResponse.Code != http.StatusOK {
		t.Fatalf("list selections failed: %d %s", selectionsResponse.Code, selectionsResponse.Body.String())
	}
	var selections projectStorageSelectionListResponse
	if err := json.NewDecoder(selectionsResponse.Body).Decode(&selections); err != nil {
		t.Fatalf("decode selections: %v", err)
	}
	var selectedRootID string
	for _, selection := range selections.Items {
		if selection.Purpose == "upload" && selection.Grant.AuthorizedRootID != nil {
			selectedRootID = *selection.Grant.AuthorizedRootID
			break
		}
	}
	if selectedRootID == "" {
		t.Fatal("project upload selection is missing")
	}

	reviewBucket, err := config.Storage.CreateLocalManagedBucket(
		context.Background(),
		storage.CreateLocalManagedBucketInput{
			WorkspaceID:          ownerSession.Workspace.ID,
			DisplayName:          "Unrelated review uploads",
			LocalPath:            filepath.Join(t.TempDir(), "unrelated-review-uploads"),
			Purpose:              "review_upload",
			UploadSecurityPolicy: "standard",
			ProjectAvailable:     false,
			CreatedBy:            ownerSession.User.ID,
		},
	)
	if err != nil {
		t.Fatalf("create unrelated review bucket: %v", err)
	}

	resolver := &handler{storage: config.Storage, projectStorage: config.ProjectStorage}
	rootID, _, err := resolver.reviewAttachmentUploadTarget(
		context.Background(),
		ownerSession.Workspace.ID,
		project.ID,
	)
	if err != nil {
		t.Fatalf("resolve review attachment target: %v", err)
	}
	if rootID != selectedRootID {
		t.Fatalf("attachment root = %q, want project-selected root %q", rootID, selectedRootID)
	}
	if rootID == reviewBucket.AuthorizedRootID {
		t.Fatal("attachment target must not use an unrelated workspace review bucket")
	}
}

func TestCommentAttachmentAdmissionIsBounded(t *testing.T) {
	concurrency := &handler{
		attachmentUploadSlots: make(chan struct{}, maxConcurrentCommentAttachmentUploads),
	}
	releases := make([]func(), 0, maxConcurrentCommentAttachmentUploads)
	for index := 0; index < maxConcurrentCommentAttachmentUploads; index++ {
		release, ok := concurrency.acquireAttachmentUploadSlot()
		if !ok {
			t.Fatalf("upload slot %d rejected while capacity remained", index)
		}
		releases = append(releases, release)
	}
	if _, ok := concurrency.acquireAttachmentUploadSlot(); ok {
		t.Fatal("attachment uploads admitted beyond the concurrency bound")
	}
	for _, release := range releases {
		release()
	}
	release, ok := concurrency.acquireAttachmentUploadSlot()
	if !ok {
		t.Fatal("upload slot was not reusable after release")
	}
	release()

	db, err := database.Open(context.Background(), database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	limited := &handler{
		rateLimits: ratelimit.New(db),
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	attempt := func() (*httptest.ResponseRecorder, bool) {
		response := httptest.NewRecorder()
		request := newRequest(
			http.MethodPost,
			"/api/v1/share-api/v1/entries/token/comment-attachments",
			nil,
		)
		allowed := limited.consumeRateLimit(
			response,
			request,
			"comment-attachment",
			"share:share-1|client:203.0.113.40",
			maxCommentAttachmentUploadsPerWindow,
			commentAttachmentUploadWindow,
		)
		return response, allowed
	}
	for index := 0; index < maxCommentAttachmentUploadsPerWindow; index++ {
		if _, allowed := attempt(); !allowed {
			t.Fatalf("attempt %d rejected before the budget was spent", index)
		}
	}
	response, allowed := attempt()
	if allowed {
		t.Fatal("attachment uploads must be rate limited after the budget is spent")
	}
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limited status = %d, want 429", response.Code)
	}
}

// TestAuthenticatedAttachmentAdmissionUsesPerUserBudget locks down SAR-F62:
// the authenticated upload entry consumes the same per-window budget keyed by
// user, and separate users do not share it.
func TestAuthenticatedAttachmentAdmissionUsesPerUserBudget(t *testing.T) {
	db, err := database.Open(context.Background(), database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	limited := &handler{
		rateLimits: ratelimit.New(db),
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	attempt := func(userID string) (*httptest.ResponseRecorder, bool) {
		response := httptest.NewRecorder()
		request := newRequest(
			http.MethodPost,
			"/api/v1/review-sessions/review-1/comment-attachments",
			nil,
		)
		allowed := limited.consumeRateLimit(
			response,
			request,
			"comment-attachment",
			"user:"+userID,
			maxCommentAttachmentUploadsPerWindow,
			commentAttachmentUploadWindow,
		)
		return response, allowed
	}
	for index := 0; index < maxCommentAttachmentUploadsPerWindow; index++ {
		if _, allowed := attempt("user-1"); !allowed {
			t.Fatalf("attempt %d rejected before the user budget was spent", index)
		}
	}
	response, allowed := attempt("user-1")
	if allowed {
		t.Fatal("authenticated uploads must be rate limited after the user budget is spent")
	}
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limited status = %d, want 429", response.Code)
	}
	if _, allowed := attempt("user-2"); !allowed {
		t.Fatal("another user must have an independent attachment budget")
	}
}
