package store

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

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
	_, err = p.pool.Exec(ctx, `
        UPDATE cameras SET status=$2, x=$3, y=$4, z=$5, heading=$6, updated_at=now()
        WHERE id=$1`, id, current.Status, current.X, current.Y, current.Z, current.Heading)
	if err != nil {
		return model.Camera{}, err
	}
	return p.GetCamera(ctx, id)
}

func (p *Postgres) SetCameraCalibration(ctx context.Context, id string, calibration *model.Calibration) (model.Camera, error) {
	if calibration == nil {
		_, err := p.pool.Exec(ctx, `UPDATE cameras SET calibration=NULL, updated_at=now() WHERE id=$1`, id)
		if err != nil {
			return model.Camera{}, err
		}
		return p.GetCamera(ctx, id)
	}
	now := time.Now().UTC()
	clone := *calibration
	clone.SolvedAt = &now
	raw, err := json.Marshal(clone)
	if err != nil {
		return model.Camera{}, err
	}
	_, err = p.pool.Exec(ctx, `UPDATE cameras SET calibration=$2, updated_at=now() WHERE id=$1`, id, raw)
	if err != nil {
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
	query := eventSelect
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

func (p *Postgres) GetEvent(ctx context.Context, id string) (model.Event, error) {
	return scanEvent(p.pool.QueryRow(ctx, eventSelect+` WHERE id=$1`, id))
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
	return p.GetEvent(ctx, id)
}

func (p *Postgres) CloseEvent(ctx context.Context, id string) (model.Event, error) {
	_, err := p.pool.Exec(ctx, `UPDATE events SET status='closed', closed_at=now() WHERE id=$1`, id)
	if err != nil {
		return model.Event{}, err
	}
	return p.GetEvent(ctx, id)
}

func (p *Postgres) CreateEventAction(ctx context.Context, action model.EventAction) error {
	_, err := p.pool.Exec(ctx, `
        INSERT INTO event_actions (id, event_id, action, actor, target, note, at)
        VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		action.ID, action.EventID, action.Action, action.Actor, action.Target, action.Note, action.At)
	return err
}

func (p *Postgres) ListEventActions(ctx context.Context, eventID string) ([]model.EventAction, error) {
	rows, err := p.pool.Query(ctx, `
        SELECT id, event_id, action, actor, target, note, at
        FROM event_actions WHERE event_id=$1 ORDER BY at`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	actions := make([]model.EventAction, 0, 4)
	for rows.Next() {
		var action model.EventAction
		if err := rows.Scan(&action.ID, &action.EventID, &action.Action, &action.Actor,
			&action.Target, &action.Note, &action.At); err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	return actions, rows.Err()
}

func (p *Postgres) CreateTrackPoints(ctx context.Context, points []model.TrackPoint) error {
	if len(points) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, point := range points {
		batch.Queue(`INSERT INTO track_points (camera_id, track_id, class, x, y, z, ts)
            VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			point.CameraID, point.TrackID, point.Class, point.X, point.Y, point.Z, point.TS)
	}
	results := p.pool.SendBatch(ctx, batch)
	defer results.Close()
	for range points {
		if _, err := results.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (p *Postgres) ListTrackPoints(ctx context.Context, filter model.TrackFilter) ([]model.TrackPoint, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 500
	}
	conditions := []string{}
	args := []any{}
	add := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(args)))
	}
	if filter.CameraID != "" {
		add("camera_id = $%d", filter.CameraID)
	}
	if filter.Since != nil {
		add("ts >= $%d", *filter.Since)
	}
	query := `SELECT track_id, camera_id, class, x, y, z, ts FROM track_points`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += fmt.Sprintf(" ORDER BY ts DESC LIMIT %d", limit)
	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	points := make([]model.TrackPoint, 0, limit)
	for rows.Next() {
		var point model.TrackPoint
		if err := rows.Scan(&point.TrackID, &point.CameraID, &point.Class,
			&point.X, &point.Y, &point.Z, &point.TS); err != nil {
			return nil, err
		}
		points = append(points, point)
	}
	return points, rows.Err()
}

func (p *Postgres) AddAudit(ctx context.Context, entry model.AuditEntry) error {
	detail, err := json.Marshal(entry.Detail)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `
        INSERT INTO audit_log (id, at, actor, action, resource, detail)
        VALUES ($1,$2,$3,$4,$5,$6)`,
		entry.ID, entry.At, entry.Actor, entry.Action, entry.Resource, detail)
	return err
}

