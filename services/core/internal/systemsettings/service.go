package systemsettings

import (
	"context"
	"strings"
	"time"
)

type Service struct {
	repository Repository
	clock      func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, clock: time.Now}
}

func (service *Service) GetNetwork(ctx context.Context) (NetworkSettings, error) {
	return service.repository.GetNetwork(ctx, service.clock().UTC())
}

func (service *Service) UpdateNetwork(
	ctx context.Context,
	input UpdateNetworkInput,
) (NetworkUpdate, error) {
	input.UpdatedBy = strings.TrimSpace(input.UpdatedBy)
	if input.UpdatedBy == "" || input.Revision < 1 {
		return NetworkUpdate{}, ErrInvalidInput
	}
	return service.repository.UpdateNetwork(ctx, updateNetworkRecord{
		UpdateNetworkInput: input,
		Now:                service.clock().UTC(),
	})
}

func (service *Service) GetHostAccess(ctx context.Context) (HostAccessSettings, error) {
	return service.repository.GetHostAccess(ctx, service.clock().UTC())
}

// SetHostAccess records the first-run wizard's choice once. The caller passes the
// Owner account that answered, so the audit trail names a real actor.
func (service *Service) SetHostAccess(
	ctx context.Context,
	input SetHostAccessInput,
) (HostAccessUpdate, error) {
	input.UpdatedBy = strings.TrimSpace(input.UpdatedBy)
	if input.UpdatedBy == "" {
		return HostAccessUpdate{}, ErrInvalidInput
	}
	return service.repository.SetHostAccess(ctx, setHostAccessRecord{
		SetHostAccessInput: input,
		Now:                service.clock().UTC(),
	})
}

// RecordMediaEncodingRuntime stores what the encoder job decided: the encoder in
// use, its consecutive failure count and any breaker trip. It does not go through
// UpdateMediaEncoding because it is not an Owner edit and must not disturb the
// Owner's revision.
func (service *Service) RecordMediaEncodingRuntime(
	ctx context.Context,
	input RecordMediaEncodingRuntimeInput,
) (MediaEncodingUpdate, error) {
	input.ActiveEncoder = strings.TrimSpace(input.ActiveEncoder)
	input.TrippedEncoder = strings.TrimSpace(input.TrippedEncoder)
	input.TrippedReason = strings.TrimSpace(input.TrippedReason)
	input.UpdatedBy = strings.TrimSpace(input.UpdatedBy)
	if input.UpdatedBy == "" || input.FailureCount < 0 {
		return MediaEncodingUpdate{}, ErrInvalidInput
	}
	if input.ActiveEncoder != "" && !knownMediaEncoders[input.ActiveEncoder] {
		return MediaEncodingUpdate{}, ErrInvalidInput
	}
	if input.TrippedEncoder != "" && !knownMediaEncoders[input.TrippedEncoder] {
		return MediaEncodingUpdate{}, ErrInvalidInput
	}
	return service.repository.RecordMediaEncodingRuntime(ctx, recordMediaEncodingRuntimeRecord{
		RecordMediaEncodingRuntimeInput: input,
		Now:                             service.clock().UTC(),
	})
}

// RecordMediaEncodingProbe stores what a sweep of the installed runtime found. It
// does not go through UpdateMediaEncoding because it is not an Owner edit and must
// not disturb the Owner's revision, and it does not go through
// RecordMediaEncodingRuntime because a sweep must not touch the encoder in use.
func (service *Service) RecordMediaEncodingProbe(
	ctx context.Context,
	input RecordMediaEncodingProbeInput,
) (MediaEncodingUpdate, error) {
	input.UpdatedBy = strings.TrimSpace(input.UpdatedBy)
	if input.UpdatedBy == "" {
		return MediaEncodingUpdate{}, ErrInvalidInput
	}
	detected := input.DetectedEncoders
	if detected == nil {
		// "Probed and found nothing" is an answer the pane has to be able to
		// show, so it is stored rather than skipped.
		detected = []string{}
	}
	return service.repository.RecordMediaEncodingProbe(ctx, recordMediaEncodingProbeRecord{
		RecordMediaEncodingProbeInput: RecordMediaEncodingProbeInput{
			DetectedEncoders: detected,
			UpdatedBy:        input.UpdatedBy,
		},
		Now: service.clock().UTC(),
	})
}

// knownMediaEncoders is the set an Owner may name explicitly. It mirrors the
// candidates in internal/mediaruntime. An encoder that is known but unavailable
// on this host is still accepted: the design lets the next job fail and trip the
// breaker instead of refusing the Owner's choice up front
// (docs/MEDIA_ENCODING_SELECTION_DESIGN.md 3.2).
var knownMediaEncoders = map[string]bool{
	"h264_videotoolbox": true,
	"h264_nvenc":        true,
	"h264_qsv":          true,
	"h264_amf":          true,
	"libopenh264":       true,
	"libx264":           true,
}

func (service *Service) GetMediaEncoding(ctx context.Context) (MediaEncodingSettings, error) {
	return service.repository.GetMediaEncoding(ctx, service.clock().UTC())
}

// UpdateMediaEncoding stores the Owner's default encoder. An empty value means
// "follow the probe", which is what an instance that never chose anything keeps.
func (service *Service) UpdateMediaEncoding(
	ctx context.Context,
	input UpdateMediaEncodingInput,
) (MediaEncodingUpdate, error) {
	input.PreferredEncoder = strings.TrimSpace(input.PreferredEncoder)
	input.UpdatedBy = strings.TrimSpace(input.UpdatedBy)
	if input.UpdatedBy == "" || input.Revision < 1 {
		return MediaEncodingUpdate{}, ErrInvalidInput
	}
	if input.PreferredEncoder != "" && !knownMediaEncoders[input.PreferredEncoder] {
		return MediaEncodingUpdate{}, ErrInvalidInput
	}
	return service.repository.UpdateMediaEncoding(ctx, updateMediaEncodingRecord{
		UpdateMediaEncodingInput: input,
		Now:                      service.clock().UTC(),
	})
}
