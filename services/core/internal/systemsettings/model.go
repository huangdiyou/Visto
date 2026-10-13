package systemsettings

import (
	"errors"
	"time"
)

var (
	ErrNotFound              = errors.New("system network settings not found")
	ErrHostAccessNotFound    = errors.New("system host access settings not found")
	ErrMediaEncodingNotFound = errors.New("system media encoding settings not found")
	ErrInvalidInput          = errors.New("system network settings input is invalid")
	ErrRevisionConflict      = errors.New("system network settings revision conflict")
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

// HostAccessSettings records whether an Owner web session may reach the host
// management surface.
//
// D2, docs/FREE_TIER_BOUNDARY_DESIGN.md: the free tier has to let the Owner add a
// storage location from the browser. The choice is made once in the first-run
// wizard and is deliberately not changeable from the web afterwards, because a
// setting that grants host-level access must not be flippable by the session
// that benefits from it. It stays false for any instance that never recorded a
// choice, so an upgrade cannot silently relax the boundary.
type HostAccessSettings struct {
	AllowWebHostPaths bool
	Revision          int
	UpdatedBy         *string
	UpdatedAt         time.Time
}

type SetHostAccessInput struct {
	AllowWebHostPaths bool
	UpdatedBy         string
}

// MediaEncodingSettings is the deployment-wide singleton for H.264 encoder
// selection (docs/MEDIA_ENCODING_SELECTION_DESIGN.md 3.5).
//
// Every field starts empty, so an instance upgraded from before this setting
// existed keeps the encoder it already used until a probe has actually run.
// PreferredEncoder is the Owner's override; empty means "follow the probe".
// DetectedEncoders is the last probe sweep, ActiveEncoder is what the next job
// uses, and the Tripped* fields explain a breaker that changed the encoder so a
// restart cannot silently forget why.
type MediaEncodingSettings struct {
	PreferredEncoder string
	DetectedEncoders []string
	DetectedAt       *time.Time
	ActiveEncoder    string
	FailureCount     int
	TrippedEncoder   *string
	TrippedReason    *string
	TrippedAt        *time.Time
	Revision         int
	UpdatedBy        *string
	UpdatedAt        time.Time
}

type UpdateMediaEncodingInput struct {
	PreferredEncoder string
	Revision         int
	UpdatedBy        string
}

// RecordMediaEncodingRuntimeInput writes the columns the encoder job owns: which
// encoder is active, how many times it has failed in a row, and why the breaker
// moved. Unlike UpdateMediaEncodingInput it is not an Owner edit and carries no
// revision handshake, because a job is the only writer and two Owners cannot race
// for it. UpdatedBy still names an actor so the activity record is complete.
//
// It deliberately cannot write the sweep columns: a job failure says nothing
// about which encoders exist, and writing them here would erase the probe result.
type RecordMediaEncodingRuntimeInput struct {
	ActiveEncoder  string
	FailureCount   int
	TrippedEncoder string
	TrippedReason  string
	TrippedAt      *time.Time
	UpdatedBy      string
}

// RecordMediaEncodingProbeInput writes what a sweep found. It cannot reach the
// runtime columns, so a sweep can never clear the encoder in use or end a trip by
// accident; the selection service decides those separately.
type RecordMediaEncodingProbeInput struct {
	// DetectedEncoders is the sweep's result. A nil list is stored as empty
	// rather than as null, because "probed and found nothing" is an answer.
	DetectedEncoders []string
	UpdatedBy        string
}
