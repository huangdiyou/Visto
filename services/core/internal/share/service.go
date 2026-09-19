package share

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"review-studio.local/core/internal/ratelimit"
	"review-studio.local/core/internal/secretstore"
)

const (
	publicSessionLifetime           = 12 * time.Hour
	publicUnverifiedSessionLifetime = 15 * time.Minute
	visitorCodeLifetime             = 7 * 24 * time.Hour
)

type verificationAttempt struct {
	Count       int
	WindowStart time.Time
}

type Service struct {
	repository Repository
	secrets    secretstore.Store
	clock      func() time.Time
	attemptsMu sync.Mutex
	attempts   map[string]verificationAttempt
	rateLimits *ratelimit.Service
}

func NewService(repository Repository) *Service {
	return NewServiceWithSecrets(repository, nil)
}

func NewServiceWithSecrets(
	repository Repository,
	secrets secretstore.Store,
) *Service {
	return &Service{
		repository: repository,
		secrets:    secrets,
		clock:      time.Now,
		attempts:   make(map[string]verificationAttempt),
	}
}

func NewServiceWithSecretsAndRateLimits(
	repository Repository,
	secrets secretstore.Store,
	rateLimits *ratelimit.Service,
) *Service {
	service := NewServiceWithSecrets(repository, secrets)
	service.rateLimits = rateLimits
	return service
}

func (service *Service) List(
	ctx context.Context,
	workspaceID string,
) ([]Share, error) {
	return service.repository.List(ctx, workspaceID, service.clock().UTC())
}

func (service *Service) Get(
	ctx context.Context,
	workspaceID string,
	id string,
) (Share, error) {
	return service.repository.Get(ctx, workspaceID, id, service.clock().UTC())
}

func (service *Service) GetByLink(
	ctx context.Context,
	workspaceID string,
	linkID string,
) (Share, error) {
	return service.repository.GetByLink(
		ctx,
		workspaceID,
		linkID,
		service.clock().UTC(),
	)
}

func (service *Service) Create(
	ctx context.Context,
	input CreateInput,
) (CreateResult, error) {
	now := service.clock().UTC()
	input.Name = strings.TrimSpace(input.Name)
	input.ReviewSessionID = strings.TrimSpace(input.ReviewSessionID)
	if err := validateCreate(input, now); err != nil {
		return CreateResult{}, err
	}
	shareID, err := newID()
	if err != nil {
		return CreateResult{}, err
	}
	linkID, err := newID()
	if err != nil {
		return CreateResult{}, err
	}
	token, digest, prefix, err := newToken()
	if err != nil {
		return CreateResult{}, err
	}
	cleanupRefs := make([]string, 0, 2)
	tokenSecretRef, err := service.storeSecret(
		ctx,
		input.WorkspaceID,
		"share.link_token",
		token,
	)
	if err != nil {
		return CreateResult{}, err
	}
	if tokenSecretRef != "" {
		cleanupRefs = append(cleanupRefs, tokenSecretRef)
	}
	passwordHash := ""
	passwordSecretRef := ""
	input.Password = strings.TrimSpace(input.Password)
	if input.Password != "" {
		passwordHash, err = hashPassword(input.Password)
		if err != nil {
			service.deleteSecrets(ctx, input.WorkspaceID, cleanupRefs)
			return CreateResult{}, err
		}
		passwordSecretRef, err = service.storeSecret(
			ctx,
			input.WorkspaceID,
			"share.password",
			input.Password,
		)
		if err != nil {
			service.deleteSecrets(ctx, input.WorkspaceID, cleanupRefs)
			return CreateResult{}, err
		}
		if passwordSecretRef != "" {
			cleanupRefs = append(cleanupRefs, passwordSecretRef)
		}
	}
	var passwordSecretRefPtr *string
	if passwordSecretRef != "" {
		passwordSecretRefPtr = &passwordSecretRef
	}
	item, link, err := service.repository.Create(ctx, createRecord{
		Share: Share{
			ID:                shareID,
			WorkspaceID:       input.WorkspaceID,
			ReviewSessionID:   &input.ReviewSessionID,
			Name:              input.Name,
			Status:            "active",
			AllowComment:      input.AllowComment,
			AllowDownload:     input.AllowDownload,
			RequireNickname:   input.RequireNickname,
			ExpiresAt:         input.ExpiresAt,
			PasswordProtected: passwordHash != "",
			PasswordSecretRef: passwordSecretRefPtr,
			CreatedBy:         input.UserID,
			Revision:          1,
		},
		PasswordHash:   passwordHash,
		LinkID:         linkID,
		TokenDigest:    digest,
		TokenSecretRef: tokenSecretRef,
		TokenPrefix:    prefix,
		Now:            now,
	})
	if err != nil {
		service.deleteSecrets(ctx, input.WorkspaceID, cleanupRefs)
		return CreateResult{}, err
	}
	return CreateResult{
		Share: item,
		Link:  LinkSecret{Link: link, Token: token},
	}, nil
}

