package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"review-studio.local/core/internal/secretstore"
)

type Clock func() time.Time

type Service struct {
	repository          Repository
	registry            *ProviderRegistry
	secrets             secretstore.Store
	clock               Clock
	copyTempDir         string
	stagingReservations *StagingReservationTracker
}

func NewService(repository Repository) *Service {
	return NewServiceWithRegistry(repository, NewDefaultProviderRegistry())
}

func NewServiceWithRegistry(
	repository Repository,
	registry *ProviderRegistry,
) *Service {
	return NewServiceWithDependencies(repository, registry, nil)
}

func NewServiceWithDependencies(
	repository Repository,
	registry *ProviderRegistry,
	secrets secretstore.Store,
) *Service {
	if registry == nil {
		registry = NewDefaultProviderRegistry()
	}
	return &Service{
		repository:          repository,
		registry:            registry,
		secrets:             secrets,
		clock:               time.Now,
		copyTempDir:         filepath.Join(os.TempDir(), "review-studio-storage-copy"),
		stagingReservations: NewStagingReservationTracker(),
	}
}

func (service *Service) StagingReservations() *StagingReservationTracker {
	if service == nil {
		return nil
	}
	if service.stagingReservations == nil {
		service.stagingReservations = NewStagingReservationTracker()
	}
	return service.stagingReservations
}

func (service *Service) SetCopyTempDir(path string) {
	if strings.TrimSpace(path) != "" {
		service.copyTempDir = path
	}
}

func (service *Service) RegisterLocalRoot(
	ctx context.Context,
	input RegisterLocalRootInput,
) (AuthorizedRoot, error) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.LocalPath = strings.TrimSpace(input.LocalPath)
	input.Mode = strings.TrimSpace(input.Mode)

	if len([]rune(input.DisplayName)) < 1 ||
		len([]rune(input.DisplayName)) > 120 {
		return AuthorizedRoot{}, fmt.Errorf(
			"%w: display name must contain 1 to 120 characters",
			ErrInvalidRootInput,
		)
	}
	if input.Mode != "referenced" && input.Mode != "managed" {
		return AuthorizedRoot{}, fmt.Errorf(
			"%w: mode must be referenced or managed",
			ErrInvalidRootInput,
		)
	}

	localRoot, err := NewLocalRoot(input.LocalPath)
	if err != nil {
		return AuthorizedRoot{}, err
	}

	providerID, err := newID()
	if err != nil {
		return AuthorizedRoot{}, err
	}
	rootID, err := newID()
	if err != nil {
		return AuthorizedRoot{}, err
	}
	secretID, err := newID()
	if err != nil {
		return AuthorizedRoot{}, err
	}

	return service.repository.RegisterLocalRoot(ctx, registerRootRecord{
		ProviderID:   providerID,
		RootID:       rootID,
		PathSecretID: secretID,
		WorkspaceID:  input.WorkspaceID,
		DisplayName:  input.DisplayName,
		DisplayPath:  displayPath(localRoot.rootPath),
		LocalPath:    localRoot.rootPath,
		Mode:         input.Mode,
		ScanEnabled:  input.ScanEnabled,
		Now:          service.clock().UTC(),
	})
}

func (service *Service) ListRoots(
	ctx context.Context,
	workspaceID string,
) ([]AuthorizedRoot, error) {
	return service.repository.ListRoots(ctx, workspaceID)
}

func (service *Service) Root(
	ctx context.Context,
	workspaceID string,
	rootID string,
) (AuthorizedRoot, error) {
	return service.repository.Root(ctx, workspaceID, rootID)
}

func (service *Service) UpdateRoot(
	ctx context.Context,
	input UpdateRootInput,
) (AuthorizedRoot, error) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if len([]rune(input.DisplayName)) < 1 ||
		len([]rune(input.DisplayName)) > 120 {
		return AuthorizedRoot{}, fmt.Errorf(
			"%w: display name must contain 1 to 120 characters",
			ErrInvalidRootInput,
		)
	}
	if input.Revision < 1 {
		return AuthorizedRoot{}, fmt.Errorf(
			"%w: revision must be positive",
			ErrInvalidRootInput,
		)
	}
	return service.repository.UpdateRoot(ctx, input, service.clock().UTC())
}

func (service *Service) ListLocalManagedBuckets(
	ctx context.Context,
	workspaceID string,
) ([]LocalManagedBucket, error) {
	return service.repository.ListLocalManagedBuckets(ctx, workspaceID)
}

func (service *Service) LocalManagedBucket(
	ctx context.Context,
	workspaceID string,
	bucketID string,
) (LocalManagedBucket, error) {
	return service.repository.LocalManagedBucket(ctx, workspaceID, bucketID)
}

