package hostbackup

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// DatabaseFileName is the SQLite database inside a data directory.
const DatabaseFileName = "review-studio.db"

// StorageRootFact is one authorized root as recorded in the archived database.
// Only local providers carry a filesystem path; WebDAV/S3 providers keep their
// endpoints and credentials in config and the secret store, so their content is
// never part of a data-directory backup.
type StorageRootFact struct {
	RootID      string
	WorkspaceID string
	DisplayName string
	Kind        string
	Purpose     string
	PathText    string
	Status      string
}

// DatabaseFacts is everything the precheck needs from an archived database.
type DatabaseFacts struct {
	SchemaVersion int
	QuickCheck    string
	Roots         []StorageRootFact
}

// ReadDatabaseFacts opens an isolated copy of the database under dataDir and
// reads the storage-root facts. The original files are never touched: the
// database and its WAL/SHM sidecars are copied to a temporary directory first
// and opened there, so WAL replay happens on the copy and a live database is
// never connected to.
func ReadDatabaseFacts(dataDir string) (*DatabaseFacts, error) {
	databasePath := filepath.Join(dataDir, DatabaseFileName)
	if _, err := os.Stat(databasePath); err != nil {
		return nil, fmt.Errorf("archived data directory does not contain %s", DatabaseFileName)
	}

	workDir, err := os.MkdirTemp("", "visto-restore-inspect-")
	if err != nil {
		return nil, fmt.Errorf("create inspection directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	copyPath := filepath.Join(workDir, DatabaseFileName)
	if err := copyFile(copyPath, databasePath); err != nil {
		return nil, fmt.Errorf("copy archived database: %w", err)
	}
	for _, sidecar := range []string{"-wal", "-shm"} {
		sidecarSource := databasePath + sidecar
		if _, err := os.Stat(sidecarSource); err != nil {
			continue
		}
		if err := copyFile(copyPath+sidecar, sidecarSource); err != nil {
			return nil, fmt.Errorf("copy archived database sidecar: %w", err)
		}
	}

	db, err := sql.Open("sqlite", copyPath)
	if err != nil {
		return nil, fmt.Errorf("open archived database copy: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	facts := &DatabaseFacts{}
	if err := db.QueryRow("PRAGMA user_version").Scan(&facts.SchemaVersion); err != nil {
		return nil, fmt.Errorf("read archived database schema version: %w", err)
	}
	facts.QuickCheck, err = quickCheck(db)
	if err != nil {
		return nil, err
	}
	if facts.QuickCheck != "ok" {
		return nil, fmt.Errorf("archived database consistency check failed: %s", facts.QuickCheck)
	}
	facts.Roots, err = readRoots(db)
	if err != nil {
		return nil, err
	}
	return facts, nil
}

// quickCheck returns "ok" or the first reported problems. A database that
// cannot answer a consistency check cannot prove the path facts the precheck
// relies on.
func quickCheck(db *sql.DB) (string, error) {
	rows, err := db.Query("PRAGMA quick_check")
	if err != nil {
		return "", fmt.Errorf("run archived database consistency check: %w", err)
	}
	defer rows.Close()
	problems := []string{}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return "", fmt.Errorf("read archived database consistency check: %w", err)
		}
		if line == "ok" {
			return "ok", nil
		}
		if len(problems) < 3 {
			problems = append(problems, line)
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("read archived database consistency check: %w", err)
	}
	if len(problems) == 0 {
		return "", fmt.Errorf("archived database consistency check returned no result")
	}
	return strings.Join(problems, "; "), nil
}

func readRoots(db *sql.DB) ([]StorageRootFact, error) {
	query := `
		SELECT
			r.id, r.workspace_id, r.display_name, r.status, r.purpose,
			COALESCE(p.kind, ''), COALESCE(s.path_text, '')
		FROM authorized_roots r
		LEFT JOIN storage_providers p ON p.id = r.storage_provider_id
		LEFT JOIN local_path_secrets s ON s.id = r.path_secret_ref
		WHERE r.deleted_at IS NULL
	`
	roots, err := scanRoots(db, query)
	if err == nil {
		return roots, nil
	}
	// Databases from before migration 0011 have no authorized_roots.purpose
	// column. They can only appear in a metadata v2 archive through manual
	// reassembly; report that instead of silently treating the root set as
	// trustworthy.
	return nil, fmt.Errorf("read storage roots from archived database: %w", err)
}

func scanRoots(db *sql.DB, query string) ([]StorageRootFact, error) {
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roots := []StorageRootFact{}
	for rows.Next() {
		var root StorageRootFact
		if err := rows.Scan(
			&root.RootID, &root.WorkspaceID, &root.DisplayName, &root.Status,
			&root.Purpose, &root.Kind, &root.PathText,
		); err != nil {
			return nil, err
		}
		roots = append(roots, root)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return roots, nil
}

func copyFile(destination, source string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return err
	}
	return output.Close()
}
