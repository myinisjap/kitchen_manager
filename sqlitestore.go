package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"time"
)

// sqliteStore implements the scs/v2 Store interface using the existing SQLite
// database. Session tokens are HMAC-signed with SESSION_SECRET so they cannot
// be forged, and sessions survive server restarts.
type sqliteStore struct {
	db  *sql.DB
	key []byte // 32-byte HMAC key derived from SESSION_SECRET
}

func newSQLiteStore(db *sql.DB, key []byte) *sqliteStore {
	return &sqliteStore{db: db, key: key}
}

// signToken returns a base64url-encoded HMAC-SHA256 signature of the token.
func (s *sqliteStore) signToken(token string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(token))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Find retrieves session data by token. Returns (nil, false, nil) when not
// found or expired.
func (s *sqliteStore) Find(token string) ([]byte, bool, error) {
	var data []byte
	var expiry time.Time
	err := s.db.QueryRow(
		`SELECT data, expiry FROM sessions WHERE token = ? AND expiry > ?`,
		s.signToken(token), time.Now().UTC(),
	).Scan(&data, &expiry)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// Commit upserts session data. Expired sessions are pruned opportunistically.
func (s *sqliteStore) Commit(token string, b []byte, expiry time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO sessions (token, data, expiry) VALUES (?, ?, ?)
		 ON CONFLICT(token) DO UPDATE SET data = excluded.data, expiry = excluded.expiry`,
		s.signToken(token), b, expiry.UTC(),
	)
	if err != nil {
		return err
	}
	// Best-effort cleanup of expired sessions.
	_, _ = s.db.Exec(`DELETE FROM sessions WHERE expiry <= ?`, time.Now().UTC())
	return nil
}

// Delete removes a session by token.
func (s *sqliteStore) Delete(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token = ?`, s.signToken(token))
	return err
}