func (service *Service) LocalManagedBucketCapacityForRoot(
	ctx context.Context,
	workspaceID string,
	rootID string,
) (LocalManagedBucketCapacity, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	rootID = strings.TrimSpace(rootID)
	if workspaceID == "" || rootID == "" {
		return LocalManagedBucketCapacity{}, fmt.Errorf(
			"%w: bucket root identifier is required",
			ErrInvalidRootInput,
		)
	}
	buckets, err := service.repository.ListLocalManagedBuckets(ctx, workspaceID)
	if err != nil {
		return LocalManagedBucketCapacity{}, err
	}
	var matched *LocalManagedBucket
	for index := range buckets {
		if buckets[index].AuthorizedRootID == rootID {
			matched = &buckets[index]
			break
		}
	}
	if matched == nil {
		return LocalManagedBucketCapacity{}, ErrBucketNotFound
	}
	used := int64(0)
	if matched.UsedBytesEstimate != nil {
		used = *matched.UsedBytesEstimate
	}
	var available *int64
	if matched.QuotaBytes != nil {
		value := *matched.QuotaBytes - used
		if value < 0 {
			value = 0
		}
		available = &value
	}

	var diskAvailable *int64
	record, err := service.repository.ResolvedRoot(ctx, workspaceID, rootID)
	if err != nil {
		return LocalManagedBucketCapacity{}, err
	}
	if record.Status != "available" {
		return LocalManagedBucketCapacity{}, ErrRootUnavailable
	}
	if value, err := diskAvailableBytes(record.LocalPath); err == nil && value >= 0 {
		diskAvailable = &value
	}
	return LocalManagedBucketCapacity{
		Bucket:              *matched,
		UsedBytesEstimate:   used,
		QuotaBytes:          matched.QuotaBytes,
		QuotaAvailableBytes: available,
		DiskAvailableBytes:  diskAvailable,
	}, nil
}

func (service *Service) CreateLocalManagedBucket(
	ctx context.Context,
	input CreateLocalManagedBucketInput,
) (LocalManagedBucket, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.LocalPath = strings.TrimSpace(input.LocalPath)
	input.Purpose = defaultBucketPurpose(input.Purpose)
	input.UploadSecurityPolicy = defaultBucketSecurityPolicy(
		input.UploadSecurityPolicy,
	)

	if err := validateBucketName(input.DisplayName); err != nil {
		return LocalManagedBucket{}, err
	}
	if input.WorkspaceID == "" {
		return LocalManagedBucket{}, fmt.Errorf(
			"%w: workspace is required",
			ErrInvalidRootInput,
		)
	}
	if input.LocalPath == "" {
		return LocalManagedBucket{}, fmt.Errorf(
			"%w: bucket path is required",
			ErrInvalidRootInput,
		)
	}
	if !validBucketPurpose(input.Purpose) {
		return LocalManagedBucket{}, fmt.Errorf(
			"%w: bucket purpose is invalid",
			ErrInvalidRootInput,
		)
	}
	if !validBucketSecurityPolicy(input.UploadSecurityPolicy) {
		return LocalManagedBucket{}, fmt.Errorf(
			"%w: upload security policy is invalid",
			ErrInvalidRootInput,
		)
	}
	if input.QuotaBytes != nil && *input.QuotaBytes < 0 {
		return LocalManagedBucket{}, fmt.Errorf(
			"%w: quota bytes must be zero or greater",
			ErrInvalidRootInput,
		)
	}

	localRoot, err := prepareManagedBucketRoot(input.LocalPath)
	if err != nil {
		return LocalManagedBucket{}, err
	}

	providerID, err := newID()
	if err != nil {
		return LocalManagedBucket{}, err
	}
	rootID, err := newID()
	if err != nil {
		return LocalManagedBucket{}, err
	}
	secretID, err := newID()
	if err != nil {
		return LocalManagedBucket{}, err
	}
	bucketID, err := newID()
	if err != nil {
		return LocalManagedBucket{}, err
	}

	return service.repository.CreateLocalManagedBucket(
		ctx,
		createLocalManagedBucketRecord{
			BucketID:             bucketID,
			ProviderID:           providerID,
			RootID:               rootID,
			PathSecretID:         secretID,
			WorkspaceID:          input.WorkspaceID,
			DisplayName:          input.DisplayName,
			DisplayPath:          displayPath(localRoot.rootPath),
			LocalPath:            localRoot.rootPath,
			Purpose:              input.Purpose,
			QuotaBytes:           input.QuotaBytes,
			UploadSecurityPolicy: input.UploadSecurityPolicy,
			ProjectAvailable:     input.ProjectAvailable,
			Status:               "active",
			CreatedBy:            strings.TrimSpace(input.CreatedBy),
			Now:                  service.clock().UTC(),
		},
	)
}

