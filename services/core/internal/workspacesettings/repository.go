package workspacesettings

import (
	"context"
	"time"
)

type updateRegistrationRecord struct {
	UpdateRegistrationInput
	Now time.Time
}

type Repository interface {
	GetRegistration(context.Context, string, time.Time) (RegistrationSettings, error)
	UpdateRegistration(context.Context, updateRegistrationRecord) (RegistrationSettings, error)
	PublicRegistration(context.Context) (PublicRegistration, error)
}
