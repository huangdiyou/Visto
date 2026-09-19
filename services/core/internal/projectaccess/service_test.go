package projectaccess

import (
	"context"
	"testing"
	"time"
)

type fakeRepository struct {
	membership Membership
}

func (repository fakeRepository) ActiveMembership(
	context.Context,
	string,
	string,
	string,
	time.Time,
) (Membership, error) {
	return repository.membership, nil
}

func TestOwnerBypassesProjectMembership(t *testing.T) {
	service := NewService(fakeRepository{})
	allowed, err := service.Allowed(context.Background(), CheckInput{
		WorkspaceID:   "workspace-1",
		ProjectID:     "project-1",
		UserID:        "owner-1",
		WorkspaceRole: "owner",
		Permission:    PermissionProjectManage,
	})
	if err != nil {
		t.Fatalf("check permission: %v", err)
	}
	if !allowed {
		t.Fatal("owner should be allowed")
	}
}

func TestProjectRoleDefaultsAndOverrides(t *testing.T) {
	tests := []struct {
		name       string
		role       string
		overrides  map[Permission]bool
		permission Permission
		allowed    bool
	}{
		{
			name:       "supervisor manages members",
			role:       "supervisor",
			permission: PermissionProjectMembersManage,
			allowed:    true,
		},
		{
			name:       "member uploads by default",
			role:       "member",
			permission: PermissionAssetsUpload,
			allowed:    true,
		},
		{
			name:       "member does not manage members by default",
			role:       "member",
			permission: PermissionProjectMembersManage,
			allowed:    false,
		},
		{
			name:       "member can submit decisions by default",
			role:       "member",
			permission: PermissionReviewsDecide,
			allowed:    true,
		},
		{
			name:       "override can remove member upload",
			role:       "member",
			overrides:  map[Permission]bool{PermissionAssetsUpload: false},
			permission: PermissionAssetsUpload,
			allowed:    false,
		},
		{
			name:       "guest reads by default",
			role:       "guest",
			permission: PermissionProjectRead,
			allowed:    true,
		},
		{
			name:       "guest can receive upload grant",
			role:       "guest",
			overrides:  map[Permission]bool{PermissionAssetsUpload: true},
			permission: PermissionAssetsUpload,
			allowed:    true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewService(fakeRepository{membership: Membership{
				RoleKey:     test.role,
				Status:      "active",
				Permissions: test.overrides,
			}})
			allowed, err := service.Allowed(context.Background(), CheckInput{
				WorkspaceID: "workspace-1",
				ProjectID:   "project-1",
				UserID:      "user-1",
				Permission:  test.permission,
			})
			if err != nil {
				t.Fatalf("check permission: %v", err)
			}
			if allowed != test.allowed {
				t.Fatalf("allowed = %t, want %t", allowed, test.allowed)
			}
		})
	}
}

func TestEffectivePermissionsIncludesRoleDefaults(t *testing.T) {
	effective := EffectivePermissions("supervisor", map[Permission]bool{
		PermissionProjectManage: false,
	})

	if effective[PermissionProjectManage] {
		t.Fatal("explicit override should remove supervisor project management")
	}
	if !effective[PermissionProjectMembersManage] {
		t.Fatal("supervisor role default should include member management")
	}
	if !effective[PermissionAssetsUpload] {
		t.Fatal("supervisor role default should include asset upload")
	}
}
