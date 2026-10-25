// Package testutil provides shared test helpers.
package testutil

import (
	"database/sql"
	"testing"

	"kitchen_manager/internal/database"
)

// NewDB opens an in-memory SQLite database using the production schema
// (database.OpenDB) and closes it when the test ends.
func NewDB(t testing.TB) *sql.DB {
	t.Helper()
	db, err := database.OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
