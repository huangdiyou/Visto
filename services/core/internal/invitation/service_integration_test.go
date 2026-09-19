package invitation

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/platform/database"
)

func TestInvitationCreateResendAcceptAndRevoke(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	identityService := identity.NewService(identity.NewSQLiteRepository(db))
	setup, err := identityService.Setup(ctx, identity.SetupInput{
		WorkspaceName: "Studio",
		OwnerName:     "Owner",
		Password:      "owner-password-123",
		Locale:        "zh-CN",
		Timezone:      "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("setup identity: %v", err)
	}
	now := time.Date(2026, 6, 11, 16, 0, 0, 0, time.UTC)
	service := NewService(NewSQLiteRepository(db), identityService)
	service.clock = func() time.Time { return now }
	created, err := service.Create(ctx, CreateInput{
		WorkspaceID: setup.Session.Workspace.ID,
		InvitedBy:   setup.Session.User.ID,
		Email:       "member@example.com",
		Role:        "member",
	})
	if err != nil {
		t.Fatalf("create invitation: %v", err)
	}
	firstToken := strings.TrimPrefix(created.URL, "/join/#")
	if firstToken == created.URL || firstToken == "" {
		t.Fatalf("invitation URL did not contain fragment token: %q", created.URL)
	}
	if _, err := service.Preview(ctx, firstToken); err != nil {
		t.Fatalf("preview invitation: %v", err)
	}
	resent, err := service.Resend(
		ctx,
		setup.Session.Workspace.ID,
		created.Invitation.ID,
	)
	if err != nil {
		t.Fatalf("resend invitation: %v", err)
	}
	secondToken := strings.TrimPrefix(resent.URL, "/join/#")
	if secondToken == firstToken || resent.Invitation.SendCount != 2 {
		t.Fatalf("invitation token was not rotated: %#v", resent)
	}
	if _, err := service.Preview(ctx, firstToken); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("old invitation token remained valid: %v", err)
	}
	accepted, err := service.Accept(ctx, AcceptInput{
		Token:       secondToken,
		DisplayName: "Member",
		Password:    "member-password-123",
		Locale:      "zh-CN",
	})
	if err != nil {
		t.Fatalf("accept invitation: %v", err)
	}
	if accepted.Session.Role != "member" ||
		accepted.Session.User.Email == nil ||
		*accepted.Session.User.Email != "member@example.com" {
		t.Fatalf("unexpected accepted session: %#v", accepted.Session)
	}
	if _, err := service.Accept(ctx, AcceptInput{
		Token:       secondToken,
		DisplayName: "Again",
		Password:    "another-password-123",
		Locale:      "zh-CN",
	}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("accepted invitation was reusable: %v", err)
	}
	if _, err := identityService.LoginAccount(ctx, identity.LoginInput{
		Email:    "member@example.com",
		Password: "member-password-123",
	}); err != nil {
		t.Fatalf("invited member login: %v", err)
	}
	revokable, err := service.Create(ctx, CreateInput{
		WorkspaceID: setup.Session.Workspace.ID,
		InvitedBy:   setup.Session.User.ID,
		Email:       "guest@example.com",
		Role:        "guest",
	})
	if err != nil {
		t.Fatalf("create revokable invitation: %v", err)
	}
	revoked, err := service.Revoke(
		ctx,
		setup.Session.Workspace.ID,
		revokable.Invitation.ID,
	)
	if err != nil {
		t.Fatalf("revoke invitation: %v", err)
	}
	if revoked.Status != "revoked" {
		t.Fatalf("unexpected revoked invitation: %#v", revoked)
	}
	if _, err := service.Preview(
		ctx,
		strings.TrimPrefix(revokable.URL, "/join/#"),
	); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("revoked invitation remained available: %v", err)
	}
}
