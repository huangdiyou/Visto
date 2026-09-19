package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"review-studio.local/core/internal/storage"
)

var (
	ErrLibraryAssetNotFound       = errors.New("media library asset not found")
	ErrLibraryAssetNameInvalid    = errors.New("media library asset name is invalid")
	ErrLibraryProjectInvalid      = errors.New("media library project is invalid")
	ErrLibraryVersionNotFound     = errors.New("media library asset version not found")
	ErrLibraryRevisionConflict    = errors.New("media library asset revision conflict")
	ErrLibraryUploadUnavailable   = errors.New("media library version upload is unavailable")
	ErrLibraryUploadInvalid       = errors.New("media library version upload is invalid")
	ErrLibraryUploadTargetNeeded  = errors.New("media library upload target is required")
	ErrLibraryUploadTargetInvalid = errors.New("media library upload target is invalid")
	ErrUploadCheckNotFound        = errors.New("upload check not found")
	ErrUploadCheckInvalid         = errors.New("upload check request is invalid")
	ErrUploadCheckConflict        = errors.New("upload check status conflict")
	ErrProjectAssetNotFound       = errors.New("project asset relation not found")
	ErrProjectAssetInvalid        = errors.New("project asset relation is invalid")
	ErrProjectAssetAlreadyLinked  = errors.New("asset already belongs to project")
)

type LibraryQuery struct {
	Search       string
	MediaType    string
	State        string
	ProjectID    string
	ModifiedFrom *time.Time
	ModifiedTo   *time.Time
	Sort         string
	Page         int
	PageSize     int
}

type LibraryJob struct {
	ID              string
	Type            string
	Status          string
	Priority        int
	SubjectType     string
	SubjectID       string
	ProgressCurrent *int64
	ProgressTotal   *int64
	ProgressUnit    *string
	ErrorCode       *string
	ErrorMessage    *string
	AttemptCount    int
	MaxAttempts     int
	CreatedAt       time.Time
	UpdatedAt       time.Time
	StartedAt       *time.Time
	CompletedAt     *time.Time
}

type LibraryItem struct {
	Object      storage.StoredObject
	Asset       *LibraryAsset
	Probe       *Metadata
	Renditions  []Rendition
	Jobs        []LibraryJob
	UploadCheck *UploadCheck
}

type LibraryAsset struct {
	ID            string
	ProjectID     *string
	ProjectName   *string
	Name          string
	Type          string
	Revision      int
	VersionID     string
	VersionNumber int
}

