package browsersync

import "database/sql"

// Store owns browser replication, device presence, and active-device control.
// Account authentication and event transport are supplied by the application.
type Store struct{ Conn *sql.DB }

func NewStore(conn *sql.DB) *Store { return &Store{Conn: conn} }
