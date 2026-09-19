package httpapi

import (
	"errors"
	"net/http"
	"time"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/authorization"
	"review-studio.local/core/internal/storage"
)

type storageProviderRequest struct {
	Kind                string `json:"kind"`
	Name                string `json:"name"`
	Endpoint            string `json:"endpoint"`
	BasePath            string `json:"basePath"`
	Region              string `json:"region"`
	Bucket              string `json:"bucket"`
	PathStyle           bool   `json:"pathStyle"`
	AllowPrivateNetwork bool   `json:"allowPrivateNetwork"`
	Username            string `json:"username"`
	Password            string `json:"password"`
	AccessKeyID         string `json:"accessKeyId"`
	SecretAccessKey     string `json:"secretAccessKey"`
	Revision            int    `json:"revision,omitempty"`
}

type storageProviderResponse struct {
	ID                  string          `json:"id"`
	WorkspaceID         string          `json:"workspaceId"`
	Kind                string          `json:"kind"`
	Name                string          `json:"name"`
	Status              string          `json:"status"`
	Endpoint            string          `json:"endpoint"`
	BasePath            string          `json:"basePath"`
	Region              string          `json:"region,omitempty"`
	Bucket              string          `json:"bucket,omitempty"`
	PathStyle           bool            `json:"pathStyle"`
	AllowPrivateNetwork bool            `json:"allowPrivateNetwork"`
	Capabilities        map[string]bool `json:"capabilities"`
	Revision            int             `json:"revision"`
	CreatedAt           time.Time       `json:"createdAt"`
	UpdatedAt           time.Time       `json:"updatedAt"`
	LastTestAt          *time.Time      `json:"lastTestAt"`
	LastTestStatus      *string         `json:"lastTestStatus"`
	LastErrorCode       *string         `json:"lastErrorCode"`
	LastErrorMessage    *string         `json:"lastErrorMessage"`
}

type storageProviderListResponse struct {
	Items []storageProviderResponse `json:"items"`
}

type storageConnectionResponse struct {
	ProviderID   string          `json:"providerId"`
	Status       string          `json:"status"`
	Capabilities map[string]bool `json:"capabilities"`
	Warnings     []string        `json:"warnings"`
	LatencyMS    int64           `json:"latencyMs"`
	TestedAt     time.Time       `json:"testedAt"`
}

type providerRootRequest struct {
	DisplayName string `json:"displayName"`
	BasePath    string `json:"basePath"`
	Mode        string `json:"mode"`
	ScanEnabled bool   `json:"scanEnabled"`
}

func (h *handler) handleListStorageProviders(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requirePermission(
		response,
		request,
		authorization.PermissionStorageManage,
	)
	if !ok {
		return
	}
	items, err := h.storage.ListProviders(
		request.Context(),
		session.Workspace.ID,
	)
	if err != nil {
		h.internalError(response, request, err)
		return
	}
	result := make([]storageProviderResponse, 0, len(items))
	for _, item := range items {
		result = append(result, toStorageProviderResponse(item))
	}
	writeJSON(response, http.StatusOK, storageProviderListResponse{Items: result})
}

func (h *handler) handleCreateStorageProvider(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requirePermission(
		response,
		request,
		authorization.PermissionStorageManage,
	)
	if !ok {
		return
	}
	var body storageProviderRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	item, err := h.storage.CreateProvider(
		request.Context(),
		storage.CreateProviderInput{
			WorkspaceID:         session.Workspace.ID,
			Kind:                body.Kind,
			Name:                body.Name,
			Endpoint:            body.Endpoint,
			BasePath:            body.BasePath,
			Region:              body.Region,
			Bucket:              body.Bucket,
			PathStyle:           body.PathStyle,
			AllowPrivateNetwork: body.AllowPrivateNetwork,
			Username:            body.Username,
			Password:            body.Password,
			AccessKeyID:         body.AccessKeyID,
			SecretAccessKey:     body.SecretAccessKey,
		},
	)
	if err != nil {
		h.handleStorageProviderError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID: session.Workspace.ID, ActorType: "user",
		ActorID: session.User.ID, Action: "storage.provider_created",
		ResourceType: "storage_provider", ResourceID: item.ID,
		After: map[string]any{"kind": item.Kind, "name": item.Name},
	})
	writeJSON(response, http.StatusCreated, toStorageProviderResponse(item))
}