func (service *Service) Update(
	ctx context.Context,
	input UpdateInput,
) (Share, error) {
	now := service.clock().UTC()
	input.Name = strings.TrimSpace(input.Name)
	if err := validatePolicy(
		input.Name,
		input.ExpiresAt,
		input.Revision,
		now,
	); err != nil {
		return Share{}, err
	}
	record := updateRecord{UpdateInput: input, Now: now}
	cleanupRefs := make([]string, 0, 1)
	if input.Password != nil {
		record.SetPasswordHash = true
		record.SetPasswordSecretRef = true
		value := strings.TrimSpace(*input.Password)
		if value != "" {
			if err := validatePassword(value); err != nil {
				return Share{}, err
			}
			hashed, err := hashPassword(value)
			if err != nil {
				return Share{}, err
			}
			record.PasswordHash = &hashed
			secretRef, err := service.storeSecret(
				ctx,
				input.WorkspaceID,
				"share.password",
				value,
			)
			if err != nil {
				return Share{}, err
			}
			if secretRef != "" {
				cleanupRefs = append(cleanupRefs, secretRef)
				record.PasswordSecretRef = &secretRef
			}
		}
	}
	item, err := service.repository.Update(ctx, record)
	if err != nil {
		service.deleteSecrets(ctx, input.WorkspaceID, cleanupRefs)
		return Share{}, err
	}
	return item, nil
}

func (service *Service) Revoke(
	ctx context.Context,
	input StateInput,
) (Share, error) {
	if input.Revision < 1 {
		return Share{}, errors.New("revision must be positive")
	}
	return service.repository.Revoke(ctx, input, service.clock().UTC())
}

func (service *Service) CreateLink(
	ctx context.Context,
	workspaceID string,
	shareID string,
) (LinkSecret, error) {
	linkID, err := newID()
	if err != nil {
		return LinkSecret{}, err
	}
	token, digest, prefix, err := newToken()
	if err != nil {
		return LinkSecret{}, err
	}
	tokenSecretRef, err := service.storeSecret(
		ctx,
		workspaceID,
		"share.link_token",
		token,
	)
	if err != nil {
		return LinkSecret{}, err
	}
	link, err := service.repository.CreateLink(
		ctx,
		workspaceID,
		shareID,
		linkID,
		digest,
		tokenSecretRef,
		prefix,
		service.clock().UTC(),
	)
	if err != nil {
		if tokenSecretRef != "" {
			service.deleteSecrets(ctx, workspaceID, []string{tokenSecretRef})
		}
		return LinkSecret{}, err
	}
	return LinkSecret{Link: link, Token: token}, nil
}

func (service *Service) Credentials(
	ctx context.Context,
	workspaceID string,
	shareID string,
) (Credentials, error) {
	item, err := service.repository.Get(
		ctx,
		workspaceID,
		shareID,
		service.clock().UTC(),
	)
	if err != nil {
		return Credentials{}, err
	}
	result := Credentials{
		ShareID: item.ID,
		Links:   make([]LinkCredential, 0, len(item.Links)),
	}
	if service.secrets == nil {
		return result, nil
	}
	if item.PasswordSecretRef != nil && *item.PasswordSecretRef != "" {
		value, err := service.secrets.Get(ctx, item.WorkspaceID, *item.PasswordSecretRef)
		if err != nil {
			return Credentials{}, err
		}
		password := string(value)
		result.Password = &password
	}
	for _, link := range item.Links {
		if link.Status != "active" {
			continue
		}
		if link.TokenSecretRef == nil || *link.TokenSecretRef == "" {
			continue
		}
		value, err := service.secrets.Get(ctx, item.WorkspaceID, *link.TokenSecretRef)
		if err != nil {
			return Credentials{}, err
		}
		result.Links = append(result.Links, LinkCredential{
			LinkID: link.ID,
			Token:  string(value),
		})
	}
	return result, nil
}

