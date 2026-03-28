package main

import (
	"database/sql"
	_ "modernc.org/sqlite"
)

var db *sql.DB

func openDB(path string) error {
	var err error
	db, err = sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1) // SQLite: single writer
	return createSchema()
}

func createSchema() error {
	_, err := db.Exec(`
	PRAGMA foreign_keys = ON;

	CREATE TABLE IF NOT EXISTS inventory (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		name            TEXT    NOT NULL,
		quantity        REAL    NOT NULL DEFAULT 0,
		unit            TEXT    NOT NULL DEFAULT '',
		location        TEXT    NOT NULL DEFAULT '',
		expiration_date TEXT    NOT NULL DEFAULT '',
		low_threshold   REAL    NOT NULL DEFAULT 1,
		barcode         TEXT    NOT NULL DEFAULT '',
		preferred_unit  TEXT    NOT NULL DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS shopping_list (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		inventory_id     INTEGER REFERENCES inventory(id),
		name             TEXT    NOT NULL,
		quantity_needed  REAL    NOT NULL DEFAULT 1,
		unit             TEXT    NOT NULL DEFAULT '',
		checked          INTEGER NOT NULL DEFAULT 0,
		source           TEXT    NOT NULL DEFAULT 'manual'
	);

	CREATE TABLE IF NOT EXISTS recipes (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		name         TEXT    NOT NULL,
		description  TEXT    NOT NULL DEFAULT '',
		instructions TEXT    NOT NULL DEFAULT '',
		tags         TEXT    NOT NULL DEFAULT '',
		servings     INTEGER NOT NULL DEFAULT 1
	);

	CREATE TABLE IF NOT EXISTS recipe_ingredients (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		recipe_id    INTEGER NOT NULL REFERENCES recipes(id),
		inventory_id INTEGER REFERENCES inventory(id),
		name         TEXT    NOT NULL,
		quantity     REAL    NOT NULL,
		unit         TEXT    NOT NULL DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS meal_calendar (
		id        INTEGER PRIMARY KEY AUTOINCREMENT,
		date      TEXT    NOT NULL,
		meal_slot TEXT    NOT NULL DEFAULT 'dinner',
		recipe_id INTEGER NOT NULL REFERENCES recipes(id),
		servings  INTEGER NOT NULL DEFAULT 1
	);

	CREATE TABLE IF NOT EXISTS sessions (
		token  TEXT PRIMARY KEY,
		data   BLOB NOT NULL,
		expiry DATETIME NOT NULL
	);

	CREATE INDEX IF NOT EXISTS sessions_expiry_idx ON sessions(expiry);
	`)
	if err != nil {
		return err
	}
	// Migration: add preferred_unit to inventory for databases created before this column
	rows, err := db.Query(`PRAGMA table_info(inventory)`)
	if err != nil {
		return err
	}
	var cols []string
	for rows.Next() {
		var cid int
		var name, colType, notNull, pk string
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			rows.Close()
			return err
		}
		cols = append(cols, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	hasPrefUnit := false
	for _, c := range cols {
		if c == "preferred_unit" {
			hasPrefUnit = true
			break
		}
	}
	if !hasPrefUnit {
		if _, err := db.Exec(`ALTER TABLE inventory ADD COLUMN preferred_unit TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	return nil
}