func (h *handler) handleUpdateStorageProvider(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requirePermission(
		response,
		request,
		authorization.PermissionStorageManage,
	)
	if !ok {
		return
	}
	var body storageProviderRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	item, err := h.storage.UpdateProvider(
		request.Context(),
		storage.UpdateProviderInput{
			WorkspaceID:         session.Workspace.ID,
			ID:                  request.PathValue("providerId"),
			Name:                body.Name,
			Endpoint:            body.Endpoint,
			BasePath:            body.BasePath,
			Region:              body.Region,
			Bucket:              body.Bucket,
			PathStyle:           body.PathStyle,
			AllowPrivateNetwork: body.AllowPrivateNetwork,
			Username:            body.Username,
			Password:            body.Password,
			AccessKeyID:         body.AccessKeyID,
			SecretAccessKey:     body.SecretAccessKey,
			Revision:            body.Revision,
		},
	)
	if err != nil {
		h.handleStorageProviderError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID: session.Workspace.ID, ActorType: "user",
		ActorID: session.User.ID, Action: "storage.provider_updated",
		ResourceType: "storage_provider", ResourceID: item.ID,
		After: map[string]any{
			"kind": item.Kind, "name": item.Name, "revision": item.Revision,
		},
	})
	writeJSON(response, http.StatusOK, toStorageProviderResponse(item))
}

func (h *handler) handleTestStorageProvider(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requirePermission(
		response,
		request,
		authorization.PermissionStorageManage,
	)
	if !ok {
		return
	}
	id := request.PathValue("providerId")
	report, err := h.storage.TestProvider(
		request.Context(),
		session.Workspace.ID,
		id,
	)
	if err != nil {
		h.recordAudit(request, audit.RecordLogInput{
			WorkspaceID: session.Workspace.ID, ActorType: "user",
			ActorID: session.User.ID, Action: "storage.provider_test_failed",
			ResourceType: "storage_provider", ResourceID: id,
			After: map[string]any{"errorCode": storageProviderErrorCode(err)},
		})
		h.handleStorageProviderError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID: session.Workspace.ID, ActorType: "user",
		ActorID: session.User.ID, Action: "storage.provider_test_succeeded",
		ResourceType: "storage_provider", ResourceID: id,
		After: map[string]any{"capabilities": report.Capabilities},
	})
	writeJSON(response, http.StatusOK, storageConnectionResponse{
		ProviderID: report.ProviderID, Status: report.Status,
		Capabilities: report.Capabilities, Warnings: report.Warnings,
		LatencyMS: report.Latency.Milliseconds(), TestedAt: report.TestedAt,
	})
}

func (h *handler) handleDeleteStorageProvider(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requirePermission(
		response,
		request,
		authorization.PermissionStorageManage,
	)
	if !ok {
		return
	}
	var body revisionRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	id := request.PathValue("providerId")
	if err := h.storage.DeleteProvider(
		request.Context(),
		storage.ProviderStateInput{
			WorkspaceID: session.Workspace.ID,
			ID:          id,
			Revision:    body.Revision,
		},
	); err != nil {
		h.handleStorageProviderError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID: session.Workspace.ID, ActorType: "user",
		ActorID: session.User.ID, Action: "storage.provider_deleted",
		ResourceType: "storage_provider", ResourceID: id,
	})
	response.WriteHeader(http.StatusNoContent)
}

func (h *handler) handleCreateStorageProviderRoot(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requirePermission(
		response,
		request,
		authorization.PermissionStorageManage,
	)
	if !ok {
		return
	}
	var body providerRootRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	root, err := h.storage.RegisterProviderRoot(
		request.Context(),
		storage.RegisterProviderRootInput{
			WorkspaceID: session.Workspace.ID,
			ProviderID:  request.PathValue("providerId"),
			DisplayName: body.DisplayName,
			BasePath:    body.BasePath,
			Mode:        defaultString(body.Mode, "referenced"),
			ScanEnabled: body.ScanEnabled,
		},
	)
	if err != nil {
		h.handleStorageProviderError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID: session.Workspace.ID, ActorType: "user",
		ActorID: session.User.ID, Action: "storage.provider_root_created",
		ResourceType: "authorized_root", ResourceID: root.ID,
		After: map[string]any{
			"providerId": root.StorageProviderID,
			"mode":       root.Mode,
		},
	})
	writeJSON(response, http.StatusCreated, toAuthorizedRootResponse(root))
}

