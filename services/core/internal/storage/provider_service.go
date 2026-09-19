package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type capabilityProbe interface {
	ProbeCapabilities(context.Context) (map[string]bool, []string, error)
}

func (service *Service) ListProviders(
	ctx context.Context,
	workspaceID string,
) ([]Provider, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, ErrInvalidRootInput
	}
	return service.repository.ListProviders(ctx, workspaceID)
}

func (service *Service) Provider(
	ctx context.Context,
	workspaceID string,
	id string,
) (Provider, error) {
	return service.repository.Provider(
		ctx,
		strings.TrimSpace(workspaceID),
		strings.TrimSpace(id),
	)
}

func (service *Service) CreateProvider(
	ctx context.Context,
	input CreateProviderInput,
) (Provider, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Kind = strings.ToLower(strings.TrimSpace(input.Kind))
	input.Name = strings.TrimSpace(input.Name)
	if input.WorkspaceID == "" ||
		len([]rune(input.Name)) < 1 ||
		len([]rune(input.Name)) > 120 {
		return Provider{}, ErrInvalidRootInput
	}
	config, secret, err := normalizeProviderInput(ctx, input)
	if err != nil {
		return Provider{}, err
	}
	id, err := newID()
	if err != nil {
		return Provider{}, err
	}
	var secretRef string
	if len(secret) > 0 {
		if service.secrets == nil {
			return Provider{}, errors.New("storage secret store is unavailable")
		}
		reference, err := service.secrets.Put(
			ctx,
			input.WorkspaceID,
			"storage."+input.Kind+".credentials",
			secret,
		)
		if err != nil {
			return Provider{}, err
		}
		secretRef = reference.ID
	}
	item, err := service.repository.CreateProvider(
		ctx,
		createProviderRecord{
			Provider: Provider{
				ID:           id,
				WorkspaceID:  input.WorkspaceID,
				Kind:         input.Kind,
				Name:         input.Name,
				Status:       "offline",
				Config:       config,
				Capabilities: map[string]bool{},
				Revision:     1,
			},
			SecretRef: secretRef,
			Now:       service.clock().UTC(),
		},
	)
	if err != nil && secretRef != "" {
		_ = service.secrets.Delete(
			context.WithoutCancel(ctx),
			input.WorkspaceID,
			secretRef,
		)
	}
	return item, err
}

