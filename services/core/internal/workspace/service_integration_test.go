package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/platform/database"
)

func TestMembershipUpdateProtectsLastOwnerAndRevokesSessions(t *testing.T) {
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
	if _, err := identityService.CreateAccount(ctx, identity.CreateAccountInput{
		WorkspaceID: setup.Session.Workspace.ID,
		Email:       "member@example.com",
		DisplayName: "Member",
		Password:    "member-password-123",
		Locale:      "zh-CN",
		Role:        "member",
	}); err != nil {
		t.Fatalf("create member: %v", err)
	}
	memberLogin, err := identityService.LoginAccount(ctx, identity.LoginInput{
		Email:    "member@example.com",
		Password: "member-password-123",
	})
	if err != nil {
		t.Fatalf("login member: %v", err)
	}
	service := NewService(NewSQLiteRepository(db))
	members, err := service.ListMemberships(ctx, setup.Session.Workspace.ID)
	if err != nil {
		t.Fatalf("list memberships: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("membership count = %d, want 2", len(members))
	}
	var owner, member Membership
	for _, item := range members {
		switch item.Role {
		case "owner":
			owner = item
		case "member":
			member = item
		}
	}
	if _, err := service.UpdateMembership(ctx, UpdateMembershipInput{
		WorkspaceID: "another-workspace",
		ID:          member.ID,
		Role:        "member",
		Status:      "disabled",
		Revision:    member.Revision,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace membership update returned %v", err)
	}
	if _, err := service.UpdateMembership(ctx, UpdateMembershipInput{
		WorkspaceID: setup.Session.Workspace.ID,
		ID:          owner.ID,
		Role:        "admin",
		Status:      "active",
		Revision:    owner.Revision,
	}); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("expected last owner protection, got %v", err)
	}
	disabled, err := service.UpdateMembership(ctx, UpdateMembershipInput{
		WorkspaceID: setup.Session.Workspace.ID,
		ID:          member.ID,
		Role:        "member",
		Status:      "disabled",
		Revision:    member.Revision,
	})
	if err != nil {
		t.Fatalf("disable member: %v", err)
	}
	if disabled.Status != "disabled" || disabled.Revision != member.Revision+1 {
		t.Fatalf("unexpected disabled membership: %#v", disabled)
	}
	if _, err := identityService.Authenticate(ctx, memberLogin.Token); !errors.Is(
		err,
		identity.ErrSessionNotFound,
	) {
		t.Fatalf("disabled member session remained active: %v", err)
	}
}