type ProjectAsset struct {
	ID             string
	WorkspaceID    string
	ProjectID      string
	ProjectName    string
	AssetID        string
	AssetName      string
	AssetType      string
	Status         string
	AddedBy        string
	TrashedBy      *string
	TrashedAt      *time.Time
	TrashExpiresAt *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type AssetVersion struct {
	ID                  string
	AssetID             string
	VersionNumber       int
	Label               *string
	Note                *string
	ProcessingStatus    string
	SourceFilename      string
	SourceMIME          *string
	SourceSizeBytes     int64
	SourceFingerprint   string
	StorageObjectID     string
	AuthorizedRootID    string
	StorageObjectKey    string
	StorageObjectStatus string
	CreatedAt           time.Time
	IsCurrent           bool
	Probe               *Metadata
	Renditions          []Rendition
	UploadCheck         *UploadCheck
}

type UploadCheck struct {
	ID                   string
	WorkspaceID          string
	ProjectID            *string
	AssetID              string
	AssetVersionID       string
	StorageObjectID      string
	UploadedByUserID     *string
	ShareVisitorID       *string
	SourceType           string
	UploadSecurityPolicy string
	Status               string
	ResultCode           *string
	Message              *string
	CreatedAt            time.Time
	UpdatedAt            time.Time
	CompletedAt          *time.Time
	QuarantinedAt        *time.Time
	RejectedAt           *time.Time
}

type UploadCheckListItem struct {
	Check           UploadCheck
	AssetName       string
	SourceFilename  string
	SourceSizeBytes int64
	ProjectName     *string
	UploadedByName  *string
}

type UploadCheckQuery struct {
	Status    string
	ProjectID string
	Page      int
	PageSize  int
}

type UploadCheckPage struct {
	Items    []UploadCheckListItem
	Total    int
	Page     int
	PageSize int
}

type ResolveUploadCheckInput struct {
	WorkspaceID string
	ID          string
	ResultCode  string
	Message     *string
}

type RejectUploadCheckInput struct {
	WorkspaceID string
	ID          string
	ResultCode  string
	Message     *string
}

type resolveUploadCheckRecord struct {
	WorkspaceID string
	ID          string
	ResultCode  string
	Message     *string
	JobID       string
	Now         time.Time
}

type UploadAssetVersionInput struct {
	WorkspaceID string
	UserID      string
	AssetID     string
	Revision    int
	Filename    string
	MIMEType    string
	Target      *UploadTarget
	Label       *string
	Note        *string
}

type UploadAssetInput struct {
	WorkspaceID string
	UserID      string
	Filename    string
	MIMEType    string
	ProjectID   *string
	Target      *UploadTarget
	Label       *string
	Note        *string
}

type UploadTarget struct {
	TargetKind           string
	ProjectID            string
	StorageProviderID    string
	AuthorizedRootID     string
	UploadSecurityPolicy string
	LocalManagedBucketID string
	QuotaBytes           *int64
	UsedBytesEstimate    int64
	QuotaAvailableBytes  *int64
	DiskAvailableBytes   *int64
	MaxSingleFileBytes   int64
	MinimumFreeBytes     int64
	ReservationKey       string
	StagingActorID       string
}

type ProjectAssetInput struct {
	WorkspaceID string
	ProjectID   string
	AssetID     string
	UserID      string
}

type MoveProjectAssetInput struct {
	WorkspaceID     string
	SourceProjectID string
	TargetProjectID string
	AssetID         string
	UserID          string
}

type createAssetVersionRecord struct {
	VersionID             string
	VersionFileID         string
	StorageObjectID       string
	UploadCheckID         string
	UploadCheckStatus     string
	UploadCheckResultCode string
	UploadCheckMessage    *string
	ProviderID            string
	RootID                string
	PathSecretID          string
	WorkspaceID           string
	UserID                string
	AssetID               string
	ProjectID             *string
	Revision              int
	Filename              string
	MIMEType              string
	UploadSecurityPolicy  string
	Label                 *string
	Note                  *string
	ManagedRootPath       string
	ObjectKey             string
	Observed              storage.ObservedFile
	Now                   time.Time
}

type createUploadedAssetRecord struct {
	AssetID               string
	ProjectAssetID        string
	VersionID             string
	VersionFileID         string
	StorageObjectID       string
	UploadCheckID         string
	UploadCheckStatus     string
	UploadCheckResultCode string
	UploadCheckMessage    *string
	ProviderID            string
	RootID                string
	PathSecretID          string
	WorkspaceID           string
	UserID                string
	ProjectID             *string
	Filename              string
	MIMEType              string
	UploadSecurityPolicy  string
	Label                 *string
	Note                  *string
	ManagedRootPath       string
	ObjectKey             string
	Observed              storage.ObservedFile
	Now                   time.Time
}

type LibraryPage struct {
	Items    []LibraryItem
	Total    int
	Page     int
	PageSize int
}

type LibraryRepository interface {
	Query(
		ctx context.Context,
		workspaceID string,
		rootID string,
		query LibraryQuery,
	) (LibraryPage, error)
	QueryProject(
		ctx context.Context,
		workspaceID string,
		projectID string,
		query LibraryQuery,
	) (LibraryPage, error)
	QueryProjectCandidates(
		ctx context.Context,
		workspaceID string,
		projectID string,
		query LibraryQuery,
	) (LibraryPage, error)
	AssignProject(
		ctx context.Context,
		workspaceID string,
		storageObjectID string,
		projectID *string,
		now time.Time,
	) (LibraryAsset, error)
	AssetProjectID(
		ctx context.Context,
		workspaceID string,
		assetID string,
	) (*string, error)
	ListVersions(
		ctx context.Context,
		workspaceID string,
		assetID string,
	) ([]AssetVersion, error)
	SetCurrentVersion(
		ctx context.Context,
		workspaceID string,
		assetID string,
		versionID string,
		revision int,
		now time.Time,
	) (LibraryAsset, error)
	UpdateAssetName(
		ctx context.Context,
		workspaceID string,
		assetID string,
		name string,
		revision int,
		now time.Time,
	) (LibraryAsset, error)
	CreateVersion(
		ctx context.Context,
		record createAssetVersionRecord,
	) (AssetVersion, error)
	CreateUploadedAsset(
		ctx context.Context,
		record createUploadedAssetRecord,
	) (AssetVersion, error)
	AssetProjectIDs(
		ctx context.Context,
		workspaceID string,
		assetID string,
	) ([]string, error)
	AssetForStorageObject(
		ctx context.Context,
		workspaceID string,
		storageObjectID string,
	) (LibraryAsset, error)
	AssetIDForVersion(
		ctx context.Context,
		workspaceID string,
		versionID string,
	) (string, error)
	AddAssetToProject(
		ctx context.Context,
		record projectAssetRecord,
	) (ProjectAsset, error)
	MoveAssetToProject(
		ctx context.Context,
		record moveProjectAssetRecord,
	) (ProjectAsset, error)
	TrashAssetFromProject(
		ctx context.Context,
		record trashProjectAssetRecord,
	) (ProjectAsset, error)
	RestoreAssetToProject(
		ctx context.Context,
		record restoreProjectAssetRecord,
	) (ProjectAsset, error)
	ListProjectTrash(
		ctx context.Context,
		workspaceID string,
		projectID string,
		now time.Time,
	) ([]ProjectAsset, error)
	ListUploadChecks(
		ctx context.Context,
		workspaceID string,
		query UploadCheckQuery,
	) (UploadCheckPage, error)
	ResolveUploadCheckForProcessing(
		ctx context.Context,
		record resolveUploadCheckRecord,
	) (UploadCheck, error)
	UpdateUploadCheckStatus(
		ctx context.Context,
		workspaceID string,
		id string,
		status string,
		resultCode string,
		message *string,
		now time.Time,
	) (UploadCheck, error)
	SetVersionProcessingStatus(
		ctx context.Context,
		workspaceID string,
		versionID string,
		status string,
	) error
}

type projectAssetRecord struct {
	ID          string
	WorkspaceID string
	ProjectID   string
	AssetID     string
	UserID      string
	Now         time.Time
}

type moveProjectAssetRecord struct {
	TargetRelationID string
	WorkspaceID      string
	SourceProjectID  string
	TargetProjectID  string
	AssetID          string
	UserID           string
	Now              time.Time
}

type trashProjectAssetRecord struct {
	WorkspaceID string
	ProjectID   string
	AssetID     string
	UserID      string
	Now         time.Time
}

type restoreProjectAssetRecord struct {
	WorkspaceID string
	ProjectID   string
	AssetID     string
	UserID      string
	Now         time.Time
}

type LibraryService struct {
	repository          LibraryRepository
	sourceStore         *ManagedSourceStore
	storage             *storage.Service
	malwareScanner      MalwareScanner
	uploadReservations  *uploadReservationTracker
	stagingReservations *storage.StagingReservationTracker
}

func NewLibraryService(repository LibraryRepository) *LibraryService {
	return &LibraryService{
		repository:          repository,
		malwareScanner:      UnavailableMalwareScanner{},
		uploadReservations:  newUploadReservationTracker(),
		stagingReservations: storage.NewStagingReservationTracker(),
	}
}

func NewLibraryServiceWithSourceStore(
	repository LibraryRepository,
	sourceStore *ManagedSourceStore,
) *LibraryService {
	return &LibraryService{
		repository:          repository,
		sourceStore:         sourceStore,
		malwareScanner:      UnavailableMalwareScanner{},
		uploadReservations:  newUploadReservationTracker(),
		stagingReservations: storage.NewStagingReservationTracker(),
	}
}

func NewLibraryServiceWithSourceStoreAndStorage(
	repository LibraryRepository,
	sourceStore *ManagedSourceStore,
	storageService *storage.Service,
) *LibraryService {
	stagingReservations := storage.NewStagingReservationTracker()
	if storageService != nil {
		stagingReservations = storageService.StagingReservations()
	}
	return &LibraryService{
		repository:          repository,
		sourceStore:         sourceStore,
		storage:             storageService,
		malwareScanner:      UnavailableMalwareScanner{},
		uploadReservations:  newUploadReservationTracker(),
		stagingReservations: stagingReservations,
	}
}

func (service *LibraryService) SetMalwareScanner(scanner MalwareScanner) {
	if scanner == nil {
		service.malwareScanner = UnavailableMalwareScanner{}
		return
	}
	service.malwareScanner = scanner
}

func (service *LibraryService) importUploadSource(
	ctx context.Context,
	workspaceID string,
	assetID string,
	versionID string,
	filename string,
	mimeType string,
	target *UploadTarget,
	reader io.Reader,
) (ManagedSource, error) {
	limitedReader, releaseReservation := service.limitUploadReader(reader, target)
	releaseOnError := true
	defer func() {
		if releaseOnError && releaseReservation != nil {
			releaseReservation()
		}
	}()
	if target == nil {
		source, err := service.sourceStore.Import(
			ctx,
			workspaceID,
			assetID,
			versionID,
			filename,
			limitedReader,
		)
		if err != nil {
			return ManagedSource{}, err
		}
		source.Release = releaseReservation
		releaseOnError = false
		return source, nil
	}
	if service.storage == nil {
		return ManagedSource{}, ErrLibraryUploadUnavailable
	}
	target.ProjectID = strings.TrimSpace(target.ProjectID)
	target.StorageProviderID = strings.TrimSpace(target.StorageProviderID)
	target.AuthorizedRootID = strings.TrimSpace(target.AuthorizedRootID)
	target.UploadSecurityPolicy = strings.TrimSpace(target.UploadSecurityPolicy)
	if target.UploadSecurityPolicy == "" {
		target.UploadSecurityPolicy = "standard"
	}
	if target.ProjectID == "" ||
		target.StorageProviderID == "" ||
		target.AuthorizedRootID == "" {
		return ManagedSource{}, ErrLibraryUploadTargetInvalid
	}
	stagingKey, stagingAvailable, stagingErr := service.sourceStore.StagingDiskReservation()
	if stagingErr != nil {
		return ManagedSource{}, stagingErr
	}
	// Every target is written to this Core-controlled directory before it is
	// persisted. Reserve its physical volume instead of the logical provider.
	target.DiskAvailableBytes = &stagingAvailable
	target.ReservationKey = stagingKey
	adapter, err := service.storage.Adapter(ctx, workspaceID, target.AuthorizedRootID)
	if err != nil {
		return ManagedSource{}, err
	}
	stagingDirectory, err := service.sourceStore.stagingDirectory()
	if err != nil {
		return ManagedSource{}, err
	}
	source, err := ImportSourceToAdapter(
		ctx,
		adapter,
		stagingDirectory,
		target.ProjectID,
		assetID,
		versionID,
		filename,
		mimeType,
		limitedReader,
	)
	if err != nil {
		return ManagedSource{}, err
	}
	source.WorkspaceID = workspaceID
	source.Release = releaseReservation
	releaseOnError = false
	return source, nil
}

func (service *LibraryService) Query(
	ctx context.Context,
	workspaceID string,
	rootID string,
	query LibraryQuery,
) (LibraryPage, error) {
	query = normalizeLibraryQuery(query)
	return service.repository.Query(ctx, workspaceID, rootID, query)
}

func (service *LibraryService) QueryProject(
	ctx context.Context,
	workspaceID string,
	projectID string,
	query LibraryQuery,
) (LibraryPage, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	projectID = strings.TrimSpace(projectID)
	if workspaceID == "" || projectID == "" {
		return LibraryPage{}, ErrLibraryProjectInvalid
	}
	query = normalizeLibraryQuery(query)
	return service.repository.QueryProject(ctx, workspaceID, projectID, query)
}

func (service *LibraryService) QueryProjectCandidates(
	ctx context.Context,
	workspaceID string,
	projectID string,
	query LibraryQuery,
) (LibraryPage, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	projectID = strings.TrimSpace(projectID)
	if workspaceID == "" || projectID == "" {
		return LibraryPage{}, ErrLibraryProjectInvalid
	}
	query = normalizeLibraryQuery(query)
	return service.repository.QueryProjectCandidates(
		ctx,
		workspaceID,
		projectID,
		query,
	)
}

func normalizeLibraryQuery(query LibraryQuery) LibraryQuery {
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize < 1 {
		query.PageSize = 48
	}
	if query.PageSize > 100 {
		query.PageSize = 100
	}
	return query
}

func (service *LibraryService) AssignProject(
	ctx context.Context,
	workspaceID string,
	storageObjectID string,
	projectID *string,
) (LibraryAsset, error) {
	return service.repository.AssignProject(
		ctx,
		workspaceID,
		storageObjectID,
		projectID,
		time.Now().UTC(),
	)
}

func (service *LibraryService) AssetProjectID(
	ctx context.Context,
	workspaceID string,
	assetID string,
) (*string, error) {
	return service.repository.AssetProjectID(ctx, workspaceID, assetID)
}

func (service *LibraryService) AssetProjectIDs(
	ctx context.Context,
	workspaceID string,
	assetID string,
) ([]string, error) {
	return service.repository.AssetProjectIDs(ctx, workspaceID, assetID)
}

func (service *LibraryService) AssetForStorageObject(
	ctx context.Context,
	workspaceID string,
	storageObjectID string,
) (LibraryAsset, error) {
	return service.repository.AssetForStorageObject(
		ctx,
		strings.TrimSpace(workspaceID),
		strings.TrimSpace(storageObjectID),
	)
}

func (service *LibraryService) AssetIDForVersion(
	ctx context.Context,
	workspaceID string,
	versionID string,
) (string, error) {
	return service.repository.AssetIDForVersion(
		ctx,
		strings.TrimSpace(workspaceID),
		strings.TrimSpace(versionID),
	)
}

func (service *LibraryService) AddAssetToProject(
	ctx context.Context,
	input ProjectAssetInput,
) (ProjectAsset, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.AssetID = strings.TrimSpace(input.AssetID)
	input.UserID = strings.TrimSpace(input.UserID)
	if input.WorkspaceID == "" || input.ProjectID == "" ||
		input.AssetID == "" || input.UserID == "" {
		return ProjectAsset{}, ErrProjectAssetInvalid
	}
	id, err := uuid.NewV7()
	if err != nil {
		return ProjectAsset{}, fmt.Errorf("create project asset identifier: %w", err)
	}
	return service.repository.AddAssetToProject(ctx, projectAssetRecord{
		ID:          id.String(),
		WorkspaceID: input.WorkspaceID,
		ProjectID:   input.ProjectID,
		AssetID:     input.AssetID,
		UserID:      input.UserID,
		Now:         time.Now().UTC(),
	})
}

func (service *LibraryService) MoveAssetToProject(
	ctx context.Context,
	input MoveProjectAssetInput,
) (ProjectAsset, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.SourceProjectID = strings.TrimSpace(input.SourceProjectID)
	input.TargetProjectID = strings.TrimSpace(input.TargetProjectID)
	input.AssetID = strings.TrimSpace(input.AssetID)
	input.UserID = strings.TrimSpace(input.UserID)
	if input.WorkspaceID == "" || input.SourceProjectID == "" ||
		input.TargetProjectID == "" || input.AssetID == "" ||
		input.UserID == "" || input.SourceProjectID == input.TargetProjectID {
		return ProjectAsset{}, ErrProjectAssetInvalid
	}
	id, err := uuid.NewV7()
	if err != nil {
		return ProjectAsset{}, fmt.Errorf("create project asset identifier: %w", err)
	}
	return service.repository.MoveAssetToProject(ctx, moveProjectAssetRecord{
		TargetRelationID: id.String(),
		WorkspaceID:      input.WorkspaceID,
		SourceProjectID:  input.SourceProjectID,
		TargetProjectID:  input.TargetProjectID,
		AssetID:          input.AssetID,
		UserID:           input.UserID,
		Now:              time.Now().UTC(),
	})
}

func (service *LibraryService) TrashAssetFromProject(
	ctx context.Context,
	input ProjectAssetInput,
) (ProjectAsset, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.AssetID = strings.TrimSpace(input.AssetID)
	input.UserID = strings.TrimSpace(input.UserID)
	if input.WorkspaceID == "" || input.ProjectID == "" ||
		input.AssetID == "" || input.UserID == "" {
		return ProjectAsset{}, ErrProjectAssetInvalid
	}
	return service.repository.TrashAssetFromProject(ctx, trashProjectAssetRecord{
		WorkspaceID: input.WorkspaceID,
		ProjectID:   input.ProjectID,
		AssetID:     input.AssetID,
		UserID:      input.UserID,
		Now:         time.Now().UTC(),
	})
}

func (service *LibraryService) RestoreAssetToProject(
	ctx context.Context,
	input ProjectAssetInput,
) (ProjectAsset, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.AssetID = strings.TrimSpace(input.AssetID)
	input.UserID = strings.TrimSpace(input.UserID)
	if input.WorkspaceID == "" || input.ProjectID == "" ||
		input.AssetID == "" || input.UserID == "" {
		return ProjectAsset{}, ErrProjectAssetInvalid
	}
	return service.repository.RestoreAssetToProject(ctx, restoreProjectAssetRecord{
		WorkspaceID: input.WorkspaceID,
		ProjectID:   input.ProjectID,
		AssetID:     input.AssetID,
		UserID:      input.UserID,
		Now:         time.Now().UTC(),
	})
}

func (service *LibraryService) ListProjectTrash(
	ctx context.Context,
	workspaceID string,
	projectID string,
) ([]ProjectAsset, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	projectID = strings.TrimSpace(projectID)
	if workspaceID == "" || projectID == "" {
		return nil, ErrProjectAssetInvalid
	}
	return service.repository.ListProjectTrash(
		ctx,
		workspaceID,
		projectID,
		time.Now().UTC(),
	)
}

func (service *LibraryService) ListVersions(
	ctx context.Context,
	workspaceID string,
	assetID string,
) ([]AssetVersion, error) {
	return service.repository.ListVersions(ctx, workspaceID, assetID)
}

func (service *LibraryService) SetCurrentVersion(
	ctx context.Context,
	workspaceID string,
	assetID string,
	versionID string,
	revision int,
) (LibraryAsset, error) {
	return service.repository.SetCurrentVersion(
		ctx,
		workspaceID,
		assetID,
		versionID,
		revision,
		time.Now().UTC(),
	)
}

func (service *LibraryService) UpdateAssetName(
	ctx context.Context,
	workspaceID string,
	assetID string,
	name string,
	revision int,
) (LibraryAsset, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	assetID = strings.TrimSpace(assetID)
	name = strings.TrimSpace(name)
	if workspaceID == "" || assetID == "" || revision < 1 || name == "" {
		return LibraryAsset{}, ErrLibraryAssetNameInvalid
	}
	if len([]rune(name)) > 160 {
		return LibraryAsset{}, ErrLibraryAssetNameInvalid
	}
	return service.repository.UpdateAssetName(
		ctx,
		workspaceID,
		assetID,
		name,
		revision,
		time.Now().UTC(),
	)
}

func (service *LibraryService) UploadVersion(
	ctx context.Context,
	input UploadAssetVersionInput,
	reader io.Reader,
) (AssetVersion, error) {
	if service.sourceStore == nil {
		return AssetVersion{}, ErrLibraryUploadUnavailable
	}
	input.Filename = strings.TrimSpace(input.Filename)
	input.MIMEType = strings.TrimSpace(input.MIMEType)
	if input.WorkspaceID == "" || input.UserID == "" || input.AssetID == "" ||
		input.Revision < 1 || input.Filename == "" {
		return AssetVersion{}, ErrLibraryUploadInvalid
	}
	input.Label = trimmedOptional(input.Label)
	input.Note = trimmedOptional(input.Note)
	if input.Target != nil {
		input.Target.StagingActorID = input.UserID
	}

	ids := make([]string, 7)
	for index := range ids {
		id, err := uuid.NewV7()
		if err != nil {
			return AssetVersion{}, fmt.Errorf("create asset version identifier: %w", err)
		}
		ids[index] = id.String()
	}
	source, err := service.importUploadSource(
		ctx,
		input.WorkspaceID,
		input.AssetID,
		ids[0],
		input.Filename,
		input.MIMEType,
		input.Target,
		reader,
	)
	if err != nil {
		return AssetVersion{}, err
	}
	defer source.Release()
	if source.Observed.SizeBytes < 1 {
		source.Cleanup()
		return AssetVersion{}, ErrLibraryUploadInvalid
	}
	if source.Observed.MIMEType == "application/octet-stream" &&
		input.MIMEType != "" {
		source.Observed.MIMEType = input.MIMEType
	}
	validatedMIME, err := service.validateUploadSourceType(
		ctx,
		uploadTypeInspection{
			Filename:     input.Filename,
			DeclaredMIME: input.MIMEType,
			ObservedMIME: source.Observed.MIMEType,
			Source:       source,
			Target:       input.Target,
		},
	)
	if err != nil {
		source.Cleanup()
		return AssetVersion{}, err
	}
	source.Observed.MIMEType = validatedMIME
	uploadCheck := service.evaluateMalwareScan(ctx, source, input.Target)
	providerID := ids[3]
	rootID := ids[4]
	projectID := (*string)(nil)
	uploadSecurityPolicy := "standard"
	if input.Target != nil {
		providerID = input.Target.StorageProviderID
		rootID = input.Target.AuthorizedRootID
		projectIDValue := input.Target.ProjectID
		projectID = &projectIDValue
		uploadSecurityPolicy = normalizeUploadSecurityPolicy(
			input.Target.UploadSecurityPolicy,
		)
	}
	item, err := service.repository.CreateVersion(ctx, createAssetVersionRecord{
		VersionID:             ids[0],
		VersionFileID:         ids[1],
		StorageObjectID:       ids[2],
		UploadCheckID:         ids[6],
		UploadCheckStatus:     uploadCheck.Status,
		UploadCheckResultCode: uploadCheck.ResultCode,
		UploadCheckMessage:    uploadCheck.Message,
		ProviderID:            providerID,
		RootID:                rootID,
		PathSecretID:          ids[5],
		WorkspaceID:           input.WorkspaceID,
		UserID:                input.UserID,
		AssetID:               input.AssetID,
		ProjectID:             projectID,
		Revision:              input.Revision,
		Filename:              input.Filename,
		MIMEType:              source.Observed.MIMEType,
		UploadSecurityPolicy:  uploadSecurityPolicy,
		Label:                 input.Label,
		Note:                  input.Note,
		ManagedRootPath:       source.RootPath,
		ObjectKey:             source.ObjectKey,
		Observed:              source.Observed,
		Now:                   time.Now().UTC(),
	})
	if err != nil {
		source.Cleanup()
		return AssetVersion{}, err
	}
	return item, nil
}

func (service *LibraryService) UploadAsset(
	ctx context.Context,
	input UploadAssetInput,
	reader io.Reader,
) (AssetVersion, error) {
	if service.sourceStore == nil {
		return AssetVersion{}, ErrLibraryUploadUnavailable
	}
	input.Filename = strings.TrimSpace(input.Filename)
	input.MIMEType = strings.TrimSpace(input.MIMEType)
	input.Label = trimmedOptional(input.Label)
	input.Note = trimmedOptional(input.Note)
	input.ProjectID = trimmedOptional(input.ProjectID)
	if input.WorkspaceID == "" || input.UserID == "" || input.Filename == "" {
		return AssetVersion{}, ErrLibraryUploadInvalid
	}
	if input.Target != nil {
		input.Target.StagingActorID = input.UserID
	}

	ids := make([]string, 9)
	for index := range ids {
		id, err := uuid.NewV7()
		if err != nil {
			return AssetVersion{}, fmt.Errorf("create uploaded asset identifier: %w", err)
		}
		ids[index] = id.String()
	}
	source, err := service.importUploadSource(
		ctx,
		input.WorkspaceID,
		ids[0],
		ids[1],
		input.Filename,
		input.MIMEType,
		input.Target,
		reader,
	)
	if err != nil {
		return AssetVersion{}, err
	}
	defer source.Release()
	if source.Observed.SizeBytes < 1 {
		source.Cleanup()
		return AssetVersion{}, ErrLibraryUploadInvalid
	}
	if source.Observed.MIMEType == "application/octet-stream" &&
		input.MIMEType != "" {
		source.Observed.MIMEType = input.MIMEType
	}
	validatedMIME, err := service.validateUploadSourceType(
		ctx,
		uploadTypeInspection{
			Filename:     input.Filename,
			DeclaredMIME: input.MIMEType,
			ObservedMIME: source.Observed.MIMEType,
			Source:       source,
			Target:       input.Target,
		},
	)
	if err != nil {
		source.Cleanup()
		return AssetVersion{}, err
	}
	source.Observed.MIMEType = validatedMIME
	uploadCheck := service.evaluateMalwareScan(ctx, source, input.Target)
	providerID := ids[5]
	rootID := ids[6]
	uploadSecurityPolicy := "standard"
	if input.Target != nil {
		providerID = input.Target.StorageProviderID
		rootID = input.Target.AuthorizedRootID
		uploadSecurityPolicy = normalizeUploadSecurityPolicy(
			input.Target.UploadSecurityPolicy,
		)
	}
	item, err := service.repository.CreateUploadedAsset(ctx, createUploadedAssetRecord{
		AssetID:               ids[0],
		ProjectAssetID:        ids[1],
		VersionID:             ids[2],
		VersionFileID:         ids[3],
		StorageObjectID:       ids[4],
		UploadCheckID:         ids[8],
		UploadCheckStatus:     uploadCheck.Status,
		UploadCheckResultCode: uploadCheck.ResultCode,
		UploadCheckMessage:    uploadCheck.Message,
		ProviderID:            providerID,
		RootID:                rootID,
		PathSecretID:          ids[7],
		WorkspaceID:           input.WorkspaceID,
		UserID:                input.UserID,
		ProjectID:             input.ProjectID,
		Filename:              input.Filename,
		MIMEType:              source.Observed.MIMEType,
		UploadSecurityPolicy:  uploadSecurityPolicy,
		Label:                 input.Label,
		Note:                  input.Note,
		ManagedRootPath:       source.RootPath,
		ObjectKey:             source.ObjectKey,
		Observed:              source.Observed,
		Now:                   time.Now().UTC(),
	})
	if err != nil {
		source.Cleanup()
		return AssetVersion{}, err
	}
	return item, nil
}

func normalizeUploadSecurityPolicy(value string) string {
	switch strings.TrimSpace(value) {
	case "quick", "standard", "enhanced":
		return strings.TrimSpace(value)
	default:
		return "standard"
	}
}

func (service *LibraryService) SetVersionProcessingStatus(
	ctx context.Context,
	workspaceID string,
	versionID string,
	status string,
) error {
	switch status {
	case "pending", "processing", "ready", "failed", "partial":
	default:
		return ErrLibraryUploadInvalid
	}
	return service.repository.SetVersionProcessingStatus(
		ctx,
		workspaceID,
		versionID,
		status,
	)
}

func (service *LibraryService) ListUploadChecks(
	ctx context.Context,
	workspaceID string,
	query UploadCheckQuery,
) (UploadCheckPage, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	query.Status = strings.TrimSpace(query.Status)
	query.ProjectID = strings.TrimSpace(query.ProjectID)
	if workspaceID == "" {
		return UploadCheckPage{}, ErrUploadCheckInvalid
	}
	if query.Page <= 0 {
		query.Page = 1
	}
	if query.PageSize <= 0 || query.PageSize > 100 {
		query.PageSize = 50
	}
	switch query.Status {
	case "", "uploaded", "checking", "processing", "ready", "quarantined", "rejected":
	default:
		return UploadCheckPage{}, ErrUploadCheckInvalid
	}
	return service.repository.ListUploadChecks(ctx, workspaceID, query)
}

func (service *LibraryService) ResolveUploadCheck(
	ctx context.Context,
	input ResolveUploadCheckInput,
) (UploadCheck, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ID = strings.TrimSpace(input.ID)
	input.ResultCode = strings.TrimSpace(input.ResultCode)
	input.Message = trimmedOptional(input.Message)
	if input.ResultCode == "" {
		input.ResultCode = "manual_released"
	}
	if input.WorkspaceID == "" || input.ID == "" {
		return UploadCheck{}, ErrUploadCheckInvalid
	}
	jobID, err := uuid.NewV7()
	if err != nil {
		return UploadCheck{}, fmt.Errorf("create upload check release job identifier: %w", err)
	}
	return service.repository.ResolveUploadCheckForProcessing(ctx, resolveUploadCheckRecord{
		WorkspaceID: input.WorkspaceID,
		ID:          input.ID,
		ResultCode:  input.ResultCode,
		Message:     input.Message,
		JobID:       jobID.String(),
		Now:         time.Now().UTC(),
	})
}

func (service *LibraryService) RejectUploadCheck(
	ctx context.Context,
	input RejectUploadCheckInput,
) (UploadCheck, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ID = strings.TrimSpace(input.ID)
	input.ResultCode = strings.TrimSpace(input.ResultCode)
	input.Message = trimmedOptional(input.Message)
	if input.ResultCode == "" {
		input.ResultCode = "manual_rejected"
	}
	if input.WorkspaceID == "" || input.ID == "" {
		return UploadCheck{}, ErrUploadCheckInvalid
	}
	return service.repository.UpdateUploadCheckStatus(
		ctx,
		input.WorkspaceID,
		input.ID,
		"rejected",
		input.ResultCode,
		input.Message,
		time.Now().UTC(),
	)
}

func trimmedOptional(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
