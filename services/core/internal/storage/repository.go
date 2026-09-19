package storage

import (
	"context"
	"errors"
	"time"
)

var (
	ErrRootNotFound           = errors.New("authorized root not found")
	ErrObjectNotFound         = errors.New("storage object not found")
	ErrPathInvalid            = errors.New("relative path is invalid")
	ErrPathEscapesRoot        = errors.New("path escapes authorized root")
	ErrPathChanged            = errors.New("path changed while opening")
	ErrNotRegularFile         = errors.New("path is not a regular file")
	ErrRangeInvalid           = errors.New("byte range is invalid")
	ErrRootUnavailable        = errors.New("authorized root is unavailable")
	ErrRevisionConflict       = errors.New("authorized root revision conflict")
	ErrScanLimitExceeded      = errors.New("directory scan limit exceeded")
	ErrInvalidRootInput       = errors.New("authorized root input is invalid")
	ErrProviderUnsupported    = errors.New("storage provider is unsupported")
	ErrProviderNotFound       = errors.New("storage provider not found")
	ErrProviderNameConflict   = errors.New("storage provider name already exists")
	ErrProviderInUse          = errors.New("storage provider is in use")
	ErrEndpointForbidden      = errors.New("storage endpoint is forbidden")
	ErrCapabilityUnsupported  = errors.New("storage capability is unsupported")
	ErrAuthenticationFailed   = errors.New("storage authentication failed")
	ErrRemoteRateLimited      = errors.New("remote storage rate limited")
	ErrBucketNotFound         = errors.New("local managed bucket not found")
	ErrBucketRevisionConflict = errors.New(
		"local managed bucket revision conflict",
	)
	ErrProviderEndpointChanged = errors.New(
		"storage provider endpoint changed; re-enter credentials",
	)
)

// maxDirectoryListingEntries bounds a single List call so an authorized but
// hostile directory or remote provider cannot force Core to materialise an
// unbounded number of entries (SAR-F63).
const maxDirectoryListingEntries = 50000

type rootRecord struct {
	ID                string
	WorkspaceID       string
	StorageProviderID string
	ProviderKind      string
	PathSecretID      string
	DisplayName       string
	DisplayPath       string
	LocalPath         string
	ProviderConfig    map[string]string
	ProviderSecretRef string
	Mode              string
	ScanEnabled       bool
	Status            string
	Revision          int
	CreatedAt         time.Time
	UpdatedAt         time.Time
	LastScanAt        *time.Time
	LastScanStatus    *string
	LastScanSummary   *ScanSummary
}

type registerRootRecord struct {
	ProviderID   string
	RootID       string
	PathSecretID string
	WorkspaceID  string
	DisplayName  string
	DisplayPath  string
	LocalPath    string
	Mode         string
	ScanEnabled  bool
	Now          time.Time
}

type createLocalManagedBucketRecord struct {
	BucketID             string
	ProviderID           string
	RootID               string
	PathSecretID         string
	WorkspaceID          string
	DisplayName          string
	DisplayPath          string
	LocalPath            string
	Purpose              string
	QuotaBytes           *int64
	UploadSecurityPolicy string
	ProjectAvailable     bool
	Status               string
	CreatedBy            string
	Now                  time.Time
}

type providerRecord struct {
	Provider
	SecretRef string
}

type createProviderRecord struct {
	Provider
	SecretRef string
	Now       time.Time
}

type updateProviderRecord struct {
	WorkspaceID  string
	ID           string
	Name         string
	Config       map[string]string
	SecretRef    *string
	Capabilities map[string]bool
	Revision     int
	Now          time.Time
}

type registerProviderRootRecord struct {
	RootID       string
	PathSecretID string
	WorkspaceID  string
	ProviderID   string
	DisplayName  string
	BasePath     string
	Mode         string
	ScanEnabled  bool
	Now          time.Time
}

type createCopyTaskRecord struct {
	ID          string
	WorkspaceID string
	Source      StoredObject
	TargetRoot  AuthorizedRoot
	TargetKey   string
	TempFile    string
	Now         time.Time
}

