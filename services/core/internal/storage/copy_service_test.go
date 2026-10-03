package storage

import (
	"context"
	"errors"
	"testing"
)

type archiveLimitRepository struct {
	Repository
	plan ProjectArchivePlan
}

func (r archiveLimitRepository) ProjectArchivePlan(context.Context, string, string) (ProjectArchivePlan, error) {
	return r.plan, nil
}

// The embedded nil repository deliberately panics if task creation is reached:
// a rejected plan must not create the first task, even for alternate repositories.
func TestArchiveLimitRejectsBeforeCreatingAnyTask(t *testing.T) {
	objects := make([]StoredObject, maxProjectArchiveObjects+1)
	service := NewService(archiveLimitRepository{plan: ProjectArchivePlan{TargetRootID: "archive", Objects: objects}})
	tasks, err := service.CreateProjectArchiveTasks(context.Background(), "workspace", "project")
	if !errors.Is(err, ErrProjectArchiveLimitExceeded) || len(tasks) != 0 {
		t.Fatalf("tasks=%d err=%v", len(tasks), err)
	}
}
func TestArchiveLimitAllowsExactBoundary(t *testing.T) {
	objects := make([]StoredObject, maxProjectArchiveObjects)
	for i := range objects {
		objects[i].AuthorizedRootID = "archive"
	}
	service := NewService(archiveLimitRepository{plan: ProjectArchivePlan{TargetRootID: "archive", Objects: objects}})
	if _, err := service.CreateProjectArchiveTasks(context.Background(), "workspace", "project"); err != nil {
		t.Fatal(err)
	}
}
