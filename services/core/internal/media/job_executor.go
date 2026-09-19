package media

import (
	"context"
	"encoding/json"
	"errors"

	"review-studio.local/core/internal/job"
)

const ProbeRootJobType = "media.probe_root"

type ProbeRootJobPayload struct {
	RootID string `json:"rootId"`
}

type ProbeRootJobExecutor struct {
	Service *Service
}

func (executor ProbeRootJobExecutor) Execute(
	ctx context.Context,
	item job.Job,
	reporter job.Reporter,
) error {
	var payload ProbeRootJobPayload
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return codedProbeJobError{
			code: "probe.payload_invalid",
			err:  err,
		}
	}
	if payload.RootID == "" ||
		item.SubjectType != "authorizedRoot" ||
		item.SubjectID != payload.RootID {
		return codedProbeJobError{
			code: "probe.subject_invalid",
			err:  errors.New("probe job subject does not match payload"),
		}
	}
	_, err := executor.Service.ProbeRootForJob(
		ctx,
		item.WorkspaceID,
		payload.RootID,
		func(current, total int64) error {
			return reporter.SetProgress(ctx, current, total, "items")
		},
	)
	return err
}

type codedProbeJobError struct {
	code string
	err  error
}

func (jobError codedProbeJobError) Error() string {
	return jobError.err.Error()
}

func (jobError codedProbeJobError) Unwrap() error {
	return jobError.err
}

func (jobError codedProbeJobError) Code() string {
	return jobError.code
}
