package main

import (
	"io/fs"
	"regexp"
	"strconv"
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var migrationFilePattern = regexp.MustCompile(`^(\d+)_([a-z0-9_]+)\.(up|down)\.sql$`)

// TestMigrations_SourceLoads guards the failure this repo keeps hitting: two
// branches each add a migration with the same version, git merges them without
// a conflict because the filenames differ, and the server then exits at startup
// because iofs.New rejects the duplicate version. Needs no database, so CI
// catches it on every PR.
func TestMigrations_SourceLoads(t *testing.T) {
	src, err := iofs.New(migrationsFS, "migrations")
	require.NoError(t, err, "migrations must load; a duplicate version stops the server from starting")
	require.NoError(t, src.Close())
}

func TestMigrations_VersionsAreUniqueAndPaired(t *testing.T) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	require.NoError(t, err)
	require.NotEmpty(t, entries, "no migrations found; the embed pattern may be wrong")

	// version -> direction -> filename
	byVersion := make(map[uint64]map[string]string)
	for _, entry := range entries {
		match := migrationFilePattern.FindStringSubmatch(entry.Name())
		require.NotNil(t, match, "migration filename does not match <version>_<name>.<up|down>.sql: %s", entry.Name())

		version, err := strconv.ParseUint(match[1], 10, 64)
		require.NoError(t, err)
		direction := match[3]

		if byVersion[version] == nil {
			byVersion[version] = make(map[string]string)
		}
		if existing, duplicate := byVersion[version][direction]; duplicate {
			assert.Fail(t, "duplicate migration version",
				"version %d has two %s migrations (%s and %s) — renumber the newer one to the next free version",
				version, direction, existing, entry.Name())
		}
		byVersion[version][direction] = entry.Name()
	}

	for version, files := range byVersion {
		assert.Contains(t, files, "up", "version %d has no up migration", version)
		assert.Contains(t, files, "down", "version %d has no down migration", version)
	}
}