func (service *Service) UpdateLocalManagedBucket(
	ctx context.Context,
	input UpdateLocalManagedBucketInput,
) (LocalManagedBucket, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ID = strings.TrimSpace(input.ID)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Purpose = defaultBucketPurpose(input.Purpose)
	input.UploadSecurityPolicy = defaultBucketSecurityPolicy(
		input.UploadSecurityPolicy,
	)
	input.Status = strings.TrimSpace(input.Status)
	if input.Status == "" {
		input.Status = "active"
	}

	if input.WorkspaceID == "" || input.ID == "" {
		return LocalManagedBucket{}, fmt.Errorf(
			"%w: bucket identifier is required",
			ErrInvalidRootInput,
		)
	}
	if err := validateBucketName(input.DisplayName); err != nil {
		return LocalManagedBucket{}, err
	}
	if !validBucketPurpose(input.Purpose) {
		return LocalManagedBucket{}, fmt.Errorf(
			"%w: bucket purpose is invalid",
			ErrInvalidRootInput,
		)
	}
	if !validBucketSecurityPolicy(input.UploadSecurityPolicy) {
		return LocalManagedBucket{}, fmt.Errorf(
			"%w: upload security policy is invalid",
			ErrInvalidRootInput,
		)
	}
	if !validBucketStatus(input.Status) {
		return LocalManagedBucket{}, fmt.Errorf(
			"%w: bucket status is invalid",
			ErrInvalidRootInput,
		)
	}
	if input.QuotaBytes != nil && *input.QuotaBytes < 0 {
		return LocalManagedBucket{}, fmt.Errorf(
			"%w: quota bytes must be zero or greater",
			ErrInvalidRootInput,
		)
	}
	if input.Revision < 1 {
		return LocalManagedBucket{}, fmt.Errorf(
			"%w: revision must be positive",
			ErrInvalidRootInput,
		)
	}
	return service.repository.UpdateLocalManagedBucket(
		ctx,
		input,
		service.clock().UTC(),
	)
}

func (service *Service) LocalManagedBucketDeleteImpact(
	ctx context.Context,
	workspaceID string,
	bucketID string,
) (StorageLocationDeleteImpact, error) {
	return service.repository.LocalManagedBucketDeleteImpact(
		ctx,
		strings.TrimSpace(workspaceID),
		strings.TrimSpace(bucketID),
	)
}

func (service *Service) DeleteLocalManagedBucket(
	ctx context.Context,
	input LocalManagedBucketStateInput,
) error {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ID = strings.TrimSpace(input.ID)
	if input.WorkspaceID == "" || input.ID == "" {
		return fmt.Errorf(
			"%w: bucket identifier is required",
			ErrInvalidRootInput,
		)
	}
	if input.Revision < 1 {
		return fmt.Errorf(
			"%w: revision must be positive",
			ErrInvalidRootInput,
		)
	}
	return service.repository.DeleteLocalManagedBucket(
		ctx,
		input,
		service.clock().UTC(),
	)
}

func (service *Service) DeleteRoot(
	ctx context.Context,
	workspaceID string,
	rootID string,
	revision int,
) error {
	if revision < 1 {
		return fmt.Errorf(
			"%w: revision must be positive",
			ErrInvalidRootInput,
		)
	}
	return service.repository.DeleteRoot(
		ctx,
		workspaceID,
		rootID,
		revision,
		service.clock().UTC(),
	)
}

func (service *Service) ScanRoot(
	ctx context.Context,
	workspaceID string,
	rootID string,
) (ScanResult, error) {
	scanID, err := newID()
	if err != nil {
		return ScanResult{}, err
	}
	startedAt := service.clock().UTC()
	if err := service.repository.StartScan(
		ctx,
		workspaceID,
		rootID,
		scanID,
		startedAt,
	); err != nil {
		return ScanResult{}, err
	}

	adapter, err := service.Adapter(ctx, workspaceID, rootID)
	if err != nil {
		service.failScan(ctx, workspaceID, rootID, scanID, err)
		return ScanResult{}, err
	}
	observed, err := scanRoot(ctx, adapter)
	if err != nil {
		service.failScan(ctx, workspaceID, rootID, scanID, err)
		return ScanResult{}, err
	}

	completedAt := service.clock().UTC()
	summary, err := service.repository.ApplyScan(
		ctx,
		workspaceID,
		rootID,
		scanID,
		observed,
		completedAt,
	)
	if err != nil {
		service.failScan(ctx, workspaceID, rootID, scanID, err)
		return ScanResult{}, err
	}
	return ScanResult{
		ID:          scanID,
		RootID:      rootID,
		Status:      "succeeded",
		Summary:     summary,
		StartedAt:   startedAt,
		CompletedAt: completedAt,
	}, nil
}

