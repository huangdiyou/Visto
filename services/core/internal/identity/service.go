package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	sessionDuration     = 30 * 24 * time.Hour
	sessionIdleDuration = 12 * time.Hour
)

type Clock func() time.Time

type Service struct {
	repository Repository
	clock      Clock
}

func NewService(repository Repository) *Service {
	return &Service{
		repository: repository,
		clock:      time.Now,
	}
}

func (service *Service) IsSetupComplete(ctx context.Context) (bool, error) {
	return service.repository.IsSetupComplete(ctx)
}

func (service *Service) Setup(
	ctx context.Context,
	input SetupInput,
) (SetupResult, error) {
	input.WorkspaceName = strings.TrimSpace(input.WorkspaceName)
	input.OwnerName = strings.TrimSpace(input.OwnerName)
	input.OwnerEmail = strings.TrimSpace(input.OwnerEmail)
	input.Locale = strings.TrimSpace(input.Locale)
	input.Timezone = strings.TrimSpace(input.Timezone)

	if err := validateSetup(input); err != nil {
		return SetupResult{}, err
	}
	var ownerEmail *string
	if input.OwnerEmail != "" {
		normalized, err := NormalizeEmail(input.OwnerEmail)
		if err != nil {
			return SetupResult{}, err
		}
		ownerEmail = &normalized
	}

	passwordHash, err := hashPassword(input.Password)
	if err != nil {
		return SetupResult{}, err
	}

	token, digest, err := newSessionToken()
	if err != nil {
		return SetupResult{}, err
	}

	userID, err := newID()
	if err != nil {
		return SetupResult{}, err
	}
	workspaceID, err := newID()
	if err != nil {
		return SetupResult{}, err
	}
	membershipID, err := newID()
	if err != nil {
		return SetupResult{}, err
	}
	sessionID, err := newID()
	if err != nil {
		return SetupResult{}, err
	}

	now := service.clock().UTC()
	expiresAt := now.Add(sessionDuration)
	if err := service.repository.Setup(ctx, setupRecord{
		UserID:         userID,
		WorkspaceID:    workspaceID,
		MembershipID:   membershipID,
		SessionID:      sessionID,
		WorkspaceName:  input.WorkspaceName,
		OwnerName:      input.OwnerName,
		OwnerEmail:     ownerEmail,
		PasswordHash:   passwordHash,
		SessionDigest:  digest,
		Locale:         input.Locale,
		Timezone:       input.Timezone,
		Now:            now,
		SessionExpires: expiresAt,
	}); err != nil {
		return SetupResult{}, err
	}

	return SetupResult{
		Session: Session{
			ID: sessionID,
			User: User{
				ID:          userID,
				Email:       ownerEmail,
				DisplayName: input.OwnerName,
				Locale:      input.Locale,
			},
			Workspace: Workspace{
				ID:       workspaceID,
				Name:     input.WorkspaceName,
				TeamName: input.WorkspaceName,
				Timezone: input.Timezone,
			},
			Role:      "owner",
			ExpiresAt: expiresAt,
		},
		Token: token,
	}, nil
}

func (service *Service) Login(
	ctx context.Context,
	password string,
) (LoginResult, error) {
	return service.LoginAccount(ctx, LoginInput{Password: password})
}

func (service *Service) LoginAccount(
	ctx context.Context,
	input LoginInput,
) (LoginResult, error) {
	input.Email = strings.TrimSpace(input.Email)
	var record credentialRecord
	var err error
	if input.Email == "" {
		record, err = service.repository.LegacyOwnerCredential(ctx)
	} else {
		email, normalizeErr := NormalizeEmail(input.Email)
		if normalizeErr != nil {
			return LoginResult{}, ErrInvalidCredentials
		}
		record, err = service.repository.CredentialByEmail(ctx, email)
	}
	if err != nil {
		return LoginResult{}, err
	}

	matches, err := verifyPassword(record.PasswordHash, input.Password)
	if err != nil {
		return LoginResult{}, fmt.Errorf("verify owner password: %w", err)
	}
	if !matches {
		return LoginResult{}, ErrInvalidCredentials
	}

	token, digest, err := newSessionToken()
	if err != nil {
		return LoginResult{}, err
	}
	sessionID, err := newID()
	if err != nil {
		return LoginResult{}, err
	}

	now := service.clock().UTC()
	expiresAt := now.Add(sessionDuration)
	if err := service.repository.CreateSession(
		ctx,
		sessionID,
		record.UserID,
		record.WorkspaceID,
		digest,
		now,
		expiresAt,
	); err != nil {
		return LoginResult{}, err
	}

	return LoginResult{
		Session: sessionFromCredential(sessionID, record, expiresAt),
		Token:   token,
	}, nil
}

func (service *Service) CreateAccount(
	ctx context.Context,
	input CreateAccountInput,
) (User, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Locale = strings.TrimSpace(input.Locale)
	input.Role = strings.ToLower(strings.TrimSpace(input.Role))
	email, err := NormalizeEmail(input.Email)
	if err != nil {
		return User{}, err
	}
	if input.WorkspaceID == "" ||
		len([]rune(input.DisplayName)) < 1 ||
		len([]rune(input.DisplayName)) > 80 ||
		len([]rune(input.Password)) < 10 ||
		len([]rune(input.Password)) > 256 ||
		input.Locale == "" ||
		!validRole(input.Role) {
		return User{}, errors.New("account input is invalid")
	}
	passwordHash, err := hashPassword(input.Password)
	if err != nil {
		return User{}, err
	}
	userID, err := newID()
	if err != nil {
		return User{}, err
	}
	membershipID, err := newID()
	if err != nil {
		return User{}, err
	}
	if err := service.repository.CreateAccount(ctx, accountRecord{
		UserID:       userID,
		MembershipID: membershipID,
		WorkspaceID:  input.WorkspaceID,
		Email:        email,
		DisplayName:  input.DisplayName,
		PasswordHash: passwordHash,
		Locale:       input.Locale,
		Role:         input.Role,
		Now:          service.clock().UTC(),
	}); err != nil {
		return User{}, err
	}
	return User{
		ID:          userID,
		Email:       &email,
		DisplayName: input.DisplayName,
		Locale:      input.Locale,
	}, nil
}

