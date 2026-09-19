package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"review-studio.local/core/internal/job"
	"review-studio.local/core/internal/secretstore"
)

func TestCopyTaskExecutorCopiesAndVerifiesLocalObjects(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, session := storageTestDatabase(
		t,
		ctx,
		filepath.Join(t.TempDir(), "review-studio.db"),
	)
	service := NewService(NewSQLiteRepository(db))
	service.SetCopyTempDir(filepath.Join(t.TempDir(), "copy-staging"))

	sourceDir := t.TempDir()
	targetDir := t.TempDir()
	payload := []byte("copy me with verification")
	if err := os.WriteFile(
		filepath.Join(sourceDir, "source.txt"),
		payload,
		0o600,
	); err != nil {
		t.Fatalf("write source: %v", err)
	}
	sourceRoot, err := service.RegisterLocalRoot(ctx, RegisterLocalRootInput{
		WorkspaceID: session.Workspace.ID,
		DisplayName: "Source",
		LocalPath:   sourceDir,
		Mode:        "referenced",
		ScanEnabled: true,
	})
	if err != nil {
		t.Fatalf("register source root: %v", err)
	}
	targetRoot, err := service.RegisterLocalRoot(ctx, RegisterLocalRootInput{
		WorkspaceID: session.Workspace.ID,
		DisplayName: "Target",
		LocalPath:   targetDir,
		Mode:        "managed",
		ScanEnabled: true,
	})
	if err != nil {
		t.Fatalf("register target root: %v", err)
	}
	if _, err := service.ScanRoot(ctx, session.Workspace.ID, sourceRoot.ID); err != nil {
		t.Fatalf("scan source root: %v", err)
	}
	objects, err := service.ListObjects(ctx, session.Workspace.ID, sourceRoot.ID)
	if err != nil {
		t.Fatalf("list source objects: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("unexpected source objects: %#v", objects)
	}
	task, err := service.CreateCopyTask(ctx, CreateCopyTaskInput{
		WorkspaceID:           session.Workspace.ID,
		SourceStorageObjectID: objects[0].ID,
		TargetRootID:          targetRoot.ID,
		TargetObjectKey:       "copies/copied.txt",
	})
	if err != nil {
		t.Fatalf("create copy task: %v", err)
	}
	if err := os.MkdirAll(service.copyTempDir, 0o700); err != nil {
		t.Fatalf("create copy staging dir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(service.copyTempDir, task.TempFileName),
		[]byte("wrong"),
		0o600,
	); err != nil {
		t.Fatalf("seed corrupt staging file: %v", err)
	}
	payloadJSON, _ := json.Marshal(CopyTaskPayload{TaskID: task.ID})
	reporter := &copyTestReporter{}
	if err := (CopyTaskExecutor{Service: service}).Execute(
		ctx,
		job.Job{
			ID:          "job-copy",
			WorkspaceID: session.Workspace.ID,
			Type:        CopyObjectJobType,
			Payload:     payloadJSON,
		},
		reporter,
	); err != nil {
		t.Fatalf("execute copy task: %v", err)
	}

	copied, err := os.ReadFile(filepath.Join(targetDir, "copies", "copied.txt"))
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(copied) != string(payload) {
		t.Fatalf("copied payload mismatch: %q", copied)
	}
	finished, err := service.CopyTask(ctx, session.Workspace.ID, task.ID)
	if err != nil {
		t.Fatalf("reload copy task: %v", err)
	}
	if finished.Status != "succeeded" ||
		finished.TargetStorageObjectID == nil ||
		finished.ContentHash == nil ||
		finished.ContentHashAlgorithm == nil ||
		*finished.ContentHashAlgorithm != "sha256" {
		t.Fatalf("unexpected finished task: %#v", finished)
	}
	sum := sha256.Sum256(payload)
	if *finished.ContentHash != hex.EncodeToString(sum[:]) {
		t.Fatalf("unexpected content hash %q", *finished.ContentHash)
	}
	targetObjects, err := service.ListObjects(ctx, session.Workspace.ID, targetRoot.ID)
	if err != nil {
		t.Fatalf("list target objects: %v", err)
	}
	if len(targetObjects) != 1 ||
		targetObjects[0].ID != *finished.TargetStorageObjectID ||
		targetObjects[0].ObjectKey != "copies/copied.txt" {
		t.Fatalf("unexpected target objects: %#v", targetObjects)
	}
	if reporter.current != reporter.total || reporter.total != int64(len(payload)) {
		t.Fatalf("unexpected progress: %#v", reporter)
	}
}

func TestCopyTargetCapacityReservationPreventsQuotaOvercommit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, session := storageTestDatabase(
		t,
		ctx,
		filepath.Join(t.TempDir(), "review-studio.db"),
	)
	service := NewService(NewSQLiteRepository(db))
	quota := int64(100)
	bucket, err := service.CreateLocalManagedBucket(ctx, CreateLocalManagedBucketInput{
		WorkspaceID:          session.Workspace.ID,
		DisplayName:          "Copy target",
		LocalPath:            filepath.Join(t.TempDir(), "target"),
		Purpose:              "upload",
		QuotaBytes:           &quota,
		UploadSecurityPolicy: "standard",
		CreatedBy:            session.User.ID,
	})
	if err != nil {
		t.Fatalf("create target bucket: %v", err)
	}
	task := CopyTask{
		WorkspaceID:  session.Workspace.ID,
		TargetRootID: bucket.AuthorizedRootID,
	}

	release, err := service.reserveCopyTargetCapacity(ctx, task, 60)
	if err != nil {
		t.Fatalf("reserve first copy: %v", err)
	}
	if _, err := service.reserveCopyTargetCapacity(ctx, task, 50); err == nil {
		t.Fatal("second concurrent copy should exceed the target bucket quota")
	}

	release()
	if releaseFullQuota, err := service.reserveCopyTargetCapacity(ctx, task, 100); err != nil {
		t.Fatalf("released quota should be reusable: %v", err)
	} else {
		releaseFullQuota()
	}
}

func TestCopyTaskExecutorCopiesLocalObjectToRemoteAdapters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		createProvider func(*testing.T, context.Context, *Service, string) Provider
	}{
		{
			name: "webdav",
			createProvider: func(
				t *testing.T,
				ctx context.Context,
				service *Service,
				workspaceID string,
			) Provider {
				t.Helper()
				server := newWebDAVTestServer(t)
				t.Cleanup(server.Close)
				provider, err := service.CreateProvider(ctx, CreateProviderInput{
					WorkspaceID:         workspaceID,
					Kind:                "webdav",
					Name:                "WebDAV Target",
					Endpoint:            server.URL + "/dav",
					Username:            "review",
					Password:            "secret",
					AllowPrivateNetwork: true,
				})
				if err != nil {
					t.Fatalf("create webdav provider: %v", err)
				}
				if _, err := service.TestProvider(ctx, workspaceID, provider.ID); err != nil {
					t.Fatalf("test webdav provider: %v", err)
				}
				return provider
			},
		},
		{
			name: "s3",
			createProvider: func(
				t *testing.T,
				ctx context.Context,
				service *Service,
				workspaceID string,
			) Provider {
				t.Helper()
				server, _ := newS3TestServer(t)
				t.Cleanup(server.Close)
				provider, err := service.CreateProvider(ctx, CreateProviderInput{
					WorkspaceID:         workspaceID,
					Kind:                "s3",
					Name:                "S3 Target",
					Endpoint:            server.URL,
					Region:              "us-test-1",
					Bucket:              "review-bucket",
					PathStyle:           true,
					AccessKeyID:         "access",
					SecretAccessKey:     "secret",
					AllowPrivateNetwork: true,
				})
				if err != nil {
					t.Fatalf("create s3 provider: %v", err)
				}
				if _, err := service.TestProvider(ctx, workspaceID, provider.ID); err != nil {
					t.Fatalf("test s3 provider: %v", err)
				}
				return provider
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db, session := storageTestDatabase(
				t,
				ctx,
				filepath.Join(t.TempDir(), "review-studio.db"),
			)
			secretStore, err := secretstore.NewEncryptedSQLiteStore(
				db,
				bytes.Repeat([]byte{0x31}, 32),
			)
			if err != nil {
				t.Fatalf("create secret store: %v", err)
			}
			service := NewServiceWithDependencies(
				NewSQLiteRepository(db),
				NewDefaultProviderRegistry(),
				secretStore,
			)
			service.SetCopyTempDir(filepath.Join(t.TempDir(), "copy-staging"))
			sourceDir := t.TempDir()
			payload := []byte("remote target copy")
			if err := os.WriteFile(
				filepath.Join(sourceDir, "source.bin"),
				payload,
				0o600,
			); err != nil {
				t.Fatalf("write source: %v", err)
			}
			sourceRoot, err := service.RegisterLocalRoot(
				ctx,
				RegisterLocalRootInput{
					WorkspaceID: session.Workspace.ID,
					DisplayName: "Source",
					LocalPath:   sourceDir,
					Mode:        "referenced",
					ScanEnabled: true,
				},
			)
			if err != nil {
				t.Fatalf("register source root: %v", err)
			}
			if _, err := service.ScanRoot(
				ctx,
				session.Workspace.ID,
				sourceRoot.ID,
			); err != nil {
				t.Fatalf("scan source root: %v", err)
			}
			objects, err := service.ListObjects(
				ctx,
				session.Workspace.ID,
				sourceRoot.ID,
			)
			if err != nil || len(objects) != 1 {
				t.Fatalf("load source object: %#v %v", objects, err)
			}
			provider := test.createProvider(
				t,
				ctx,
				service,
				session.Workspace.ID,
			)
			targetRoot, err := service.RegisterProviderRoot(
				ctx,
				RegisterProviderRootInput{
					WorkspaceID: session.Workspace.ID,
					ProviderID:  provider.ID,
					DisplayName: "Remote Target",
					Mode:        "managed",
					ScanEnabled: true,
				},
			)
			if err != nil {
				t.Fatalf("register remote target root: %v", err)
			}
			task, err := service.CreateCopyTask(ctx, CreateCopyTaskInput{
				WorkspaceID:           session.Workspace.ID,
				SourceStorageObjectID: objects[0].ID,
				TargetRootID:          targetRoot.ID,
				TargetObjectKey:       "remote/copied.bin",
			})
			if err != nil {
				t.Fatalf("create copy task: %v", err)
			}
			payloadJSON, _ := json.Marshal(CopyTaskPayload{TaskID: task.ID})
			if err := (CopyTaskExecutor{Service: service}).Execute(
				ctx,
				job.Job{
					ID:          "job-copy-" + test.name,
					WorkspaceID: session.Workspace.ID,
					Type:        CopyObjectJobType,
					Payload:     payloadJSON,
				},
				&copyTestReporter{},
			); err != nil {
				t.Fatalf("execute remote copy task: %v", err)
			}
			targetAdapter, err := service.Adapter(
				ctx,
				session.Workspace.ID,
				targetRoot.ID,
			)
			if err != nil {
				t.Fatalf("open target adapter: %v", err)
			}
			reader, _, err := targetAdapter.OpenRange(
				ctx,
				"remote/copied.bin",
				ByteRange{Offset: 0, Length: int64(len(payload))},
			)
			if err != nil {
				t.Fatalf("read remote copy: %v", err)
			}
			copied, err := io.ReadAll(reader)
			reader.Close()
			if err != nil {
				t.Fatalf("read copied payload: %v", err)
			}
			if !bytes.Equal(copied, payload) {
				t.Fatalf("remote payload mismatch: %q", copied)
			}
		})
	}
}

type copyTestReporter struct {
	current int64
	total   int64
	unit    string
}

func (reporter *copyTestReporter) SetProgress(
	_ context.Context,
	current int64,
	total int64,
	unit string,
) error {
	reporter.current = current
	reporter.total = total
	reporter.unit = unit
	return nil
}
