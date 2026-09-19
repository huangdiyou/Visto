package workspacesettings

import (
	"errors"
	"time"
)

var (
	ErrNotFound                  = errors.New("workspace settings not found")
	ErrInvalidInput              = errors.New("workspace settings input is invalid")
	ErrRevisionConflict          = errors.New("workspace settings revision conflict")
	ErrRegistrationClosed        = errors.New("workspace registration is closed")
	ErrEmailVerificationRequired = errors.New("email verification is required")
)

type RegistrationSettings struct {
	WorkspaceID               string
	WorkspaceName             string
	TeamName                  string
	RegistrationEnabled       bool
	EmailVerificationRequired bool
	DefaultWorkspaceRole      string
	Revision                  int
	UpdatedBy                 *string
	UpdatedAt                 time.Time
}

type UpdateRegistrationInput struct {
	WorkspaceID               string
	TeamName                  string
	RegistrationEnabled       bool
	EmailVerificationRequired bool
	Revision                  int
	UpdatedBy                 string
}

type PublicRegistration struct {
	WorkspaceID               string
	WorkspaceName             string
	TeamName                  string
	RegistrationEnabled       bool
	EmailVerificationRequired bool
	DefaultWorkspaceRole      string
}
