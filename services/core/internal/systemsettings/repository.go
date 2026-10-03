package systemsettings

import (
	"context"
	"time"
)

type updateNetworkRecord struct {
	UpdateNetworkInput
	Now time.Time
}

// NetworkUpdate carries both sides of a change so the caller can write an
// activity record with the previous and the new state.
type NetworkUpdate struct {
	Previous NetworkSettings
	Current  NetworkSettings
}

type setHostAccessRecord struct {
	SetHostAccessInput
	Now time.Time
}

// HostAccessUpdate carries both sides of a change for the activity record.
type HostAccessUpdate struct {
	Previous HostAccessSettings
	Current  HostAccessSettings
}

type updateMediaEncodingRecord struct {
	UpdateMediaEncodingInput
	Now time.Time
}

// recordMediaEncodingRuntimeRecord is the breaker-owned write. It has no revision
// because it is not an Owner edit.
type recordMediaEncodingRuntimeRecord struct {
	RecordMediaEncodingRuntimeInput
	Now time.Time
}

// recordMediaEncodingProbeRecord is the sweep-owned write. It is separate from
// the runtime record because a sweep knows which encoders exist, not which one is
// in use: sharing one statement let a sweep clear the runtime choice and a trip.
type recordMediaEncodingProbeRecord struct {
	RecordMediaEncodingProbeInput
	Now time.Time
}

// MediaEncodingUpdate carries both sides of a change for the activity record.
type MediaEncodingUpdate struct {
	Previous MediaEncodingSettings
	Current  MediaEncodingSettings
}

type Repository interface {
	GetNetwork(context.Context, time.Time) (NetworkSettings, error)
	UpdateNetwork(context.Context, updateNetworkRecord) (NetworkUpdate, error)
	GetHostAccess(context.Context, time.Time) (HostAccessSettings, error)
	SetHostAccess(context.Context, setHostAccessRecord) (HostAccessUpdate, error)
	GetMediaEncoding(context.Context, time.Time) (MediaEncodingSettings, error)
	UpdateMediaEncoding(context.Context, updateMediaEncodingRecord) (MediaEncodingUpdate, error)
	RecordMediaEncodingRuntime(context.Context, recordMediaEncodingRuntimeRecord) (MediaEncodingUpdate, error)
	RecordMediaEncodingProbe(context.Context, recordMediaEncodingProbeRecord) (MediaEncodingUpdate, error)
}
