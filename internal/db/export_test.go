package db

import "database/sql"

// Versions lists the versions of the embedded migrations, for tests only.
func Versions(conn *sql.DB) ([]int64, error) {
	p, err := newProvider(conn)
	if err != nil {
		return nil, err
	}
	var versions []int64
	for _, s := range p.ListSources() {
		versions = append(versions, s.Version)
	}
	return versions, nil
}