func (service *Service) UpdateProvider(
	ctx context.Context,
	input UpdateProviderInput,
) (Provider, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ID = strings.TrimSpace(input.ID)
	input.Name = strings.TrimSpace(input.Name)
	if input.WorkspaceID == "" || input.ID == "" ||
		len([]rune(input.Name)) < 1 ||
		len([]rune(input.Name)) > 120 ||
		input.Revision < 1 {
		return Provider{}, ErrInvalidRootInput
	}
	current, err := service.repository.ResolvedProvider(
		ctx,
		input.WorkspaceID,
		input.ID,
	)
	if err != nil {
		return Provider{}, err
	}
	createInput := CreateProviderInput{
		WorkspaceID:         input.WorkspaceID,
		Kind:                current.Kind,
		Name:                input.Name,
		Endpoint:            input.Endpoint,
		BasePath:            input.BasePath,
		Region:              input.Region,
		Bucket:              input.Bucket,
		PathStyle:           input.PathStyle,
		AllowPrivateNetwork: input.AllowPrivateNetwork,
		Username:            input.Username,
		Password:            input.Password,
		AccessKeyID:         input.AccessKeyID,
		SecretAccessKey:     input.SecretAccessKey,
	}
	preserveSecret := false
	switch current.Kind {
	case "webdav":
		existingSecret, err := service.loadProviderSecret(
			ctx,
			input.WorkspaceID,
			current.SecretRef,
		)
		if err != nil {
			return Provider{}, err
		}
		if len(existingSecret) > 0 {
			var credentials webDAVCredentials
			if err := json.Unmarshal(existingSecret, &credentials); err != nil {
				return Provider{}, errors.New("stored webdav credentials are invalid")
			}
			if strings.TrimSpace(createInput.Username) == "" {
				createInput.Username = credentials.Username
			}
			if createInput.Password == "" {
				createInput.Password = credentials.Password
			}
			preserveSecret =
				strings.TrimSpace(createInput.Username) == credentials.Username &&
					createInput.Password == credentials.Password
		}
	case "s3":
		existingSecret, err := service.loadProviderSecret(
			ctx,
			input.WorkspaceID,
			current.SecretRef,
		)
		if err != nil {
			return Provider{}, err
		}
		if len(existingSecret) > 0 {
			var credentials s3Credentials
			if err := json.Unmarshal(existingSecret, &credentials); err != nil {
				return Provider{}, errors.New("stored s3 credentials are invalid")
			}
			if strings.TrimSpace(createInput.AccessKeyID) == "" {
				createInput.AccessKeyID = credentials.AccessKeyID
			}
			if createInput.SecretAccessKey == "" {
				createInput.SecretAccessKey = credentials.SecretAccessKey
			}
			preserveSecret =
				strings.TrimSpace(createInput.AccessKeyID) == credentials.AccessKeyID &&
					createInput.SecretAccessKey == credentials.SecretAccessKey
		}
	}
	config, secret, err := normalizeProviderInput(ctx, createInput)
	if err != nil {
		return Provider{}, err
	}
	if preserveSecret && providerEndpointChanged(current, config, current.Kind) {
		return Provider{}, ErrProviderEndpointChanged
	}
	if preserveSecret {
		secret = nil
	}
	var newSecretRef *string
	if len(secret) > 0 {
		if service.secrets == nil {
			return Provider{}, errors.New("storage secret store is unavailable")
		}
		reference, err := service.secrets.Put(
			ctx,
			input.WorkspaceID,
			"storage."+current.Kind+".credentials",
			secret,
		)
		if err != nil {
			return Provider{}, err
		}
		newSecretRef = &reference.ID
	}
	item, err := service.repository.UpdateProvider(ctx, updateProviderRecord{
		WorkspaceID:  input.WorkspaceID,
		ID:           input.ID,
		Name:         input.Name,
		Config:       config,
		SecretRef:    newSecretRef,
		Capabilities: map[string]bool{},
		Revision:     input.Revision,
		Now:          service.clock().UTC(),
	})
	if err != nil {
		if newSecretRef != nil {
			_ = service.secrets.Delete(
				context.WithoutCancel(ctx),
				input.WorkspaceID,
				*newSecretRef,
			)
		}
		return Provider{}, err
	}
	if newSecretRef != nil && current.SecretRef != "" {
		_ = service.secrets.Delete(
			context.WithoutCancel(ctx),
			input.WorkspaceID,
			current.SecretRef,
		)
	}
	return item, nil
}

// providerEndpointChanged reports whether the normalized remote identity that
// credentials are bound to has changed. Retained credentials must not be sent
// to a different endpoint, base path, region, or bucket.
func providerEndpointChanged(current providerRecord, config map[string]string, kind string) bool {
	switch kind {
	case "webdav":
		return current.Config["endpoint"] != config["endpoint"] ||
			current.Config["basePath"] != config["basePath"]
	case "s3":
		return current.Config["endpoint"] != config["endpoint"] ||
			current.Config["region"] != config["region"] ||
			current.Config["bucket"] != config["bucket"] ||
			current.Config["pathStyle"] != config["pathStyle"]
	default:
		return true
	}
}

func (service *Service) TestProvider(
	ctx context.Context,
	workspaceID string,
	id string,
) (ConnectionReport, error) {
	startedAt := service.clock().UTC()
	record, err := service.repository.ResolvedProvider(ctx, workspaceID, id)
	if err != nil {
		return ConnectionReport{}, err
	}
	secret, err := service.loadProviderSecret(
		ctx,
		record.WorkspaceID,
		record.SecretRef,
	)
	if err != nil {
		service.recordProviderFailure(ctx, record, err)
		return ConnectionReport{}, err
	}
	adapter, err := service.registry.Open(ctx, AdapterConfig{
		WorkspaceID: record.WorkspaceID,
		ProviderID:  record.ID,
		Kind:        record.Kind,
		Config:      record.Config,
		Secret:      secret,
	})
	if err != nil {
		service.recordProviderFailure(ctx, record, err)
		return ConnectionReport{}, err
	}
	capabilities := adapter.Capabilities()
	warnings := make([]string, 0)
	if probe, ok := adapter.(capabilityProbe); ok {
		capabilities, warnings, err = probe.ProbeCapabilities(ctx)
	} else {
		_, err = adapter.List(ctx, "")
	}
	if err == nil && capabilities["write"] {
		probeWarnings := service.probeWriteCapabilities(
			ctx,
			adapter,
			capabilities,
		)
		warnings = append(warnings, probeWarnings...)
	}
	testedAt := service.clock().UTC()
	if err != nil {
		service.recordProviderFailure(ctx, record, err)
		return ConnectionReport{}, err
	}
	if _, err := service.repository.UpdateProviderTest(
		context.WithoutCancel(ctx),
		record.WorkspaceID,
		record.ID,
		"succeeded",
		capabilities,
		"",
		"",
		testedAt,
	); err != nil {
		return ConnectionReport{}, err
	}
	return ConnectionReport{
		ProviderID: record.ID, Status: "succeeded",
		Capabilities: capabilities, Warnings: warnings,
		Latency: testedAt.Sub(startedAt), TestedAt: testedAt,
	}, nil
}

