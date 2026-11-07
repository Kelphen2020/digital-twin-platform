package store

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"digital-twin-platform/api/internal/model"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Postgres is the durable Store backed by PostgreSQL/PostGIS.
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres connects, verifies the database and applies pending migrations.
func NewPostgres(ctx context.Context, databaseURL string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	store := &Postgres{pool: pool}
	if err := store.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

// Close releases the connection pool.
func (p *Postgres) Close() { p.pool.Close() }

func (p *Postgres) migrate(ctx context.Context) error {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		body, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if _, err := p.pool.Exec(ctx, string(body)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
	}
	return nil
}

func (p *Postgres) ListAreas(ctx context.Context) ([]model.Area, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, name, type, polygon FROM areas ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	areas := make([]model.Area, 0, 8)
	for rows.Next() {
		var area model.Area
		var polygon []byte
		if err := rows.Scan(&area.ID, &area.Name, &area.Type, &polygon); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(polygon, &area.Polygon); err != nil {
			area.Polygon = nil
		}
		areas = append(areas, area)
	}
	return areas, rows.Err()
}

func (p *Postgres) ListCameras(ctx context.Context) ([]model.Camera, error) {
	rows, err := p.pool.Query(ctx, cameraSelect+` ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cameras := make([]model.Camera, 0, 16)
	for rows.Next() {
		camera, err := scanCamera(rows)
		if err != nil {
			return nil, err
		}
		cameras = append(cameras, camera)
	}
	return cameras, rows.Err()
}

func (p *Postgres) GetCamera(ctx context.Context, id string) (model.Camera, error) {
	return scanCamera(p.pool.QueryRow(ctx, cameraSelect+` WHERE id = $1`, id))
}

func (p *Postgres) GetCameraByCode(ctx context.Context, code string) (model.Camera, error) {
	return scanCamera(p.pool.QueryRow(ctx, cameraSelect+` WHERE code = $1`, code))
}

func (p *Postgres) UpdateCamera(ctx context.Context, id string, patch model.CameraPatch) (model.Camera, error) {
	current, err := p.GetCamera(ctx, id)
	if err != nil {
		return model.Camera{}, err
	}
	applyPatch(&current, patch)
	row := p.pool.QueryRow(ctx, `
        UPDATE cameras SET status=$2, x=$3, y=$4, z=$5, heading=$6, updated_at=now()
        WHERE id=$1 RETURNING updated_at`, id, current.Status, current.X, current.Y, current.Z, current.Heading)
	if err := row.Scan(&current.UpdatedAt); err != nil {
		return model.Camera{}, err
	}
	return p.GetCamera(ctx, id)
}

func (p *Postgres) ListEvents(ctx context.Context, filter model.EventFilter) ([]model.Event, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	conditions := []string{}
	args := []any{}
	add := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(args)))
	}
	if filter.Status != "" {
		add("status = $%d", filter.Status)
	}
	if filter.Type != "" {
		add("type = $%d", filter.Type)
	}
	if filter.Since != nil {
		add("occurred_at >= $%d", *filter.Since)
	}
	query := `SELECT id, camera_id, camera_code, camera_name, type, severity, confidence,
                     x, y, z, occurred_at, status, payload FROM events`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += fmt.Sprintf(" ORDER BY occurred_at DESC LIMIT %d", limit)
	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]model.Event, 0, limit)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (p *Postgres) CreateEvents(ctx context.Context, events []model.Event) error {
	if len(events) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, event := range events {
		payload, err := json.Marshal(event.Payload)
		if err != nil {
			return err
		}
		batch.Queue(`INSERT INTO events
            (id, camera_id, camera_code, camera_name, type, severity, confidence, x, y, z, occurred_at, status, payload)
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			event.ID, event.CameraID, event.CameraCode, event.CameraName, event.Type, event.Severity,
			event.Confidence, event.X, event.Y, event.Z, event.OccurredAt, event.Status, payload)
	}
	results := p.pool.SendBatch(ctx, batch)
	defer results.Close()
	for range events {
		if _, err := results.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (p *Postgres) AckEvent(ctx context.Context, id string) (model.Event, error) {
	_, err := p.pool.Exec(ctx, `UPDATE events SET status='acked' WHERE id=$1`, id)
	if err != nil {
		return model.Event{}, err
	}
	return scanEvent(p.pool.QueryRow(ctx, eventSelect+` WHERE id=$1`, id))
}

func (p *Postgres) Counts(ctx context.Context) (model.Counts, error) {
	var counts model.Counts
	err := p.pool.QueryRow(ctx, `
        SELECT (SELECT count(*) FROM events),
               (SELECT count(*) FROM cameras WHERE status='online')`).Scan(&counts.EventsTotal, &counts.OnlineCameras)
	return counts, err
}

const cameraSelect = `SELECT id, code, name, area_id, x, y, z, heading, fov_h, fov_v, range_m, status, stream_url, updated_at FROM cameras`

const eventSelect = `SELECT id, camera_id, camera_code, camera_name, type, severity, confidence,
                            x, y, z, occurred_at, status, payload FROM events`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCamera(row rowScanner) (model.Camera, error) {
	var camera model.Camera
	err := row.Scan(&camera.ID, &camera.Code, &camera.Name, &camera.AreaID, &camera.X, &camera.Y, &camera.Z,
		&camera.Heading, &camera.FovH, &camera.FovV, &camera.RangeM, &camera.Status, &camera.StreamURL, &camera.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return model.Camera{}, ErrNotFound
		}
		return model.Camera{}, err
	}
	return camera, nil
}

func scanEvent(row rowScanner) (model.Event, error) {
	var event model.Event
	var payload []byte
	err := row.Scan(&event.ID, &event.CameraID, &event.CameraCode, &event.CameraName, &event.Type, &event.Severity,
		&event.Confidence, &event.X, &event.Y, &event.Z, &event.OccurredAt, &event.Status, &payload)
	if err != nil {
		if err == pgx.ErrNoRows {
			return model.Event{}, ErrNotFound
		}
		return model.Event{}, err
	}
	event.OccurredAt = event.OccurredAt.UTC()
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &event.Payload)
	}
	return event, nil
}
