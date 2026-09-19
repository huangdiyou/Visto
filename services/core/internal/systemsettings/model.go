package systemsettings

import (
	"errors"
	"time"
)

var (
	ErrNotFound         = errors.New("system network settings not found")
	ErrInvalidInput     = errors.New("system network settings input is invalid")
	ErrRevisionConflict = errors.New("system network settings revision conflict")
)

// NetworkSettings is the deployment-wide singleton for network access policy.
//
// RequireRemoteHTTPS defaults to false. Visto is a local-first, self-hosted
// product, so a fresh install must not block LAN HTTP access before the Owner
// has any chance to configure TLS. Turning it off only means "HTTP or HTTPS is
// accepted"; it never disables HTTPS. Loopback always accepts plaintext,
// regardless of this value.
type NetworkSettings struct {
	RequireRemoteHTTPS bool
	Revision           int
	UpdatedBy          *string
	UpdatedAt          time.Time
}

type UpdateNetworkInput struct {
	RequireRemoteHTTPS bool
	Revision           int
	UpdatedBy          string
}
