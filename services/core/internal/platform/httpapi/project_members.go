package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/catalog"
	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/projectaccess"
	"review-studio.local/core/internal/projectmember"
)

type projectMemberRequest struct {
	UserID      string          `json:"userId"`
	RoleKey     string          `json:"roleKey"`
	Status      string          `json:"status,omitempty"`
	Permissions map[string]bool `json:"permissions"`
	ExpiresAt   *time.Time      `json:"expiresAt"`
	Revision    int             `json:"revision,omitempty"`
}

type projectTransferRequest struct {
	NewPrimaryUserID  string `json:"newPrimaryUserId"`
	FormerOwnerAction string `json:"formerOwnerAction"`
}

type projectGuestRequest struct {
	Email       *string         `json:"email"`
	DisplayName string          `json:"displayName"`
	Locale      string          `json:"locale"`
	Permissions map[string]bool `json:"permissions"`
	ExpiresAt   *time.Time      `json:"expiresAt"`
}

type projectMemberResponse struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspaceId"`
	ProjectID   string          `json:"projectId"`
	UserID      string          `json:"userId"`
	Email       *string         `json:"email"`
	DisplayName string          `json:"displayName"`
	RoleKey     string          `json:"roleKey"`
	Status      string          `json:"status"`
	Permissions map[string]bool `json:"permissions"`
	ExpiresAt   *time.Time      `json:"expiresAt"`
	JoinedAt    *time.Time      `json:"joinedAt"`
	RemovedAt   *time.Time      `json:"removedAt"`
	Revision    int             `json:"revision"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

type projectMemberListResponse struct {
	Items []projectMemberResponse `json:"items"`
}

func (h *handler) handleListProjectMembers(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	projectID := request.PathValue("projectId")
	if !h.requireProjectPermission(
		response,
		request,
		session,
		projectID,
		projectaccess.PermissionProjectRead,
	) {
		return
	}
	items, err := h.projectMembers.List(
		request.Context(),
		session.Workspace.ID,
		projectID,
	)
	if err != nil {
		h.handleProjectMemberError(response, request, err)
		return
	}
	result := make([]projectMemberResponse, 0, len(items))
	for _, item := range items {
		result = append(result, toProjectMemberResponse(item))
	}
	writeJSON(response, http.StatusOK, projectMemberListResponse{Items: result})
}

func (h *handler) handleAddProjectMember(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	projectID := request.PathValue("projectId")
	if !h.requireProjectPermission(
		response,
		request,
		session,
		projectID,
		projectaccess.PermissionProjectMembersManage,
	) {
		return
	}
	var body projectMemberRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	if !h.requireProjectPermissionSubset(
		response,
		request,
		session,
		projectID,
		body.RoleKey,
		body.Permissions,
	) {
		return
	}
	item, err := h.projectMembers.Add(
		request.Context(),
		projectmember.AddInput{
			WorkspaceID: session.Workspace.ID,
			ProjectID:   projectID,
			UserID:      body.UserID,
			RoleKey:     body.RoleKey,
			Permissions: body.Permissions,
			ExpiresAt:   body.ExpiresAt,
			CreatedBy:   session.User.ID,
		},
	)
	if err != nil {
		h.handleProjectMemberError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "project.member_added",
		ResourceType: "project",
		ResourceID:   projectID,
		After: map[string]any{
			"userId":  item.UserID,
			"roleKey": item.RoleKey,
		},
	})
	writeJSON(response, http.StatusCreated, toProjectMemberResponse(item))
}

func (h *handler) handleCreateProjectGuest(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	projectID := request.PathValue("projectId")
	if !h.requireProjectPermission(
		response,
		request,
		session,
		projectID,
		projectaccess.PermissionProjectMembersManage,
	) {
		return
	}
	var body projectGuestRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	if !h.requireProjectPermissionSubset(
		response,
		request,
		session,
		projectID,
		"guest",
		body.Permissions,
	) {
		return
	}
	item, err := h.projectMembers.CreateGuest(
		request.Context(),
		projectmember.CreateGuestInput{
			WorkspaceID: session.Workspace.ID,
			ProjectID:   projectID,
			Email:       body.Email,
			DisplayName: body.DisplayName,
			Locale:      body.Locale,
			Permissions: body.Permissions,
			ExpiresAt:   body.ExpiresAt,
			CreatedBy:   session.User.ID,
		},
	)
	if err != nil {
		h.handleProjectMemberError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "project.guest_created",
		ResourceType: "project",
		ResourceID:   projectID,
		After: map[string]any{
			"userId":    item.UserID,
			"expiresAt": item.ExpiresAt,
		},
	})
	writeJSON(response, http.StatusCreated, toProjectMemberResponse(item))
}

func (h *handler) handleUpdateProjectMember(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	projectID := request.PathValue("projectId")
	if !h.requireProjectPermission(
		response,
		request,
		session,
		projectID,
		projectaccess.PermissionProjectMembersManage,
	) {
		return
	}
	var body projectMemberRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	if !h.requireProjectPermissionSubset(
		response,
		request,
		session,
		projectID,
		body.RoleKey,
		body.Permissions,
	) {
		return
	}
	item, err := h.projectMembers.Update(
		request.Context(),
		projectmember.UpdateInput{
			WorkspaceID: session.Workspace.ID,
			ProjectID:   projectID,
			ID:          request.PathValue("membershipId"),
			RoleKey:     body.RoleKey,
			Status:      body.Status,
			Permissions: body.Permissions,
			ExpiresAt:   body.ExpiresAt,
			Revision:    body.Revision,
		},
	)
	if err != nil {
		h.handleProjectMemberError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "project.member_updated",
		ResourceType: "project",
		ResourceID:   projectID,
		After: map[string]any{
			"membershipId": item.ID,
			"roleKey":      item.RoleKey,
			"status":       item.Status,
		},
	})
	writeJSON(response, http.StatusOK, toProjectMemberResponse(item))
}

func (h *handler) handleRemoveProjectMember(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	projectID := request.PathValue("projectId")
	if !h.requireProjectPermission(
		response,
		request,
		session,
		projectID,
		projectaccess.PermissionProjectMembersManage,
	) {
		return
	}
	var body revisionRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	err := h.projectMembers.Remove(
		request.Context(),
		projectmember.RemoveInput{
			WorkspaceID: session.Workspace.ID,
			ProjectID:   projectID,
			ID:          request.PathValue("membershipId"),
			Revision:    body.Revision,
		},
	)
	if err != nil {
		h.handleProjectMemberError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "project.member_removed",
		ResourceType: "project",
		ResourceID:   projectID,
		After:        map[string]any{"membershipId": request.PathValue("membershipId")},
	})
	response.WriteHeader(http.StatusNoContent)
}

func (h *handler) handleTransferProjectPrimaryOwner(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	projectID := request.PathValue("projectId")
	project, err := h.catalog.Project(
		request.Context(),
		session.Workspace.ID,
		projectID,
	)
	if err != nil {
		h.handleCatalogError(response, request, err)
		return
	}
	if !canTransferProject(session, project) {
		writeError(
			response,
			http.StatusForbidden,
			requestID(response),
			"permission.project_transfer_denied",
			"只有 Owner 或项目主负责人可以转交项目",
		)
		return
	}
	var body projectTransferRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	item, err := h.projectMembers.Transfer(
		request.Context(),
		projectmember.TransferInput{
			WorkspaceID:        session.Workspace.ID,
			ProjectID:          projectID,
			NewPrimaryUserID:   body.NewPrimaryUserID,
			FormerOwnerAction:  body.FormerOwnerAction,
			ActorUserID:        session.User.ID,
			ActorWorkspaceRole: session.Role,
		},
	)
	if err != nil {
		h.handleProjectMemberError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "project.primary_owner_transferred",
		ResourceType: "project",
		ResourceID:   projectID,
		After: map[string]any{
			"newPrimaryUserId": item.UserID,
			"formerAction":     strings.TrimSpace(body.FormerOwnerAction),
		},
	})
	writeJSON(response, http.StatusOK, toProjectMemberResponse(item))
}

func canTransferProject(session identity.Session, project catalog.Project) bool {
	if strings.EqualFold(session.Role, "owner") {
		return true
	}
	return project.PrimaryOwnerUserID != nil &&
		*project.PrimaryOwnerUserID == session.User.ID
}

func (h *handler) requireProjectPermissionSubset(
	response http.ResponseWriter,
	request *http.Request,
	session identity.Session,
	projectID string,
	roleKey string,
	requested map[string]bool,
) bool {
	allowed, err := h.projectAccess.Permissions(
		request.Context(),
		projectaccess.PermissionInput{
			WorkspaceID:   session.Workspace.ID,
			ProjectID:     projectID,
			UserID:        session.User.ID,
			WorkspaceRole: session.Role,
		},
	)
	if err != nil {
		h.internalError(response, request, err)
		return false
	}
	targetOverrides := make(map[projectaccess.Permission]bool, len(requested))
	for permission, enabled := range requested {
		targetOverrides[projectaccess.Permission(permission)] = enabled
	}
	targetPermissions := projectaccess.EffectivePermissions(roleKey, targetOverrides)
	for permission, enabled := range targetPermissions {
		if !enabled {
			continue
		}
		if !allowed[projectaccess.Permission(permission)] {
			writeError(
				response,
				http.StatusForbidden,
				requestID(response),
				"project_member.permission_escalation",
				"不能授予当前账号自己没有的项目权限",
			)
			return false
		}
	}
	return true
}

func (h *handler) handleProjectMemberError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, projectmember.ErrNotFound):
		writeError(response, http.StatusNotFound, requestID(response),
			"project_member.not_found", "项目成员不存在")
	case errors.Is(err, projectmember.ErrRevisionConflict):
		writeError(response, http.StatusConflict, requestID(response),
			"project_member.revision_conflict", "项目成员已被更新，请刷新后重试")
	case errors.Is(err, projectmember.ErrAlreadyMember):
		writeError(response, http.StatusConflict, requestID(response),
			"project_member.already_exists", "该账号已经在项目中")
	case errors.Is(err, projectmember.ErrWorkspaceMemberNeeded):
		writeError(response, http.StatusBadRequest, requestID(response),
			"project_member.workspace_member_required", "只能选择工作空间内的账号")
	case errors.Is(err, projectmember.ErrEmailInUse):
		writeError(response, http.StatusConflict, requestID(response),
			"project_member.email_in_use", "该邮箱已经被其他账号使用")
	case errors.Is(err, projectmember.ErrExpiryInvalid):
		writeError(response, http.StatusBadRequest, requestID(response),
			"project_member.expiry_invalid", "项目 Guest 有效期必须是未来时间")
	case errors.Is(err, projectmember.ErrPrimaryOwnerProtected):
		writeError(response, http.StatusForbidden, requestID(response),
			"project_member.primary_owner_protected", "项目主负责人不能被这样修改或移除")
	case errors.Is(err, projectmember.ErrTransferForbidden):
		writeError(response, http.StatusForbidden, requestID(response),
			"project_member.transfer_forbidden", "只有 Owner 或当前项目主负责人可以转交项目")
	case errors.Is(err, projectmember.ErrInvalidInput):
		h.badRequest(response, request, err)
	default:
		h.internalError(response, request, err)
	}
}

func toProjectMemberResponse(item projectmember.Member) projectMemberResponse {
	return projectMemberResponse{
		ID:          item.ID,
		WorkspaceID: item.WorkspaceID,
		ProjectID:   item.ProjectID,
		UserID:      item.UserID,
		Email:       item.Email,
		DisplayName: item.DisplayName,
		RoleKey:     item.RoleKey,
		Status:      item.Status,
		Permissions: item.Permissions,
		ExpiresAt:   item.ExpiresAt,
		JoinedAt:    item.JoinedAt,
		RemovedAt:   item.RemovedAt,
		Revision:    item.Revision,
		CreatedAt:   item.CreatedAt,
		UpdatedAt:   item.UpdatedAt,
	}
}