func (service *Service) DeleteProvider(
	ctx context.Context,
	input ProviderStateInput,
) error {
	record, err := service.repository.ResolvedProvider(
		ctx,
		input.WorkspaceID,
		input.ID,
	)
	if err != nil {
		return err
	}
	if err := service.repository.DeleteProvider(
		ctx,
		input,
		service.clock().UTC(),
	); err != nil {
		return err
	}
	if record.SecretRef != "" && service.secrets != nil {
		_ = service.secrets.Delete(
			context.WithoutCancel(ctx),
			input.WorkspaceID,
			record.SecretRef,
		)
	}
	return nil
}

func (service *Service) ProviderDeleteImpact(
	ctx context.Context,
	workspaceID string,
	id string,
) (StorageLocationDeleteImpact, error) {
	return service.repository.ProviderDeleteImpact(
		ctx,
		strings.TrimSpace(workspaceID),
		strings.TrimSpace(id),
	)
}

func (service *Service) RegisterProviderRoot(
	ctx context.Context,
	input RegisterProviderRootInput,
) (AuthorizedRoot, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ProviderID = strings.TrimSpace(input.ProviderID)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.BasePath = strings.TrimSpace(input.BasePath)
	input.Mode = strings.TrimSpace(input.Mode)
	if input.WorkspaceID == "" || input.ProviderID == "" ||
		len([]rune(input.DisplayName)) < 1 ||
		len([]rune(input.DisplayName)) > 120 ||
		(input.Mode != "referenced" && input.Mode != "managed") {
		return AuthorizedRoot{}, ErrInvalidRootInput
	}
	basePath, err := NormalizeObjectKey(input.BasePath)
	if err != nil {
		return AuthorizedRoot{}, err
	}
	provider, err := service.repository.Provider(
		ctx,
		input.WorkspaceID,
		input.ProviderID,
	)
	if err != nil {
		return AuthorizedRoot{}, err
	}
	if provider.Status != "active" {
		return AuthorizedRoot{}, ErrRootUnavailable
	}
	rootID, err := newID()
	if err != nil {
		return AuthorizedRoot{}, err
	}
	pathSecretID, err := newID()
	if err != nil {
		return AuthorizedRoot{}, err
	}
	return service.repository.RegisterProviderRoot(
		ctx,
		registerProviderRootRecord{
			RootID: rootID, PathSecretID: pathSecretID,
			WorkspaceID: input.WorkspaceID, ProviderID: input.ProviderID,
			DisplayName: input.DisplayName, BasePath: basePath,
			Mode: input.Mode, ScanEnabled: input.ScanEnabled,
			Now: service.clock().UTC(),
		},
	)
}

func normalizeProviderInput(
	ctx context.Context,
	input CreateProviderInput,
) (map[string]string, []byte, error) {
	switch input.Kind {
	case "webdav":
		endpoint, err := validateRemoteEndpoint(
			ctx,
			input.Endpoint,
			input.AllowPrivateNetwork,
		)
		if err != nil {
			return nil, nil, err
		}
		basePath, err := NormalizeObjectKey(input.BasePath)
		if err != nil {
			return nil, nil, err
		}
		input.Username = strings.TrimSpace(input.Username)
		if input.Username == "" && input.Password != "" {
			return nil, nil, ErrInvalidRootInput
		}
		config := map[string]string{
			"endpoint":            endpoint.String(),
			"basePath":            basePath,
			"allowPrivateNetwork": fmt.Sprint(input.AllowPrivateNetwork),
		}
		if input.Username == "" {
			return config, nil, nil
		}
		secret, err := json.Marshal(webDAVCredentials{
			Username: input.Username,
			Password: input.Password,
		})
		return config, secret, err
	case "s3":
		return normalizeS3ProviderInput(ctx, input)
	default:
		return nil, nil, ErrProviderUnsupported
	}
}