func (service *Service) storeSecret(
	ctx context.Context,
	workspaceID string,
	purpose string,
	value string,
) (string, error) {
	if service.secrets == nil || value == "" {
		return "", nil
	}
	reference, err := service.secrets.Put(
		ctx,
		workspaceID,
		purpose,
		[]byte(value),
	)
	if err != nil {
		return "", err
	}
	return reference.ID, nil
}

func (service *Service) deleteSecrets(
	ctx context.Context,
	workspaceID string,
	refs []string,
) {
	if service.secrets == nil {
		return
	}
	for _, ref := range refs {
		if strings.TrimSpace(ref) == "" {
			continue
		}
		_ = service.secrets.Delete(ctx, workspaceID, ref)
	}
}

func (service *Service) CreateVisitorCode(
	ctx context.Context,
	input CreateVisitorCodeInput,
) (VisitorCodeSecret, error) {
	now := service.clock().UTC()
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.ShareID = strings.TrimSpace(input.ShareID)
	if input.ExpiresAt == nil {
		value := now.Add(visitorCodeLifetime)
		input.ExpiresAt = &value
	}
	if strings.TrimSpace(input.ShareID) == "" {
		return VisitorCodeSecret{}, errors.New("share is required")
	}
	if err := validateVisitorDisplayName(input.DisplayName); err != nil {
		return VisitorCodeSecret{}, err
	}
	if input.ExpiresAt != nil && !input.ExpiresAt.After(now) {
		return VisitorCodeSecret{}, errors.New("visitor code expiration must be in the future")
	}
	codeID, err := newID()
	if err != nil {
		return VisitorCodeSecret{}, err
	}
	code, digest, prefix, err := newVisitorCode()
	if err != nil {
		return VisitorCodeSecret{}, err
	}
	item, err := service.repository.CreateVisitorCode(ctx, visitorCodeRecord{
		ID:          codeID,
		WorkspaceID: input.WorkspaceID,
		ShareID:     input.ShareID,
		CodeDigest:  digest,
		CodePrefix:  prefix,
		DisplayName: input.DisplayName,
		ExpiresAt:   input.ExpiresAt,
		CreatedBy:   input.UserID,
		Now:         now,
	})
	if err != nil {
		return VisitorCodeSecret{}, err
	}
	return VisitorCodeSecret{VisitorCode: item, Code: code}, nil
}

func (service *Service) RevokeLink(
	ctx context.Context,
	workspaceID string,
	linkID string,
) (Link, error) {
	return service.repository.RevokeLink(
		ctx,
		workspaceID,
		linkID,
		service.clock().UTC(),
	)
}

func (service *Service) Open(
	ctx context.Context,
	entryToken string,
) (OpenResult, error) {
	if strings.TrimSpace(entryToken) == "" {
		return OpenResult{}, ErrEntryUnavailable
	}
	sessionID, err := newID()
	if err != nil {
		return OpenResult{}, err
	}
	visitorID, err := newID()
	if err != nil {
		return OpenResult{}, err
	}
	sessionToken, sessionDigest, _, err := newToken()
	if err != nil {
		return OpenResult{}, err
	}
	now := service.clock().UTC()
	expiresAt := now.Add(publicSessionLifetime)
	access, err := service.repository.OpenEntry(
		ctx,
		digestToken(entryToken),
		sessionRecord{
			ID:                  sessionID,
			SessionDigest:       sessionDigest,
			VisitorID:           visitorID,
			ExpiresAt:           expiresAt,
			UnverifiedExpiresAt: now.Add(publicUnverifiedSessionLifetime),
			Now:                 now,
		},
	)
	if err != nil {
		return OpenResult{}, err
	}
	expiresAt = access.SessionExpiresAt
	return OpenResult{
		SessionToken:     sessionToken,
		SessionExpiresAt: expiresAt,
		PasswordRequired: access.PasswordHash != "",
		Share:            access.PublicShare,
	}, nil
}

