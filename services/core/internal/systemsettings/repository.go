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

type Repository interface {
	GetNetwork(context.Context, time.Time) (NetworkSettings, error)
	UpdateNetwork(context.Context, updateNetworkRecord) (NetworkUpdate, error)
}