func (service *Service) ListObjects(
	ctx context.Context,
	workspaceID string,
	rootID string,
) ([]StoredObject, error) {
	return service.repository.ListObjects(ctx, workspaceID, rootID)
}

func (service *Service) Object(
	ctx context.Context,
	workspaceID string,
	objectID string,
) (StoredObject, error) {
	return service.repository.Object(ctx, workspaceID, objectID)
}

func (service *Service) LocalAdapter(
	ctx context.Context,
	workspaceID string,
	rootID string,
) (*LocalRoot, error) {
	record, err := service.repository.ResolvedRoot(ctx, workspaceID, rootID)
	if err != nil {
		return nil, err
	}
	if record.Status != "available" {
		return nil, ErrRootUnavailable
	}
	return NewLocalRoot(record.LocalPath)
}

func (service *Service) Adapter(
	ctx context.Context,
	workspaceID string,
	rootID string,
) (Adapter, error) {
	record, err := service.repository.ResolvedRoot(ctx, workspaceID, rootID)
	if err != nil {
		return nil, err
	}
	if record.Status != "available" {
		return nil, ErrRootUnavailable
	}
	secret, err := service.loadProviderSecret(
		ctx,
		record.WorkspaceID,
		record.ProviderSecretRef,
	)
	if err != nil {
		return nil, err
	}
	return service.registry.Open(ctx, AdapterConfig{
		WorkspaceID: record.WorkspaceID,
		ProviderID:  record.StorageProviderID,
		RootID:      record.ID,
		Kind:        record.ProviderKind,
		LocalPath:   record.LocalPath,
		Mode:        record.Mode,
		Config:      record.ProviderConfig,
		Secret:      secret,
	})
}

func (service *Service) failScan(
	ctx context.Context,
	workspaceID string,
	rootID string,
	scanID string,
	scanErr error,
) {
	code := "scan.failed"
	switch {
	case errors.Is(scanErr, ErrRootUnavailable), errors.Is(scanErr, os.ErrNotExist):
		code = "scan.root_missing"
	case errors.Is(scanErr, os.ErrPermission):
		code = "scan.permission_lost"
	case errors.Is(scanErr, ErrScanLimitExceeded):
		code = "scan.limit_exceeded"
	}
	_ = service.repository.FailScan(
		context.WithoutCancel(ctx),
		workspaceID,
		rootID,
		scanID,
		code,
		scanErr.Error(),
		service.clock().UTC(),
	)
}

func displayPath(value string) string {
	home, err := os.UserHomeDir()
	if err == nil {
		if relative, relErr := filepath.Rel(home, value); relErr == nil {
			if relative == "." {
				return "~"
			}
			if relative != ".." &&
				!strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
				return filepath.ToSlash(filepath.Join("~", relative))
			}
		}
	}

	volume := filepath.VolumeName(value)
	base := filepath.Base(value)
	if volume != "" {
		return fmt.Sprintf("%s/…/%s", volume, base)
	}
	return "…/" + base
}

func prepareManagedBucketRoot(localPath string) (*LocalRoot, error) {
	if err := os.MkdirAll(localPath, 0o700); err != nil {
		return nil, fmt.Errorf("create bucket directory: %w", err)
	}
	root, err := newLocalRoot(localPath, true)
	if err != nil {
		return nil, err
	}
	if filepath.Dir(root.rootPath) == root.rootPath {
		return nil, fmt.Errorf(
			"%w: bucket path cannot be a filesystem root",
			ErrInvalidRootInput,
		)
	}
	probePath := filepath.Join(
		root.rootPath,
		".review-studio-write-test-"+uuid.NewString(),
	)
	if err := os.WriteFile(probePath, []byte("ok"), 0o600); err != nil {
		return nil, fmt.Errorf("write bucket probe: %w", err)
	}
	if err := os.Remove(probePath); err != nil {
		return nil, fmt.Errorf("remove bucket probe: %w", err)
	}
	return root, nil
}

func validateBucketName(value string) error {
	if len([]rune(value)) < 1 || len([]rune(value)) > 120 {
		return fmt.Errorf(
			"%w: display name must contain 1 to 120 characters",
			ErrInvalidRootInput,
		)
	}
	return nil
}

func defaultBucketPurpose(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "upload"
	}
	return value
}

func validBucketPurpose(value string) bool {
	switch value {
	case "upload", "source_archive", "review_upload":
		return true
	default:
		return false
	}
}

func defaultBucketSecurityPolicy(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "standard"
	}
	return value
}

func validBucketSecurityPolicy(value string) bool {
	switch value {
	case "quick", "standard", "enhanced":
		return true
	default:
		return false
	}
}

func validBucketStatus(value string) bool {
	switch value {
	case "active", "disabled", "error":
		return true
	default:
		return false
	}
}

func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}