func (service *Service) Verify(
	ctx context.Context,
	sessionToken string,
	password string,
	clientIdentity string,
) (PublicShare, error) {
	now := service.clock().UTC()
	sessionKey := digestToken(sessionToken)
	access, err := service.repository.Session(ctx, sessionKey, now)
	if err != nil {
		return PublicShare{}, err
	}
	if access.PasswordHash == "" {
		return access.PublicShare, nil
	}
	clientIdentity = strings.TrimSpace(clientIdentity)
	if clientIdentity == "" {
		return PublicShare{}, ErrRateLimited
	}
	// Password failures are isolated by both share and client network identity.
	// A new browser session cannot reset this budget, while one hostile client
	// cannot lock every other visitor out of a shared link.
	rateKey := "share:" + access.PublicShare.ID + "|client:" + clientIdentity
	if service.rateLimits != nil {
		allowed, limitErr := service.rateLimits.Consume(ctx, "share-password", rateKey, 5, 5*time.Minute)
		if limitErr != nil {
			return PublicShare{}, limitErr
		}
		if !allowed {
			return PublicShare{}, ErrRateLimited
		}
	} else if service.rateLimited(rateKey, now) {
		return PublicShare{}, ErrRateLimited
	}
	ok, err := verifyPassword(access.PasswordHash, password)
	if err != nil {
		return PublicShare{}, err
	}
	if !ok {
		if service.rateLimits == nil {
			service.recordFailure(rateKey, now)
		}
		return PublicShare{}, ErrPasswordInvalid
	}
	sessionExpiresAt := now.Add(publicSessionLifetime)
	if access.ExpiresAt != nil && access.ExpiresAt.Before(sessionExpiresAt) {
		sessionExpiresAt = *access.ExpiresAt
	}
	if err := service.repository.MarkSessionVerified(ctx, sessionKey, now, sessionExpiresAt); err != nil {
		return PublicShare{}, err
	}
	if service.rateLimits != nil {
		if err := service.rateLimits.Clear(ctx, "share-password", rateKey); err != nil {
			return PublicShare{}, err
		}
	} else {
		service.clearFailures(rateKey)
	}
	return access.PublicShare, nil
}

func (service *Service) Identify(
	ctx context.Context,
	input IdentifyInput,
) (PublicShare, error) {
	input.SessionToken = strings.TrimSpace(input.SessionToken)
	if input.SessionToken == "" {
		return PublicShare{}, ErrSessionNotFound
	}
	method := strings.TrimSpace(input.Method)
	if method == "" {
		if strings.TrimSpace(input.Code) != "" {
			method = "verification_code"
		} else if strings.TrimSpace(input.DisplayName) != "" {
			method = "nickname"
		} else {
			method = "anonymous"
		}
	}
	visitorID, err := newID()
	if err != nil {
		return PublicShare{}, err
	}
	now := service.clock().UTC()
	sessionDigest := digestToken(input.SessionToken)
	switch method {
	case "anonymous":
		access, err := service.repository.IdentifySession(
			ctx,
			sessionDigest,
			visitorIdentityRecord{
				VisitorID:      visitorID,
				IdentityMethod: "anonymous",
				Now:            now,
			},
		)
		if err != nil {
			return PublicShare{}, err
		}
		return access.PublicShare, nil
	case "nickname":
		displayName := strings.TrimSpace(input.DisplayName)
		if err := validateVisitorDisplayName(displayName); err != nil {
			return PublicShare{}, err
		}
		access, err := service.repository.IdentifySession(
			ctx,
			sessionDigest,
			visitorIdentityRecord{
				VisitorID:      visitorID,
				DisplayName:    &displayName,
				IdentityMethod: "nickname",
				Now:            now,
			},
		)
		if err != nil {
			return PublicShare{}, err
		}
		return access.PublicShare, nil
	case "verification_code":
		code := normalizeVisitorCode(input.Code)
		if code == "" {
			return PublicShare{}, ErrVisitorCodeInvalid
		}
		// A visitor verification code is an authentication challenge, so
		// brute-force attempts are bounded by a persistent share+client budget,
		// mirroring the share-password path. Re-opening the page or restarting
		// Core cannot reset this budget.
		access, err := service.repository.Session(ctx, sessionDigest, now)
		if err != nil {
			return PublicShare{}, err
		}
		clientIdentity := strings.TrimSpace(input.ClientIdentity)
		if clientIdentity == "" {
			return PublicShare{}, ErrRateLimited
		}
		rateKey := "share:" + access.PublicShare.ID + "|client:" + clientIdentity
		if service.rateLimits != nil {
			allowed, limitErr := service.rateLimits.Consume(ctx, "share-visitor-code", rateKey, 5, 5*time.Minute)
			if limitErr != nil {
				return PublicShare{}, limitErr
			}
			if !allowed {
				return PublicShare{}, ErrRateLimited
			}
		} else if service.rateLimited(rateKey, now) {
			return PublicShare{}, ErrRateLimited
		}
		access, err = service.repository.IdentifySessionWithCode(
			ctx,
			visitorCodeUseRecord{
				VisitorID:     visitorID,
				SessionDigest: sessionDigest,
				CodeDigest:    digestToken(code),
				Now:           now,
			},
		)
		if err != nil {
			if errors.Is(err, ErrVisitorCodeInvalid) && service.rateLimits == nil {
				service.recordFailure(rateKey, now)
			}
			return PublicShare{}, err
		}
		if service.rateLimits != nil {
			if clearErr := service.rateLimits.Clear(ctx, "share-visitor-code", rateKey); clearErr != nil {
				return PublicShare{}, clearErr
			}
		} else {
			service.clearFailures(rateKey)
		}
		return access.PublicShare, nil
	default:
		return PublicShare{}, errors.New("visitor identity method is invalid")
	}
}

