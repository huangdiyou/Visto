package identity

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"review-studio.local/core/internal/platform/database"
)

func TestSessionExpiresAfterIdleTimeout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{Path: filepath.Join(t.TempDir(), "review-studio.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	now := time.Date(2026, time.June, 9, 8, 0, 0, 0, time.UTC)
	service := NewService(NewSQLiteRepository(db))
	service.clock = func() time.Time { return now }
	result, err := service.Setup(ctx, SetupInput{
		WorkspaceName: "Studio",
		OwnerName:     "Owner",
		Password:      "local-password-123",
		Locale:        "zh-CN",
		Timezone:      "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	service.clock = func() time.Time { return now.Add(sessionIdleDuration + time.Second) }
	if _, err := service.Authenticate(ctx, result.Token); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected idle session expiry, got %v", err)
	}
}

func TestSetupAndSessionSurviveDatabaseRestart(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "review-studio.db")
	fixedNow := time.Date(2026, time.June, 9, 8, 0, 0, 0, time.UTC)

	db, err := database.Open(ctx, database.Config{Path: path})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	service := NewService(NewSQLiteRepository(db))
	service.clock = func() time.Time { return fixedNow }

	result, err := service.Setup(ctx, SetupInput{
		WorkspaceName: "我的工作空间",
		OwnerName:     "Huang",
		Password:      "local-password-123",
		Locale:        "zh-CN",
		Timezone:      "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	if result.Session.Role != "owner" {
		t.Fatalf("expected owner role, got %q", result.Session.Role)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	reopened, err := database.Open(ctx, database.Config{Path: path})
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer reopened.Close()

	restartedService := NewService(NewSQLiteRepository(reopened))
	restartedService.clock = func() time.Time { return fixedNow.Add(time.Hour) }

	session, err := restartedService.Authenticate(ctx, result.Token)
	if err != nil {
		t.Fatalf("authenticate persisted session: %v", err)
	}

	if session.Workspace.Name != "我的工作空间" {
		t.Fatalf("unexpected workspace %q", session.Workspace.Name)
	}
	if session.User.DisplayName != "Huang" {
		t.Fatalf("unexpected owner %q", session.User.DisplayName)
	}
	updated, err := restartedService.UpdateProfile(ctx, UpdateProfileInput{
		UserID:      session.User.ID,
		DisplayName: "Huang Updated",
		Locale:      "en-US",
	})
	if err != nil {
		t.Fatalf("update profile: %v", err)
	}
	if updated.DisplayName != "Huang Updated" {
		t.Fatalf("unexpected updated profile: %#v", updated)
	}
	if updated.Locale != "en-US" {
		t.Fatalf("unexpected updated locale: %#v", updated)
	}
	refreshed, err := restartedService.Authenticate(ctx, result.Token)
	if err != nil {
		t.Fatalf("authenticate updated profile: %v", err)
	}
	if refreshed.User.DisplayName != "Huang Updated" {
		t.Fatalf("session did not refresh profile: %#v", refreshed.User)
	}
	if refreshed.User.Locale != "en-US" {
		t.Fatalf("session did not refresh locale: %#v", refreshed.User)
	}

	login, err := restartedService.Login(ctx, "local-password-123")
	if err != nil {
		t.Fatalf("login after restart: %v", err)
	}
	if login.Token == result.Token {
		t.Fatal("expected a newly generated login token")
	}
}

func TestSetupCanOnlyRunOnce(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	service := NewService(NewSQLiteRepository(db))
	input := SetupInput{
		WorkspaceName: "Studio",
		OwnerName:     "Owner",
		Password:      "local-password-123",
		Locale:        "zh-CN",
		Timezone:      "Asia/Shanghai",
	}

	if _, err := service.Setup(ctx, input); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	if _, err := service.Setup(ctx, input); err != ErrSetupAlreadyCompleted {
		t.Fatalf("expected setup conflict, got %v", err)
	}
}

func TestMultipleAccountsLoginWithIndependentEmail(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	service := NewService(NewSQLiteRepository(db))
	setup, err := service.Setup(ctx, SetupInput{
		WorkspaceName: "Studio",
		OwnerName:     "Owner",
		OwnerEmail:    "owner@example.com",
		Password:      "owner-password-123",
		Locale:        "zh-CN",
		Timezone:      "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	member, err := service.CreateAccount(ctx, CreateAccountInput{
		WorkspaceID: setup.Session.Workspace.ID,
		Email:       "Member@Example.com",
		DisplayName: "Member",
		Password:    "member-password-123",
		Locale:      "zh-CN",
		Role:        "member",
	})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	if member.Email == nil || *member.Email != "member@example.com" {
		t.Fatalf("unexpected normalized member email: %#v", member.Email)
	}
	login, err := service.LoginAccount(ctx, LoginInput{
		Email:    "MEMBER@example.com",
		Password: "member-password-123",
	})
	if err != nil {
		t.Fatalf("member login: %v", err)
	}
	if login.Session.User.ID != member.ID || login.Session.Role != "member" {
		t.Fatalf("unexpected member session: %#v", login.Session)
	}
	if _, err := service.LoginAccount(ctx, LoginInput{
		Email:    "owner@example.com",
		Password: "member-password-123",
	}); err != ErrInvalidCredentials {
		t.Fatalf("expected independent credentials, got %v", err)
	}
	if _, err := service.Login(ctx, "owner-password-123"); err != ErrInvalidCredentials {
		t.Fatalf("email owner should not use legacy login, got %v", err)
	}
	if _, err := service.CreateAccount(ctx, CreateAccountInput{
		WorkspaceID: setup.Session.Workspace.ID,
		Email:       "member@example.com",
		DisplayName: "Duplicate",
		Password:    "another-password-123",
		Locale:      "zh-CN",
		Role:        "guest",
	}); err != ErrEmailInUse {
		t.Fatalf("expected duplicate email rejection, got %v", err)
	}
}
