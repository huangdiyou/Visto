package projectmember

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"review-studio.local/core/internal/catalog"
	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/platform/database"
	"review-studio.local/core/internal/projectaccess"
)

func TestProjectMemberLifecycleAndTransfer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, owner := projectMemberTestDatabase(t, ctx)
	service := NewService(NewSQLiteRepository(db))
	service.clock = func() time.Time {
		return time.Date(2026, time.June, 13, 12, 0, 0, 0, time.UTC)
	}
	catalogService := catalog.NewService(catalog.NewSQLiteRepository(db))
	project, err := catalogService.CreateProject(ctx, catalog.CreateProjectInput{
		WorkspaceID: owner.Workspace.ID,
		UserID:      owner.User.ID,
		Name:        "项目成员测试",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	identityService := identity.NewService(identity.NewSQLiteRepository(db))
	member, err := identityService.CreateAccount(ctx, identity.CreateAccountInput{
		WorkspaceID: owner.Workspace.ID,
		Email:       "member@example.com",
		DisplayName: "Member",
		Password:    "member-password-123",
		Locale:      "zh-CN",
		Role:        "member",
	})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	nextOwner, err := identityService.CreateAccount(ctx, identity.CreateAccountInput{
		WorkspaceID: owner.Workspace.ID,
		Email:       "next-owner@example.com",
		DisplayName: "Next Owner",
		Password:    "next-owner-password-123",
		Locale:      "zh-CN",
		Role:        "admin",
	})
	if err != nil {
		t.Fatalf("create next owner: %v", err)
	}

	members, err := service.List(ctx, owner.Workspace.ID, project.ID)
	if err != nil {
		t.Fatalf("list initial members: %v", err)
	}
	if len(members) != 1 || members[0].RoleKey != "primary_owner" {
		t.Fatalf("unexpected initial members: %#v", members)
	}

	added, err := service.Add(ctx, AddInput{
		WorkspaceID: owner.Workspace.ID,
		ProjectID:   project.ID,
		UserID:      member.ID,
		RoleKey:     "supervisor",
		Permissions: map[string]bool{"assets.upload": true},
		CreatedBy:   owner.User.ID,
	})
	if err != nil {
		t.Fatalf("add project member: %v", err)
	}
	if added.DisplayName != "Member" || added.RoleKey != "supervisor" ||
		!added.Permissions["assets.upload"] {
		t.Fatalf("unexpected added member: %#v", added)
	}
	guestExpiry := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
	guest, err := service.CreateGuest(ctx, CreateGuestInput{
		WorkspaceID: owner.Workspace.ID,
		ProjectID:   project.ID,
		DisplayName: "Guest Reviewer",
		Permissions: map[string]bool{"reviews.comment": true},
		ExpiresAt:   &guestExpiry,
		CreatedBy:   owner.User.ID,
	})
	if err != nil {
		t.Fatalf("create project guest: %v", err)
	}
	if guest.RoleKey != "guest" || guest.Email != nil ||
		guest.ExpiresAt == nil || !guest.Permissions["reviews.comment"] {
		t.Fatalf("unexpected guest member: %#v", guest)
	}
	var credentialCount int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM user_credentials WHERE user_id = ?
	`, guest.UserID).Scan(&credentialCount); err != nil {
		t.Fatalf("count guest credentials: %v", err)
	}
	if credentialCount != 0 {
		t.Fatalf("project guest should not have login credentials, got %d", credentialCount)
	}
	projectAccess := projectaccess.NewService(projectaccess.NewSQLiteRepository(db))
	allowed, err := projectAccess.Allowed(ctx, projectaccess.CheckInput{
		WorkspaceID:   owner.Workspace.ID,
		ProjectID:     project.ID,
		UserID:        guest.UserID,
		WorkspaceRole: "guest",
		Permission:    projectaccess.PermissionReviewsComment,
	})
	if err != nil {
		t.Fatalf("check guest project permission: %v", err)
	}
	if !allowed {
		t.Fatal("expected guest comment permission before expiry")
	}
	if _, err := service.Add(ctx, AddInput{
		WorkspaceID: owner.Workspace.ID,
		ProjectID:   project.ID,
		UserID:      member.ID,
		RoleKey:     "member",
		CreatedBy:   owner.User.ID,
	}); !errors.Is(err, ErrAlreadyMember) {
		t.Fatalf("expected duplicate member error, got %v", err)
	}

	updated, err := service.Update(ctx, UpdateInput{
		WorkspaceID: owner.Workspace.ID,
		ProjectID:   project.ID,
		ID:          added.ID,
		RoleKey:     "member",
		Status:      "active",
		Permissions: map[string]bool{"assets.upload": false},
		Revision:    added.Revision,
	})
	if err != nil {
		t.Fatalf("update project member: %v", err)
	}
	if updated.RoleKey != "member" || updated.Permissions["assets.upload"] {
		t.Fatalf("unexpected updated member: %#v", updated)
	}
	if err := service.Remove(ctx, RemoveInput{
		WorkspaceID: owner.Workspace.ID,
		ProjectID:   project.ID,
		ID:          members[0].ID,
		Revision:    members[0].Revision,
	}); !errors.Is(err, ErrPrimaryOwnerProtected) {
		t.Fatalf("expected primary owner protection, got %v", err)
	}
	if err := service.Remove(ctx, RemoveInput{
		WorkspaceID: owner.Workspace.ID,
		ProjectID:   project.ID,
		ID:          updated.ID,
		Revision:    updated.Revision,
	}); err != nil {
		t.Fatalf("remove project member: %v", err)
	}

	newPrimary, err := service.Transfer(ctx, TransferInput{
		WorkspaceID:        owner.Workspace.ID,
		ProjectID:          project.ID,
		NewPrimaryUserID:   nextOwner.ID,
		FormerOwnerAction:  "demote_member",
		ActorUserID:        owner.User.ID,
		ActorWorkspaceRole: "owner",
	})
	if err != nil {
		t.Fatalf("transfer project: %v", err)
	}
	if newPrimary.UserID != nextOwner.ID || newPrimary.RoleKey != "primary_owner" {
		t.Fatalf("unexpected new primary owner: %#v", newPrimary)
	}
	project, err = catalogService.Project(ctx, owner.Workspace.ID, project.ID)
	if err != nil {
		t.Fatalf("read transferred project: %v", err)
	}
	if project.PrimaryOwnerUserID == nil || *project.PrimaryOwnerUserID != nextOwner.ID {
		t.Fatalf("project primary owner not updated: %#v", project)
	}
}

func projectMemberTestDatabase(
	t *testing.T,
	ctx context.Context,
) (*sql.DB, identity.Session) {
	t.Helper()

	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	result, err := identity.NewService(identity.NewSQLiteRepository(db)).Setup(
		ctx,
		identity.SetupInput{
			WorkspaceName: "Studio",
			OwnerName:     "Owner",
			OwnerEmail:    "owner@example.com",
			Password:      "owner-password-123",
			Locale:        "zh-CN",
			Timezone:      "Asia/Shanghai",
		},
	)
	if err != nil {
		t.Fatalf("setup identity: %v", err)
	}
	return db, result.Session
}
