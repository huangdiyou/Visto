package storage

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"review-studio.local/core/internal/secretstore"
)

func TestRemoteProviderLifecycleEncryptsCredentialsAndScans(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	server := newWebDAVTestServer(t)
	defer server.Close()
	db, session := storageTestDatabase(
		t,
		ctx,
		filepath.Join(t.TempDir(), "review-studio.db"),
	)
	secretStore, err := secretstore.NewEncryptedSQLiteStore(
		db,
		bytes.Repeat([]byte{0x42}, 32),
	)
	if err != nil {
		t.Fatalf("create encrypted secret store: %v", err)
	}
	service := NewServiceWithDependencies(
		NewSQLiteRepository(db),
		NewDefaultProviderRegistry(),
		secretStore,
	)

	provider, err := service.CreateProvider(ctx, CreateProviderInput{
		WorkspaceID:         session.Workspace.ID,
		Kind:                "webdav",
		Name:                "验收 WebDAV",
		Endpoint:            server.URL + "/dav",
		Username:            "review",
		Password:            "secret",
		AllowPrivateNetwork: true,
	})
	if err != nil {
		t.Fatalf("create remote provider: %v", err)
	}
	if provider.Status != "offline" ||
		provider.Config["endpoint"] == "" ||
		provider.Config["username"] != "" {
		t.Fatalf("unexpected provider response: %#v", provider)
	}
	var purpose string
	var ciphertext []byte
	if err := db.QueryRowContext(ctx, `
		SELECT purpose, ciphertext
		FROM secret_values
		WHERE workspace_id = ?
	`, session.Workspace.ID).Scan(&purpose, &ciphertext); err != nil {
		t.Fatalf("read encrypted credential: %v", err)
	}
	if purpose != "storage.webdav.credentials" ||
		bytes.Contains(ciphertext, []byte("secret")) ||
		bytes.Contains(ciphertext, []byte("review")) {
		t.Fatalf("credential was not safely encrypted: purpose=%q", purpose)
	}

	report, err := service.TestProvider(ctx, session.Workspace.ID, provider.ID)
	if err != nil {
		t.Fatalf("test remote provider: %v", err)
	}
	if report.Status != "succeeded" ||
		!report.Capabilities["write"] ||
		!report.Capabilities["rangeRead"] {
		t.Fatalf("unexpected connection report: %#v", report)
	}

	updated, err := service.UpdateProvider(ctx, UpdateProviderInput{
		WorkspaceID:         session.Workspace.ID,
		ID:                  provider.ID,
		Name:                "验收 WebDAV 更新",
		Endpoint:            server.URL + "/dav",
		AllowPrivateNetwork: true,
		Revision:            provider.Revision,
	})
	if err != nil {
		t.Fatalf("update remote provider without credentials: %v", err)
	}
	report, err = service.TestProvider(ctx, session.Workspace.ID, updated.ID)
	if err != nil {
		t.Fatalf("test updated remote provider with preserved credentials: %v", err)
	}
	if report.Status != "succeeded" {
		t.Fatalf("unexpected preserved credential report: %#v", report)
	}
	provider = updated

	root, err := service.RegisterProviderRoot(ctx, RegisterProviderRootInput{
		WorkspaceID: session.Workspace.ID,
		ProviderID:  provider.ID,
		DisplayName: "远程素材",
		BasePath:    "",
		Mode:        "managed",
		ScanEnabled: true,
	})
	if err != nil {
		t.Fatalf("register remote root: %v", err)
	}
	adapter, err := service.Adapter(ctx, session.Workspace.ID, root.ID)
	if err != nil {
		t.Fatalf("open remote root adapter: %v", err)
	}
	payload := []byte("remote scan fixture")
	if _, err := adapter.Put(
		ctx,
		"deliverables/final.txt",
		bytes.NewReader(payload),
		int64(len(payload)),
		"text/plain",
	); err != nil {
		t.Fatalf("write remote fixture: %v", err)
	}
	result, err := service.ScanRoot(ctx, session.Workspace.ID, root.ID)
	if err != nil {
		t.Fatalf("scan remote root: %v", err)
	}
	if result.Summary.New != 1 {
		t.Fatalf("unexpected remote scan summary: %#v", result.Summary)
	}
	objects, err := service.ListObjects(ctx, session.Workspace.ID, root.ID)
	if err != nil {
		t.Fatalf("list remote objects: %v", err)
	}
	if len(objects) != 1 ||
		objects[0].ObjectKey != "deliverables/final.txt" {
		t.Fatalf("unexpected remote objects: %#v", objects)
	}

	if err := service.DeleteProvider(ctx, ProviderStateInput{
		WorkspaceID: session.Workspace.ID,
		ID:          provider.ID,
		Revision:    provider.Revision,
	}); !errors.Is(err, ErrProviderInUse) {
		t.Fatalf("expected provider-in-use guard, got %v", err)
	}
	if err := service.DeleteRoot(
		ctx,
		session.Workspace.ID,
		root.ID,
		root.Revision,
	); err != nil {
		t.Fatalf("delete remote root: %v", err)
	}
	refreshed, err := service.Provider(ctx, session.Workspace.ID, provider.ID)
	if err != nil {
		t.Fatalf("reload remote provider: %v", err)
	}
	if err := service.DeleteProvider(ctx, ProviderStateInput{
		WorkspaceID: session.Workspace.ID,
		ID:          provider.ID,
		Revision:    refreshed.Revision,
	}); err != nil {
		t.Fatalf("delete remote provider: %v", err)
	}
	var secretCount int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM secret_values WHERE workspace_id = ?
	`, session.Workspace.ID).Scan(&secretCount); err != nil {
		t.Fatalf("count remaining credentials: %v", err)
	}
	if secretCount != 0 {
		t.Fatalf("expected credential cleanup, got %d records", secretCount)
	}
}

func TestUpdateProviderRejectsEndpointChangeWithRetainedCredentials(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	server := newWebDAVTestServer(t)
	defer server.Close()
	otherServer := newWebDAVTestServer(t)
	defer otherServer.Close()
	db, session := storageTestDatabase(
		t,
		ctx,
		filepath.Join(t.TempDir(), "review-studio.db"),
	)
	secretStore, err := secretstore.NewEncryptedSQLiteStore(
		db,
		bytes.Repeat([]byte{0x42}, 32),
	)
	if err != nil {
		t.Fatalf("create encrypted secret store: %v", err)
	}
	service := NewServiceWithDependencies(
		NewSQLiteRepository(db),
		NewDefaultProviderRegistry(),
		secretStore,
	)

	provider, err := service.CreateProvider(ctx, CreateProviderInput{
		WorkspaceID:         session.Workspace.ID,
		Kind:                "webdav",
		Name:                "重定向 WebDAV",
		Endpoint:            server.URL + "/dav",
		Username:            "review",
		Password:            "secret",
		AllowPrivateNetwork: true,
	})
	if err != nil {
		t.Fatalf("create remote provider: %v", err)
	}

	_, err = service.UpdateProvider(ctx, UpdateProviderInput{
		WorkspaceID:         session.Workspace.ID,
		ID:                  provider.ID,
		Name:                "重定向 WebDAV",
		Endpoint:            otherServer.URL + "/dav",
		AllowPrivateNetwork: true,
		Revision:            provider.Revision,
	})
	if !errors.Is(err, ErrProviderEndpointChanged) {
		t.Fatalf("expected endpoint-change rejection, got %v", err)
	}
}

func TestProviderNamesAreUniqueWithinKind(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	server := newWebDAVTestServer(t)
	defer server.Close()
	db, session := storageTestDatabase(
		t,
		ctx,
		filepath.Join(t.TempDir(), "review-studio.db"),
	)
	secretStore, err := secretstore.NewEncryptedSQLiteStore(
		db,
		bytes.Repeat([]byte{0x24}, 32),
	)
	if err != nil {
		t.Fatalf("create encrypted secret store: %v", err)
	}
	service := NewServiceWithDependencies(
		NewSQLiteRepository(db),
		NewDefaultProviderRegistry(),
		secretStore,
	)
	input := CreateProviderInput{
		WorkspaceID: session.Workspace.ID,
		Kind:        "webdav", Name: "Remote",
		Endpoint: server.URL + "/dav", AllowPrivateNetwork: true,
	}
	if _, err := service.CreateProvider(ctx, input); err != nil {
		t.Fatalf("create first provider: %v", err)
	}
	input.Name = strings.ToLower(input.Name)
	if _, err := service.CreateProvider(ctx, input); !errors.Is(
		err,
		ErrProviderNameConflict,
	) {
		t.Fatalf("expected provider name conflict, got %v", err)
	}
}