func (service *Service) PublicSession(
	ctx context.Context,
	sessionToken string,
) (PublicShare, error) {
	if strings.TrimSpace(sessionToken) == "" {
		return PublicShare{}, ErrSessionNotFound
	}
	access, err := service.repository.Session(
		ctx,
		digestToken(sessionToken),
		service.clock().UTC(),
	)
	if err != nil {
		return PublicShare{}, err
	}
	if access.PasswordHash != "" && !access.Verified {
		return PublicShare{}, ErrPasswordRequired
	}
	return access.PublicShare, nil
}

func (service *Service) rateLimited(key string, now time.Time) bool {
	service.attemptsMu.Lock()
	defer service.attemptsMu.Unlock()
	attempt := service.attempts[key]
	if attempt.WindowStart.IsZero() || now.Sub(attempt.WindowStart) > 5*time.Minute {
		delete(service.attempts, key)
		return false
	}
	return attempt.Count >= 5
}

func (service *Service) recordFailure(key string, now time.Time) {
	service.attemptsMu.Lock()
	defer service.attemptsMu.Unlock()
	attempt := service.attempts[key]
	if attempt.WindowStart.IsZero() || now.Sub(attempt.WindowStart) > 5*time.Minute {
		attempt = verificationAttempt{WindowStart: now}
	}
	attempt.Count++
	service.attempts[key] = attempt
}

func (service *Service) clearFailures(key string) {
	service.attemptsMu.Lock()
	defer service.attemptsMu.Unlock()
	delete(service.attempts, key)
}

func validateCreate(input CreateInput, now time.Time) error {
	if strings.TrimSpace(input.ReviewSessionID) == "" {
		return errors.New("review session is required")
	}
	if err := validatePolicy(
		strings.TrimSpace(input.Name),
		input.ExpiresAt,
		1,
		now,
	); err != nil {
		return err
	}
	if input.Password != "" {
		return validatePassword(input.Password)
	}
	return nil
}

func validatePolicy(
	name string,
	expiresAt *time.Time,
	revision int,
	now time.Time,
) error {
	if length := len([]rune(name)); length < 1 || length > 120 {
		return errors.New("share name must contain 1 to 120 characters")
	}
	if revision < 1 {
		return errors.New("revision must be positive")
	}
	if expiresAt != nil && !expiresAt.After(now) {
		return errors.New("share expiration must be in the future")
	}
	return nil
}

func validatePassword(password string) error {
	if length := len([]rune(password)); length < 4 || length > 256 {
		return errors.New("share password must contain 4 to 256 characters")
	}
	return nil
}

func validateVisitorDisplayName(displayName string) error {
	if length := len([]rune(displayName)); length < 1 || length > 80 {
		return errors.New("visitor name must contain 1 to 80 characters")
	}
	return nil
}

func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func newToken() (string, string, string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(value)
	prefix := token
	if len(prefix) > 6 {
		prefix = prefix[:6]
	}
	return token, digestToken(token), prefix, nil
}

func newVisitorCode() (string, string, string, error) {
	var value [6]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", "", "", err
	}
	raw := strings.ToUpper(hex.EncodeToString(value[:]))
	code := raw[:4] + "-" + raw[4:8] + "-" + raw[8:]
	return code, digestToken(code), code[:4], nil
}

func normalizeVisitorCode(code string) string {
	value := strings.ToUpper(strings.TrimSpace(code))
	value = strings.ReplaceAll(value, " ", "")
	if len(value) == 12 && !strings.Contains(value, "-") {
		return value[:4] + "-" + value[4:8] + "-" + value[8:]
	}
	return value
}

func digestToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
