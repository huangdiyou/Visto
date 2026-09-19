package systemsettings

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"review-studio.local/core/internal/platform/database"
)

func TestSQLiteRepositoryLazilyCreatesNetworkRow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, "DELETE FROM system_network_settings"); err != nil {
		t.Fatalf("delete seeded network settings: %v", err)
	}

	service := NewService(NewSQLiteRepository(db))
	service.clock = func() time.Time {
		return time.Date(2026, time.August, 29, 8, 0, 0, 0, time.UTC)
	}
	item, err := service.GetNetwork(ctx)
	if err != nil {
		t.Fatalf("get network settings: %v", err)
	}
	if item.RequireRemoteHTTPS || item.Revision != 1 || item.UpdatedBy != nil {
		t.Fatalf("unexpected default network settings: %#v", item)
	}
	if !item.UpdatedAt.Equal(time.Date(2026, time.August, 29, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("updated at = %s", item.UpdatedAt)
	}
}

func TestSQLiteRepositoryRejectsStaleNetworkRevision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	service := NewService(NewSQLiteRepository(db))
	service.clock = func() time.Time {
		return time.Date(2026, time.August, 29, 8, 0, 0, 0, time.UTC)
	}
	initial, err := service.GetNetwork(ctx)
	if err != nil {
		t.Fatalf("get initial network settings: %v", err)
	}

	updated, err := service.UpdateNetwork(ctx, UpdateNetworkInput{
		RequireRemoteHTTPS: true,
		Revision:           initial.Revision,
		UpdatedBy:          "owner-1",
	})
	if err != nil {
		t.Fatalf("update network settings: %v", err)
	}
	if !updated.Current.RequireRemoteHTTPS ||
		updated.Current.Revision != initial.Revision+1 {
		t.Fatalf("unexpected updated network settings: %#v", updated.Current)
	}
	// The previous state is returned so the caller can record both sides of the
	// change in the activity log.
	if updated.Previous.RequireRemoteHTTPS ||
		updated.Previous.Revision != initial.Revision {
		t.Fatalf("unexpected previous network settings: %#v", updated.Previous)
	}

	_, err = service.UpdateNetwork(ctx, UpdateNetworkInput{
		RequireRemoteHTTPS: false,
		Revision:           initial.Revision,
		UpdatedBy:          "owner-2",
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale revision error = %v, want ErrRevisionConflict", err)
	}
}