func toStorageProviderResponse(
	item storage.Provider,
) storageProviderResponse {
	return storageProviderResponse{
		ID: item.ID, WorkspaceID: item.WorkspaceID, Kind: item.Kind,
		Name: item.Name, Status: item.Status,
		Endpoint: item.Config["endpoint"], BasePath: item.Config["basePath"],
		Region: item.Config["region"], Bucket: item.Config["bucket"],
		PathStyle:           item.Config["pathStyle"] == "true",
		AllowPrivateNetwork: item.Config["allowPrivateNetwork"] == "true",
		Capabilities:        item.Capabilities, Revision: item.Revision,
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		LastTestAt: item.LastTestAt, LastTestStatus: item.LastTestStatus,
		LastErrorCode:    item.LastErrorCode,
		LastErrorMessage: item.LastErrorMessage,
	}
}

func (h *handler) handleStorageProviderError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, storage.ErrProviderNotFound):
		writeError(response, http.StatusNotFound, requestID(response),
			"storage.provider_not_found", "远程存储不存在")
	case errors.Is(err, storage.ErrProviderNameConflict):
		writeError(response, http.StatusConflict, requestID(response),
			"storage.provider_name_conflict", "已经存在同名远程存储")
	case errors.Is(err, storage.ErrProviderInUse):
		writeError(response, http.StatusConflict, requestID(response),
			"storage.provider_in_use", "这个存储仍被授权位置、媒体、审阅或任务引用，请先停用、迁移或清理引用后再删除。")
	case errors.Is(err, storage.ErrRevisionConflict):
		writeError(response, http.StatusConflict, requestID(response),
			"resource.revision_conflict", "存储设置已更新，请刷新后重试")
	case errors.Is(err, storage.ErrEndpointForbidden):
		writeError(response, http.StatusBadRequest, requestID(response),
			"storage.endpoint_forbidden", "远程地址不符合网络安全策略。请检查系统代理或 DNS 是否把域名解析到本机、内网、链路本地、元数据地址或 198.18.x.x 这类代理保留地址；确需连接内网地址时，请勾选“允许连接局域网或内网地址”。")
	case errors.Is(err, storage.ErrAuthenticationFailed):
		writeError(response, http.StatusBadGateway, requestID(response),
			"storage.authentication_failed", "远程存储凭据无效或权限不足")
	case errors.Is(err, storage.ErrRemoteRateLimited):
		response.Header().Set("Retry-After", "30")
		writeError(response, http.StatusTooManyRequests, requestID(response),
			"storage.rate_limited", "远程存储正在限流，请稍后重试")
	case errors.Is(err, storage.ErrCapabilityUnsupported):
		writeError(response, http.StatusUnprocessableEntity, requestID(response),
			"storage.capability_unsupported", "远程存储不支持所需能力")
	case errors.Is(err, storage.ErrRootUnavailable):
		writeError(response, http.StatusServiceUnavailable, requestID(response),
			"storage.unavailable", "远程存储暂时不可用")
	case errors.Is(err, storage.ErrInvalidRootInput),
		errors.Is(err, storage.ErrPathInvalid),
		errors.Is(err, storage.ErrProviderUnsupported):
		h.badRequest(response, request, err)
	default:
		h.internalError(response, request, err)
	}
}

func storageProviderErrorCode(err error) string {
	switch {
	case errors.Is(err, storage.ErrAuthenticationFailed):
		return "storage.authentication_failed"
	case errors.Is(err, storage.ErrEndpointForbidden):
		return "storage.endpoint_forbidden"
	case errors.Is(err, storage.ErrRemoteRateLimited):
		return "storage.rate_limited"
	case errors.Is(err, storage.ErrRootUnavailable):
		return "storage.unavailable"
	default:
		return "storage.connection_failed"
	}
}