func (service *Service) UpdateProfile(
	ctx context.Context,
	input UpdateProfileInput,
) (User, error) {
	input.UserID = strings.TrimSpace(input.UserID)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Locale = normalizeProfileLocale(input.Locale)
	if input.UserID == "" ||
		len([]rune(input.DisplayName)) < 1 ||
		len([]rune(input.DisplayName)) > 80 ||
		input.Locale == "" {
		return User{}, errors.New("profile input is invalid")
	}
	return service.repository.UpdateProfile(
		ctx,
		input.UserID,
		input.DisplayName,
		input.Locale,
		service.clock().UTC(),
	)
}

func (service *Service) Authenticate(
	ctx context.Context,
	token string,
) (Session, error) {
	if strings.TrimSpace(token) == "" {
		return Session{}, ErrSessionNotFound
	}

	now := service.clock().UTC()
	record, err := service.repository.SessionByDigest(
		ctx,
		digestToken(token),
		now,
		now.Add(-sessionIdleDuration),
	)
	if err != nil {
		return Session{}, err
	}

	// Activity is checked before the session is returned, then refreshed. A
	// transient touch failure does not invalidate an otherwise valid request.
	_ = service.repository.TouchSession(ctx, record.SessionID, now)

	return Session{
		ID: record.SessionID,
		User: User{
			ID:          record.UserID,
			Email:       record.Email,
			DisplayName: record.DisplayName,
			Locale:      record.Locale,
		},
		Workspace: Workspace{
			ID:       record.WorkspaceID,
			Name:     record.WorkspaceName,
			TeamName: record.WorkspaceTeamName,
			Timezone: record.Timezone,
		},
		Role:      record.Role,
		ExpiresAt: record.ExpiresAt,
	}, nil
}

func (service *Service) Logout(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}

	return service.repository.RevokeSession(
		ctx,
		digestToken(token),
		service.clock().UTC(),
	)
}

func validateSetup(input SetupInput) error {
	switch {
	case len([]rune(input.WorkspaceName)) < 1 || len([]rune(input.WorkspaceName)) > 80:
		return errors.New("workspace name must contain 1 to 80 characters")
	case len([]rune(input.OwnerName)) < 1 || len([]rune(input.OwnerName)) > 80:
		return errors.New("owner name must contain 1 to 80 characters")
	case len([]rune(input.Password)) < 10 || len([]rune(input.Password)) > 256:
		return errors.New("password must contain 10 to 256 characters")
	case input.Locale == "":
		return errors.New("locale is required")
	case input.Timezone == "":
		return errors.New("timezone is required")
	default:
		return nil
	}
}

func normalizeProfileLocale(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "en", "en-us", "en_us":
		return "en-US"
	case "zh", "zh-cn", "zh_cn", "zh-hans", "zh-hans-cn":
		return "zh-CN"
	default:
		return ""
	}
}

func NormalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || len(value) > 254 {
		return "", errors.New("email is invalid")
	}
	return value, nil
}

func (service *Service) PrepareProvisioning(
	password string,
) (ProvisioningMaterial, error) {
	if len([]rune(password)) < 10 || len([]rune(password)) > 256 {
		return ProvisioningMaterial{}, errors.New(
			"password must contain 10 to 256 characters",
		)
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		return ProvisioningMaterial{}, err
	}
	token, digest, err := newSessionToken()
	if err != nil {
		return ProvisioningMaterial{}, err
	}
	userID, err := newID()
	if err != nil {
		return ProvisioningMaterial{}, err
	}
	membershipID, err := newID()
	if err != nil {
		return ProvisioningMaterial{}, err
	}
	sessionID, err := newID()
	if err != nil {
		return ProvisioningMaterial{}, err
	}
	return ProvisioningMaterial{
		UserID:         userID,
		MembershipID:   membershipID,
		SessionID:      sessionID,
		PasswordHash:   passwordHash,
		SessionToken:   token,
		SessionDigest:  digest,
		SessionExpires: service.clock().UTC().Add(sessionDuration),
	}, nil
}

func validRole(value string) bool {
	switch value {
	case "owner", "admin", "member", "guest":
		return true
	default:
		return false
	}
}

func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate UUIDv7: %w", err)
	}

	return id.String(), nil
}

func newSessionToken() (string, string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", fmt.Errorf("generate session token: %w", err)
	}

	token := base64.RawURLEncoding.EncodeToString(bytes)
	return token, digestToken(token), nil
}

func digestToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func sessionFromCredential(
	sessionID string,
	record credentialRecord,
	expiresAt time.Time,
) Session {
	return Session{
		ID: sessionID,
		User: User{
			ID:          record.UserID,
			Email:       record.Email,
			DisplayName: record.DisplayName,
			Locale:      record.Locale,
		},
		Workspace: Workspace{
			ID:       record.WorkspaceID,
			Name:     record.WorkspaceName,
			TeamName: record.WorkspaceTeamName,
			Timezone: record.Timezone,
		},
		Role:      record.Role,
		ExpiresAt: expiresAt,
	}
}
