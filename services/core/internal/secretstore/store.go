package secretstore

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidInput = errors.New("secret input is invalid")
	ErrNotFound     = errors.New("secret not found")
)

type Reference struct {
	ID          string
	WorkspaceID string
	Purpose     string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Store interface {
	Put(context.Context, string, string, []byte) (Reference, error)
	Get(context.Context, string, string) ([]byte, error)
	Delete(context.Context, string, string) error
}

type EncryptedSQLiteStore struct {
	db    *sql.DB
	aead  cipher.AEAD
	clock func() time.Time
}

func NewEncryptedSQLiteStore(
	db *sql.DB,
	key []byte,
) (*EncryptedSQLiteStore, error) {
	if db == nil {
		return nil, errors.New("secret database is required")
	}
	if len(key) != 32 {
		return nil, errors.New("secret encryption key must contain 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create secret cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create secret AEAD: %w", err)
	}
	return &EncryptedSQLiteStore{db: db, aead: aead, clock: time.Now}, nil
}

func (store *EncryptedSQLiteStore) Put(
	ctx context.Context,
	workspaceID string,
	purpose string,
	value []byte,
) (Reference, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	purpose = strings.TrimSpace(purpose)
	if workspaceID == "" || purpose == "" || len(purpose) > 120 ||
		len(value) == 0 || len(value) > 64*1024 {
		return Reference{}, ErrInvalidInput
	}
	id, err := newID()
	if err != nil {
		return Reference{}, err
	}
	nonce := make([]byte, store.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Reference{}, fmt.Errorf("generate secret nonce: %w", err)
	}
	ciphertext := store.aead.Seal(nil, nonce, value, associatedData(id, workspaceID, purpose))
	now := store.clock().UTC()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO secret_values (
			id, workspace_id, purpose, key_version, nonce, ciphertext,
			created_at, updated_at
		) VALUES (?, ?, ?, 1, ?, ?, ?, ?)
	`, id, workspaceID, purpose, nonce, ciphertext, formatTime(now), formatTime(now)); err != nil {
		return Reference{}, fmt.Errorf("store encrypted secret: %w", err)
	}
	return Reference{
		ID:          id,
		WorkspaceID: workspaceID,
		Purpose:     purpose,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func (store *EncryptedSQLiteStore) Get(
	ctx context.Context,
	workspaceID string,
	id string,
) ([]byte, error) {
	var purpose string
	var keyVersion int
	var nonce, ciphertext []byte
	err := store.db.QueryRowContext(ctx, `
		SELECT purpose, key_version, nonce, ciphertext
		FROM secret_values
		WHERE id = ? AND workspace_id = ?
	`, strings.TrimSpace(id), strings.TrimSpace(workspaceID)).Scan(
		&purpose,
		&keyVersion,
		&nonce,
		&ciphertext,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read encrypted secret: %w", err)
	}
	if keyVersion != 1 {
		return nil, errors.New("secret key version is unsupported")
	}
	value, err := store.aead.Open(
		nil,
		nonce,
		ciphertext,
		associatedData(id, workspaceID, purpose),
	)
	if err != nil {
		return nil, errors.New("secret could not be decrypted")
	}
	return value, nil
}

func (store *EncryptedSQLiteStore) Delete(
	ctx context.Context,
	workspaceID string,
	id string,
) error {
	result, err := store.db.ExecContext(ctx, `
		DELETE FROM secret_values WHERE id = ? AND workspace_id = ?
	`, strings.TrimSpace(id), strings.TrimSpace(workspaceID))
	if err != nil {
		return fmt.Errorf("delete encrypted secret: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted secret result: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func associatedData(id, workspaceID, purpose string) []byte {
	return []byte(id + "\x00" + workspaceID + "\x00" + purpose)
}

func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate secret reference: %w", err)
	}
	return id.String(), nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
