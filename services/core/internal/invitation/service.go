package invitation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"time"

	"github.com/google/uuid"

	"review-studio.local/core/internal/identity"
)

const defaultInvitationDuration = 7 * 24 * time.Hour

type Service struct {
	repository Repository
	identity   *identity.Service
	clock      func() time.Time
}

func NewService(
	repository Repository,
	identityService *identity.Service,
) *Service {
	return &Service{
		repository: repository,
		identity:   identityService,
		clock:      time.Now,
	}
}

func (service *Service) Create(
	ctx context.Context,
	input CreateInput,
) (Secret, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.InvitedBy = strings.TrimSpace(input.InvitedBy)
	input.Role = strings.ToLower(strings.TrimSpace(input.Role))
	email, err := identity.NormalizeEmail(input.Email)
	if err != nil || input.WorkspaceID == "" || input.InvitedBy == "" ||
		!validInvitationRole(input.Role) {
		return Secret{}, ErrInvalidInput
	}
	now := service.clock().UTC()
	if input.ExpiresAt.IsZero() {
		input.ExpiresAt = now.Add(defaultInvitationDuration)
	}
	if !input.ExpiresAt.After(now) ||
		input.ExpiresAt.After(now.Add(30*24*time.Hour)) {
		return Secret{}, ErrInvalidInput
	}
	id, err := newID()
	if err != nil {
		return Secret{}, err
	}
	token, digest, err := newToken()
	if err != nil {
		return Secret{}, err
	}
	item, err := service.repository.Create(ctx, createRecord{
		ID:          id,
		WorkspaceID: input.WorkspaceID,
		InvitedBy:   input.InvitedBy,
		Email:       email,
		Role:        input.Role,
		TokenDigest: digest,
		TokenPrefix: tokenPrefix(token),
		ExpiresAt:   input.ExpiresAt.UTC(),
		Now:         now,
	})
	if err != nil {
		return Secret{}, err
	}
	return Secret{Invitation: item, URL: "/join/#" + token}, nil
}

func (service *Service) List(
	ctx context.Context,
	workspaceID string,
) ([]Invitation, error) {
	return service.repository.List(
		ctx,
		strings.TrimSpace(workspaceID),
		service.clock().UTC(),
	)
}

func (service *Service) Resend(
	ctx context.Context,
	workspaceID string,
	id string,
) (Secret, error) {
	token, digest, err := newToken()
	if err != nil {
		return Secret{}, err
	}
	now := service.clock().UTC()
	item, err := service.repository.Rotate(ctx, rotateRecord{
		WorkspaceID: strings.TrimSpace(workspaceID),
		ID:          strings.TrimSpace(id),
		TokenDigest: digest,
		TokenPrefix: tokenPrefix(token),
		ExpiresAt:   now.Add(defaultInvitationDuration),
		Now:         now,
	})
	if err != nil {
		return Secret{}, err
	}
	return Secret{Invitation: item, URL: "/join/#" + token}, nil
}

func (service *Service) Revoke(
	ctx context.Context,
	workspaceID string,
	id string,
) (Invitation, error) {
	return service.repository.Revoke(
		ctx,
		strings.TrimSpace(workspaceID),
		strings.TrimSpace(id),
		service.clock().UTC(),
	)
}

func (service *Service) Preview(
	ctx context.Context,
	token string,
) (Invitation, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Invitation{}, ErrUnavailable
	}
	return service.repository.Preview(
		ctx,
		digestToken(token),
		service.clock().UTC(),
	)
}

func (service *Service) Accept(
	ctx context.Context,
	input AcceptInput,
) (AcceptResult, error) {
	input.Token = strings.TrimSpace(input.Token)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Locale = strings.TrimSpace(input.Locale)
	if input.Token == "" || len([]rune(input.DisplayName)) < 1 ||
		len([]rune(input.DisplayName)) > 80 || input.Locale == "" {
		return AcceptResult{}, ErrInvalidInput
	}
	material, err := service.identity.PrepareProvisioning(input.Password)
	if err != nil {
		return AcceptResult{}, ErrInvalidInput
	}
	record, err := service.repository.Accept(ctx, acceptRecord{
		TokenDigest: digestToken(input.Token),
		DisplayName: input.DisplayName,
		Locale:      input.Locale,
		Material:    material,
		Now:         service.clock().UTC(),
	})
	if err != nil {
		return AcceptResult{}, err
	}
	return AcceptResult{
		Invitation: record.Invitation,
		Session: identity.Session{
			ID:        record.SessionID,
			User:      record.User,
			Workspace: record.Workspace,
			Role:      record.Role,
			ExpiresAt: record.SessionExpiry,
		},
		Token: material.SessionToken,
	}, nil
}

func validInvitationRole(value string) bool {
	return value == "admin" || value == "member" || value == "guest"
}

func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func newToken() (string, string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(value)
	return token, digestToken(token), nil
}

func digestToken(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func tokenPrefix(value string) string {
	if len(value) <= 6 {
		return value
	}
	return value[:6]
}
