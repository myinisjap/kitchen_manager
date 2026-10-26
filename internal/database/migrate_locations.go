package database

import "database/sql"

// migrateRoomsAndStorageUnits introduces the two-level location hierarchy:
// a room contains storage units, and a stock lot points at one storage unit.
// It replaces the free-text stock_lots.location, which made "Kitchen",
// "kitchen" and "Kitchen counter" three different places.
//
// storage_unit_id lives on stock_lots rather than products because location is
// a property of a physical quantity ("3 cans, in the pantry"), not of the
// abstract item.
//
// Both tables are created IF NOT EXISTS: a database that ran the pre-versioning
// code already has them (with data), and those rows must be kept.
//
// See docs/rooms-storage-units-intent.md.
func migrateRoomsAndStorageUnits(tx *sql.Tx) error {
	for _, s := range []string{
		`CREATE TABLE IF NOT EXISTS rooms (
			id   INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT    NOT NULL UNIQUE
		)`,
		// UNIQUE(room_id, name) and not a global unique name: "Kitchen > Shelf"
		// and "Garage > Shelf" are different places.
		`CREATE TABLE IF NOT EXISTS storage_units (
			id      INTEGER PRIMARY KEY AUTOINCREMENT,
			room_id INTEGER NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
			name    TEXT    NOT NULL,
			UNIQUE(room_id, name)
		)`,
	} {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}
	// Nullable: an item whose location is unknown is valid ("Unfiled"). No
	// ON DELETE clause — deletes are refused at the handler while referenced,
	// so the database never silently forgets where food was.
	if err := addColumnIfMissing(tx, "stock_lots", "storage_unit_id",
		`storage_unit_id INTEGER REFERENCES storage_units(id)`); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`CREATE INDEX IF NOT EXISTS stock_lots_storage_unit_idx ON stock_lots(storage_unit_id)`,
	); err != nil {
		return err
	}
	return backfillStorageUnits(tx)
}

// UnsortedRoom holds storage units synthesised from legacy location text. The
// name makes it obvious which places still need filing into a real room.
const UnsortedRoom = "Unsorted"

// backfillStorageUnits turns each distinct legacy stock_lots.location into a
// storage unit under UnsortedRoom and links its lots.
//
// Only rows that still have location text AND no storage_unit_id are touched,
// so this is a no-op on every run after the first.
//
// Where a unit of that name already exists it is reused rather than duplicated,
// which matters for databases carrying rooms/storage_units from the
// pre-versioning code. A name that exists in several rooms is ambiguous, so
// those lots are deliberately left unlinked ("Unfiled") for the user to file by
// hand rather than guessing a room for them.
//
// stock_lots.location is deliberately left in place: this codebase never drops
// columns, and it is the only record of the original text if a link is wrong.
func backfillStorageUnits(tx *sql.Tx) error {
	rows, err := tx.Query(
		`SELECT DISTINCT location FROM stock_lots WHERE location <> '' AND storage_unit_id IS NULL`)
	if err != nil {
		return err
	}
	var legacy []string
	for rows.Next() {
		var loc string
		if err := rows.Scan(&loc); err != nil {
			rows.Close()
			return err
		}
		legacy = append(legacy, loc)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(legacy) == 0 {
		return nil
	}

	for _, loc := range legacy {
		// Ambiguous: same unit name in more than one room. Leave it Unfiled.
		var matches int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM storage_units WHERE name=?`, loc).Scan(&matches); err != nil {
			return err
		}
		if matches > 1 {
			continue
		}
		var unitID int64
		if matches == 1 {
			if err := tx.QueryRow(`SELECT id FROM storage_units WHERE name=?`, loc).Scan(&unitID); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO rooms (name) VALUES (?)`, UnsortedRoom); err != nil {
				return err
			}
			var roomID int64
			if err := tx.QueryRow(`SELECT id FROM rooms WHERE name=?`, UnsortedRoom).Scan(&roomID); err != nil {
				return err
			}
			res, err := tx.Exec(`INSERT INTO storage_units (room_id, name) VALUES (?,?)`, roomID, loc)
			if err != nil {
				return err
			}
			if unitID, err = res.LastInsertId(); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(
			`UPDATE stock_lots SET storage_unit_id=? WHERE location=? AND storage_unit_id IS NULL`,
			unitID, loc,
		); err != nil {
			return err
		}
	}
	return nil
}

// Backfill re-runs the legacy location backfill against an open database.
// Migration 11 already calls it; this exists so tests (and any future repair
// command) can invoke it directly. Safe to call repeatedly.
func Backfill(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := backfillStorageUnits(tx); err != nil {
		return err
	}
	return tx.Commit()
}