type copiedObjectRecord struct {
	WorkspaceID string
	RootID      string
	Observed    ObservedFile
	ContentHash string
	Algorithm   string
	Now         time.Time
}

type Repository interface {
	ListProviders(context.Context, string) ([]Provider, error)
	Provider(context.Context, string, string) (Provider, error)
	ResolvedProvider(context.Context, string, string) (providerRecord, error)
	CreateProvider(context.Context, createProviderRecord) (Provider, error)
	UpdateProvider(context.Context, updateProviderRecord) (Provider, error)
	UpdateProviderTest(
		context.Context,
		string,
		string,
		string,
		map[string]bool,
		string,
		string,
		time.Time,
	) (Provider, error)
	ProviderDeleteImpact(
		context.Context,
		string,
		string,
	) (StorageLocationDeleteImpact, error)
	DeleteProvider(context.Context, ProviderStateInput, time.Time) error
	RegisterLocalRoot(
		ctx context.Context,
		record registerRootRecord,
	) (AuthorizedRoot, error)
	RegisterProviderRoot(
		context.Context,
		registerProviderRootRecord,
	) (AuthorizedRoot, error)
	ListRoots(ctx context.Context, workspaceID string) ([]AuthorizedRoot, error)
	Root(ctx context.Context, workspaceID, rootID string) (AuthorizedRoot, error)
	ResolvedRoot(ctx context.Context, workspaceID, rootID string) (rootRecord, error)
	UpdateRoot(
		ctx context.Context,
		input UpdateRootInput,
		now time.Time,
	) (AuthorizedRoot, error)
	DeleteRoot(
		ctx context.Context,
		workspaceID string,
		rootID string,
		revision int,
		now time.Time,
	) error
	ListLocalManagedBuckets(
		context.Context,
		string,
	) ([]LocalManagedBucket, error)
	LocalManagedBucket(
		context.Context,
		string,
		string,
	) (LocalManagedBucket, error)
	CreateLocalManagedBucket(
		context.Context,
		createLocalManagedBucketRecord,
	) (LocalManagedBucket, error)
	UpdateLocalManagedBucket(
		context.Context,
		UpdateLocalManagedBucketInput,
		time.Time,
	) (LocalManagedBucket, error)
	LocalManagedBucketDeleteImpact(
		context.Context,
		string,
		string,
	) (StorageLocationDeleteImpact, error)
	DeleteLocalManagedBucket(
		context.Context,
		LocalManagedBucketStateInput,
		time.Time,
	) error
	StartScan(
		ctx context.Context,
		workspaceID string,
		rootID string,
		scanID string,
		now time.Time,
	) error
	ApplyScan(
		ctx context.Context,
		workspaceID string,
		rootID string,
		scanID string,
		observed []ObservedFile,
		now time.Time,
	) (ScanSummary, error)
	FailScan(
		ctx context.Context,
		workspaceID string,
		rootID string,
		scanID string,
		code string,
		message string,
		now time.Time,
	) error
	ListObjects(
		ctx context.Context,
		workspaceID string,
		rootID string,
	) ([]StoredObject, error)
	Object(
		ctx context.Context,
		workspaceID string,
		objectID string,
	) (StoredObject, error)
	ObjectForAsset(context.Context, string, string) (StoredObject, error)
	ObjectForAssetVersion(context.Context, string, string) (StoredObject, error)
	ProjectArchivePlan(context.Context, string, string) (ProjectArchivePlan, error)
	CreateCopyTask(context.Context, createCopyTaskRecord) (CopyTask, error)
	AttachCopyTaskJob(context.Context, string, string, string, time.Time) (CopyTask, error)
	CopyTask(context.Context, string, string) (CopyTask, error)
	UpdateCopyTaskRunning(context.Context, string, string, time.Time) error
	UpdateCopyTaskProgress(context.Context, string, string, int64, int64, time.Time) error
	CompleteCopyTask(
		context.Context,
		string,
		string,
		string,
		string,
		string,
		time.Time,
	) (CopyTask, error)
	FailCopyTask(context.Context, string, string, string, string, bool, time.Time) error
	UpsertCopiedObject(context.Context, copiedObjectRecord) (StoredObject, error)
}