func (p *Postgres) ListAudit(ctx context.Context, filter model.AuditFilter) ([]model.AuditEntry, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	conditions := []string{}
	args := []any{}
	add := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(args)))
	}
	if filter.Action != "" {
		add("action = $%d", filter.Action)
	}
	if filter.Actor != "" {
		add("actor = $%d", filter.Actor)
	}
	query := `SELECT id, at, actor, action, resource, detail FROM audit_log`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += fmt.Sprintf(" ORDER BY at DESC LIMIT %d", limit)
	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]model.AuditEntry, 0, limit)
	for rows.Next() {
		var entry model.AuditEntry
		var detail []byte
		if err := rows.Scan(&entry.ID, &entry.At, &entry.Actor, &entry.Action, &entry.Resource, &detail); err != nil {
			return nil, err
		}
		if len(detail) > 0 {
			_ = json.Unmarshal(detail, &entry.Detail)
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (p *Postgres) Stats(ctx context.Context, from, to time.Time) (model.StatsSummary, error) {
	summary := model.StatsSummary{
		From:   from,
		To:     to,
		Totals: model.StatsTotals{BySeverity: map[string]int64{}, ByType: map[string]int64{}},
	}

	rows, err := p.pool.Query(ctx,
		`SELECT severity, count(*) FROM events WHERE occurred_at >= $1 AND occurred_at <= $2 GROUP BY severity`, from, to)
	if err != nil {
		return summary, err
	}
	for rows.Next() {
		var severity string
		var count int64
		if err := rows.Scan(&severity, &count); err != nil {
			rows.Close()
			return summary, err
		}
		summary.Totals.BySeverity[severity] = count
		summary.Totals.Events += count
	}
	rows.Close()

	rows, err = p.pool.Query(ctx,
		`SELECT type, count(*) FROM events WHERE occurred_at >= $1 AND occurred_at <= $2 GROUP BY type`, from, to)
	if err != nil {
		return summary, err
	}
	for rows.Next() {
		var eventType string
		var count int64
		if err := rows.Scan(&eventType, &count); err != nil {
			rows.Close()
			return summary, err
		}
		summary.Totals.ByType[eventType] = count
	}
	rows.Close()

	rows, err = p.pool.Query(ctx, `
        SELECT e.camera_id, c.code, c.name, count(*) AS count
        FROM events e JOIN cameras c ON c.id = e.camera_id
        WHERE e.occurred_at >= $1 AND e.occurred_at <= $2
        GROUP BY e.camera_id, c.code, c.name ORDER BY count DESC LIMIT 20`, from, to)
	if err != nil {
		return summary, err
	}
	for rows.Next() {
		var stat model.CameraStat
		if err := rows.Scan(&stat.CameraID, &stat.Code, &stat.Name, &stat.Count); err != nil {
			rows.Close()
			return summary, err
		}
		summary.ByCamera = append(summary.ByCamera, stat)
	}
	rows.Close()

	rows, err = p.pool.Query(ctx, `
        SELECT date_trunc('hour', occurred_at) AS hour, count(*)
        FROM events WHERE occurred_at >= $1 AND occurred_at <= $2
        GROUP BY hour ORDER BY hour`, from, to)
	if err != nil {
		return summary, err
	}
	for rows.Next() {
		var stat model.HourStat
		if err := rows.Scan(&stat.Hour, &stat.Count); err != nil {
			rows.Close()
			return summary, err
		}
		summary.ByHour = append(summary.ByHour, stat)
	}
	rows.Close()
	return summary, nil
}

func (p *Postgres) Counts(ctx context.Context) (model.Counts, error) {
	var counts model.Counts
	err := p.pool.QueryRow(ctx, `
        SELECT (SELECT count(*) FROM events),
               (SELECT count(*) FROM cameras WHERE status='online')`).Scan(&counts.EventsTotal, &counts.OnlineCameras)
	return counts, err
}

const cameraSelect = `SELECT id, code, name, area_id, x, y, z, heading, fov_h, fov_v, range_m, status, stream_url, calibration, updated_at FROM cameras`

const eventSelect = `SELECT id, camera_id, camera_code, camera_name, type, severity, confidence,
                            x, y, z, occurred_at, status, closed_at, payload FROM events`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCamera(row rowScanner) (model.Camera, error) {
	var camera model.Camera
	var calibration []byte
	err := row.Scan(&camera.ID, &camera.Code, &camera.Name, &camera.AreaID, &camera.X, &camera.Y, &camera.Z,
		&camera.Heading, &camera.FovH, &camera.FovV, &camera.RangeM, &camera.Status, &camera.StreamURL,
		&calibration, &camera.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return model.Camera{}, ErrNotFound
		}
		return model.Camera{}, err
	}
	if len(calibration) > 0 {
		var parsed model.Calibration
		if err := json.Unmarshal(calibration, &parsed); err == nil {
			camera.Calibration = &parsed
		}
	}
	return camera, nil
}

func scanEvent(row rowScanner) (model.Event, error) {
	var event model.Event
	var payload []byte
	err := row.Scan(&event.ID, &event.CameraID, &event.CameraCode, &event.CameraName, &event.Type, &event.Severity,
		&event.Confidence, &event.X, &event.Y, &event.Z, &event.OccurredAt, &event.Status, &event.ClosedAt, &payload)
	if err != nil {
		if err == pgx.ErrNoRows {
			return model.Event{}, ErrNotFound
		}
		return model.Event{}, err
	}
	event.OccurredAt = event.OccurredAt.UTC()
	if event.ClosedAt != nil {
		closed := event.ClosedAt.UTC()
		event.ClosedAt = &closed
	}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &event.Payload)
	}
	return event, nil
}
