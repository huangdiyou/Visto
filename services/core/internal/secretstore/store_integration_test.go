package secretstore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/platform/database"
)

func TestEncryptedSQLiteStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	setup, err := identity.NewService(identity.NewSQLiteRepository(db)).Setup(
		ctx,
		identity.SetupInput{
			WorkspaceName: "Studio",
			OwnerName:     "Owner",
			Password:      "local-password-123",
			Locale:        "zh-CN",
			Timezone:      "Asia/Shanghai",
		},
	)
	if err != nil {
		t.Fatalf("setup identity: %v", err)
	}
	key := bytes.Repeat([]byte{0x42}, keyBytes)
	store, err := NewEncryptedSQLiteStore(db, key)
	if err != nil {
		t.Fatalf("create secret store: %v", err)
	}
	plaintext := []byte(`{"accessKey":"not-in-plain-text"}`)
	reference, err := store.Put(
		ctx,
		setup.Session.Workspace.ID,
		"storage.s3.credentials",
		plaintext,
	)
	if err != nil {
		t.Fatalf("put secret: %v", err)
	}
	var ciphertext []byte
	if err := db.QueryRowContext(
		ctx,
		"SELECT ciphertext FROM secret_values WHERE id = ?",
		reference.ID,
	).Scan(&ciphertext); err != nil {
		t.Fatalf("read ciphertext: %v", err)
	}
	if bytes.Contains(ciphertext, []byte("not-in-plain-text")) {
		t.Fatal("database ciphertext contains plaintext secret")
	}
	value, err := store.Get(ctx, setup.Session.Workspace.ID, reference.ID)
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if !bytes.Equal(value, plaintext) {
		t.Fatalf("secret = %q, want %q", value, plaintext)
	}
	if err := store.Delete(ctx, setup.Session.Workspace.ID, reference.ID); err != nil {
		t.Fatalf("delete secret: %v", err)
	}
	if _, err := store.Get(ctx, setup.Session.Workspace.ID, reference.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted secret to be missing, got %v", err)
	}
}

func TestLoadOrCreateKeyPersistsStableKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	first, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	second, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatalf("load key: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("persisted key changed")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("key file is empty")
	}
}
