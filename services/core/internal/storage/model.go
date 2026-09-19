package storage

import "time"

type Provider struct {
	ID               string
	WorkspaceID      string
	Kind             string
	Name             string
	Status           string
	Config           map[string]string
	Capabilities     map[string]bool
	Revision         int
	CreatedAt        time.Time
	UpdatedAt        time.Time
	LastTestAt       *time.Time
	LastTestStatus   *string
	LastErrorCode    *string
	LastErrorMessage *string
}

type CreateProviderInput struct {
	WorkspaceID         string
	Kind                string
	Name                string
	Endpoint            string
	BasePath            string
	Region              string
	Bucket              string
	PathStyle           bool
	AllowPrivateNetwork bool
	Username            string
	Password            string
	AccessKeyID         string
	SecretAccessKey     string
}

type UpdateProviderInput struct {
	WorkspaceID         string
	ID                  string
	Name                string
	Endpoint            string
	BasePath            string
	Region              string
	Bucket              string
	PathStyle           bool
	AllowPrivateNetwork bool
	Username            string
	Password            string
	AccessKeyID         string
	SecretAccessKey     string
	Revision            int
}

type ProviderStateInput struct {
	WorkspaceID string
	ID          string
	Revision    int
}

type LocalManagedBucketStateInput struct {
	WorkspaceID string
	ID          string
	Revision    int
}

type ConnectionReport struct {
	ProviderID   string
	Status       string
	Capabilities map[string]bool
	Warnings     []string
	Latency      time.Duration
	TestedAt     time.Time
}

type RegisterProviderRootInput struct {
	WorkspaceID string
	ProviderID  string
	DisplayName string
	BasePath    string
	Mode        string
	ScanEnabled bool
}

type AuthorizedRoot struct {
	ID                string
	WorkspaceID       string
	StorageProviderID string
	DisplayName       string
	DisplayPath       string
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

type RegisterLocalRootInput struct {
	WorkspaceID string
	DisplayName string
	LocalPath   string
	Mode        string
	ScanEnabled bool
}

type LocalManagedBucket struct {
	ID                   string
	WorkspaceID          string
	AuthorizedRootID     string
	StorageProviderID    string
	DisplayName          string
	DisplayPath          string
	Purpose              string
	QuotaBytes           *int64
	UsedBytesEstimate    *int64
	UploadSecurityPolicy string
	ProjectAvailable     bool
	Status               string
	CreatedBy            *string
	Revision             int
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type LocalManagedBucketCapacity struct {
	Bucket              LocalManagedBucket
	UsedBytesEstimate   int64
	QuotaBytes          *int64
	QuotaAvailableBytes *int64
	DiskAvailableBytes  *int64
}

type StorageLocationDeleteImpact struct {
	TargetType       string
	ProviderID       string
	ProviderName     string
	ProviderKind     string
	AuthorizedRootID *string
	BucketID         *string
	BucketName       *string
	CanDelete        bool
	CanDisable       bool
	Counts           StorageLocationImpactCounts
	BlockingReasons  []string
}

type StorageLocationImpactCounts struct {
	AuthorizedRoots   int
	ProjectGrants     int
	ProjectSelections int
	StorageObjects    int
	AssetVersions     int
	Renditions        int
	ReviewSessions    int
	PendingJobs       int
	StorageCopyTasks  int
	UploadChecks      int
}

type CreateLocalManagedBucketInput struct {
	WorkspaceID          string
	DisplayName          string
	LocalPath            string
	Purpose              string
	QuotaBytes           *int64
	UploadSecurityPolicy string
	ProjectAvailable     bool
	CreatedBy            string
}

type UpdateLocalManagedBucketInput struct {
	WorkspaceID          string
	ID                   string
	DisplayName          string
	Purpose              string
	QuotaBytes           *int64
	UploadSecurityPolicy string
	ProjectAvailable     bool
	Status               string
	Revision             int
}

type FileInfo struct {
	ObjectKey  string
	Name       string
	Kind       string
	SizeBytes  int64
	ModifiedAt time.Time
	MIMEType   string
}

type DirectoryEntry struct {
	ObjectKey  string
	Name       string
	Kind       string
	SizeBytes  int64
	ModifiedAt time.Time
}

type ByteRange struct {
	Offset int64
	Length int64
}

type ObservedFile struct {
	ObjectKey        string
	SizeBytes        int64
	ModifiedAt       time.Time
	MIMEType         string
	QuickFingerprint string
}

type StoredObject struct {
	ID                string
	WorkspaceID       string
	StorageProviderID string
	AuthorizedRootID  string
	ObjectKey         string
	Status            string
	SizeBytes         int64
	ModifiedAt        time.Time
	QuickFingerprint  string
	MIMEType          string
	FirstDiscoveredAt time.Time
	LastSeenAt        *time.Time
	MissingSince      *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type CopyTask struct {
	ID                    string
	WorkspaceID           string
	SourceStorageObjectID string
	SourceRootID          string
	SourceObjectKey       string
	TargetRootID          string
	TargetObjectKey       string
	TargetStorageObjectID *string
	JobID                 *string
	Status                string
	TotalBytes            int64
	CopiedBytes           int64
	ContentHash           *string
	ContentHashAlgorithm  *string
	TempFileName          string
	ErrorCode             *string
	ErrorMessage          *string
	Revision              int
	CreatedAt             time.Time
	UpdatedAt             time.Time
	CompletedAt           *time.Time
}

type ProjectArchivePlan struct {
	TargetRootID string
	Objects      []StoredObject
}

type CreateCopyTaskInput struct {
	WorkspaceID           string
	SourceStorageObjectID string
	SourceAssetID         string
	SourceAssetVersionID  string
	TargetRootID          string
	TargetObjectKey       string
}

type CopyTaskStateInput struct {
	WorkspaceID string
	ID          string
}

type ScanSummary struct {
	Discovered int `json:"discovered"`
	New        int `json:"new"`
	Modified   int `json:"modified"`
	Moved      int `json:"moved"`
	Missing    int `json:"missing"`
	Recovered  int `json:"recovered"`
	Unchanged  int `json:"unchanged"`
}

type ScanResult struct {
	ID          string
	RootID      string
	Status      string
	Summary     ScanSummary
	StartedAt   time.Time
	CompletedAt time.Time
}

type UpdateRootInput struct {
	WorkspaceID string
	ID          string
	DisplayName string
	ScanEnabled bool
	Revision    int
}
