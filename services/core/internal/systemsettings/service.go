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
