package localai

import (
	"context"
	"database/sql"
	"errors"
)

// CatalogEntry is one row in the local_models table.
type CatalogEntry struct {
	ID         string
	Name       string
	ModelPath  string
	MmprojPath string
	SourceURL  string
	IsVision   bool
	SizeBytes  int64
	CreatedAt  string
}

func (e *CatalogEntry) HasProjector() bool { return e.MmprojPath != "" }

// List returns all catalog entries ordered by creation time desc.
func List(ctx context.Context, db *sql.DB) ([]CatalogEntry, error) {
	rows, err := db.QueryContext(ctx,
		`select id, name, model_path, coalesce(mmproj_path, ''), coalesce(source_url, ''),
		        is_vision, size_bytes, created_at
		 from local_models order by created_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CatalogEntry
	for rows.Next() {
		var e CatalogEntry
		var vision int
		if err := rows.Scan(&e.ID, &e.Name, &e.ModelPath, &e.MmprojPath, &e.SourceURL,
			&vision, &e.SizeBytes, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.IsVision = vision == 1
		out = append(out, e)
	}
	return out, nil
}

// Get returns one catalog entry by id.
func Get(ctx context.Context, db *sql.DB, id string) (*CatalogEntry, error) {
	var e CatalogEntry
	var vision int
	err := db.QueryRowContext(ctx,
		`select id, name, model_path, coalesce(mmproj_path, ''), coalesce(source_url, ''),
		        is_vision, size_bytes, created_at
		 from local_models where id = ?`, id,
	).Scan(&e.ID, &e.Name, &e.ModelPath, &e.MmprojPath, &e.SourceURL, &vision, &e.SizeBytes, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.IsVision = vision == 1
	return &e, nil
}

// FindByName looks up a catalog entry by its display name.
func FindByName(ctx context.Context, db *sql.DB, name string) (*CatalogEntry, error) {
	var e CatalogEntry
	var vision int
	err := db.QueryRowContext(ctx,
		`select id, name, model_path, coalesce(mmproj_path, ''), coalesce(source_url, ''),
		        is_vision, size_bytes, created_at
		 from local_models where name = ? order by created_at desc limit 1`, name,
	).Scan(&e.ID, &e.Name, &e.ModelPath, &e.MmprojPath, &e.SourceURL, &vision, &e.SizeBytes, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.IsVision = vision == 1
	return &e, nil
}

// Insert adds a new entry to the catalog.
func Insert(ctx context.Context, db *sql.DB, e CatalogEntry) error {
	vision := 0
	if e.IsVision {
		vision = 1
	}
	_, err := db.ExecContext(ctx,
		`insert into local_models (id, name, model_path, mmproj_path, source_url, is_vision, size_bytes)
		 values (?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.Name, e.ModelPath, nullString(e.MmprojPath), nullString(e.SourceURL), vision, e.SizeBytes)
	return err
}

// Delete removes a catalog row by id.
func Delete(ctx context.Context, db *sql.DB, id string) error {
	_, err := db.ExecContext(ctx, `delete from local_models where id = ?`, id)
	return err
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