func (service *Service) loadProviderSecret(
	ctx context.Context,
	workspaceID string,
	reference string,
) ([]byte, error) {
	if reference == "" {
		return nil, nil
	}
	if service.secrets == nil {
		return nil, errors.New("storage secret store is unavailable")
	}
	return service.secrets.Get(ctx, workspaceID, reference)
}

func (service *Service) recordProviderFailure(
	ctx context.Context,
	record providerRecord,
	testErr error,
) {
	_, _ = service.repository.UpdateProviderTest(
		context.WithoutCancel(ctx),
		record.WorkspaceID,
		record.ID,
		"failed",
		record.Capabilities,
		providerErrorCode(testErr),
		safeProviderError(testErr),
		service.clock().UTC(),
	)
}

func (service *Service) probeWriteCapabilities(
	ctx context.Context,
	adapter Adapter,
	capabilities map[string]bool,
) []string {
	id, err := newID()
	if err != nil {
		return []string{"无法生成写入能力探测标识"}
	}
	source := ".review-studio-probe-" + id + ".txt"
	copyKey := ".review-studio-probe-" + id + "-copy.txt"
	moveKey := ".review-studio-probe-" + id + "-move.txt"
	payload := []byte("review-studio-storage-probe")
	cleanup := func() {
		for _, key := range []string{source, copyKey, moveKey} {
			_ = adapter.Delete(context.WithoutCancel(ctx), key)
		}
	}
	defer cleanup()
	if _, err := adapter.Put(
		ctx,
		source,
		bytes.NewReader(payload),
		int64(len(payload)),
		"text/plain",
	); err != nil {
		capabilities["write"] = false
		return []string{"服务器拒绝写入探测"}
	}
	reader, _, err := adapter.OpenRange(
		ctx,
		source,
		ByteRange{Offset: 7, Length: 6},
	)
	if err != nil {
		capabilities["rangeRead"] = false
	} else {
		_, _ = io.Copy(io.Discard, reader)
		_ = reader.Close()
	}
	warnings := make([]string, 0)
	if capabilities["copy"] {
		if err := adapter.Copy(ctx, source, copyKey); err != nil {
			capabilities["copy"] = false
			warnings = append(warnings, "服务器拒绝复制探测")
		}
	}
	if capabilities["move"] {
		if err := adapter.Move(ctx, source, moveKey); err != nil {
			capabilities["move"] = false
			warnings = append(warnings, "服务器拒绝移动探测")
		}
	}
	deleteKey := source
	if capabilities["move"] {
		deleteKey = moveKey
	}
	if capabilities["delete"] {
		if err := adapter.Delete(ctx, deleteKey); err != nil {
			capabilities["delete"] = false
			warnings = append(warnings, "服务器拒绝删除探测文件")
		}
	}
	return warnings
}

func providerErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrAuthenticationFailed):
		return "storage.authentication_failed"
	case errors.Is(err, ErrEndpointForbidden):
		return "storage.endpoint_forbidden"
	case errors.Is(err, ErrRemoteRateLimited):
		return "storage.rate_limited"
	case errors.Is(err, context.DeadlineExceeded):
		return "storage.timeout"
	default:
		return "storage.connection_failed"
	}
}

func safeProviderError(err error) string {
	switch providerErrorCode(err) {
	case "storage.authentication_failed":
		return "远程存储凭据无效或权限不足"
	case "storage.endpoint_forbidden":
		return "远程地址不符合网络安全策略。请检查系统代理或 DNS 是否把域名解析到本机、内网、链路本地、元数据地址或 198.18.x.x 这类代理保留地址；确需连接内网地址时，请勾选“允许连接局域网或内网地址”。"
	case "storage.rate_limited":
		return "远程存储正在限流，请稍后重试"
	case "storage.timeout":
		return "远程存储连接超时"
	default:
		return "远程存储连接失败"
	}
}
