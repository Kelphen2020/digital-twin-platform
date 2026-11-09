package store

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	if len(filter.CameraIDs) > 0 {
		add("camera_id = ANY($%d)", filter.CameraIDs)
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
	if len(filter.CameraIDs) > 0 {
		add("camera_id = ANY($%d)", filter.CameraIDs)
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
	if filter.From != nil {
		add("at >= $%d", *filter.From)
	}
	if filter.To != nil {
		add("at <= $%d", *filter.To)
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

func (p *Postgres) Stats(ctx context.Context, from, to time.Time, cameraIDs []string) (model.StatsSummary, error) {
	summary := model.StatsSummary{
		From:   from,
		To:     to,
		Totals: model.StatsTotals{BySeverity: map[string]int64{}, ByType: map[string]int64{}},
	}

	var filterArg []string
	if len(cameraIDs) > 0 {
		filterArg = cameraIDs
	}
	rows, err := p.pool.Query(ctx,
		`SELECT severity, count(*) FROM events
		 WHERE occurred_at >= $1 AND occurred_at <= $2 AND ($3::text[] IS NULL OR camera_id = ANY($3))
		 GROUP BY severity`, from, to, filterArg)
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
		`SELECT type, count(*) FROM events
		 WHERE occurred_at >= $1 AND occurred_at <= $2 AND ($3::text[] IS NULL OR camera_id = ANY($3))
		 GROUP BY type`, from, to, filterArg)
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
        WHERE e.occurred_at >= $1 AND e.occurred_at <= $2 AND ($3::text[] IS NULL OR e.camera_id = ANY($3))
        GROUP BY e.camera_id, c.code, c.name ORDER BY count DESC LIMIT 20`, from, to, filterArg)
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
        FROM events WHERE occurred_at >= $1 AND occurred_at <= $2 AND ($3::text[] IS NULL OR camera_id = ANY($3))
        GROUP BY hour ORDER BY hour`, from, to, filterArg)
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
                            x, y, z, occurred_at, status, closed_at, escalated_at, re_escalated_at, payload FROM events`

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
		&event.Confidence, &event.X, &event.Y, &event.Z, &event.OccurredAt, &event.Status, &event.ClosedAt,
		&event.EscalatedAt, &event.ReEscalatedAt, &payload)
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
	if event.EscalatedAt != nil {
		escalated := event.EscalatedAt.UTC()
		event.EscalatedAt = &escalated
	}
	if event.ReEscalatedAt != nil {
		stamp := event.ReEscalatedAt.UTC()
		event.ReEscalatedAt = &stamp
	}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &event.Payload)
	}
	return event, nil
}

func (p *Postgres) GetCameraRules(ctx context.Context, cameraID string) (model.CameraRules, error) {
	var rules model.CameraRules
	var classes, roi []byte
	err := p.pool.QueryRow(ctx, `
        SELECT camera_id, confidence_threshold, dedupe_seconds, enabled_classes, roi, updated_at
        FROM camera_rules WHERE camera_id=$1`, cameraID).
		Scan(&rules.CameraID, &rules.ConfidenceThreshold, &rules.DedupeSeconds, &classes, &roi, &rules.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return model.CameraRules{}, ErrNotFound
		}
		return model.CameraRules{}, err
	}
	if len(classes) > 0 {
		_ = json.Unmarshal(classes, &rules.EnabledClasses)
	}
	if len(roi) > 0 {
		_ = json.Unmarshal(roi, &rules.ROI)
	}
	return rules, nil
}

func (p *Postgres) SetCameraRules(ctx context.Context, rules model.CameraRules) (model.CameraRules, error) {
	classes, err := json.Marshal(rules.EnabledClasses)
	if err != nil {
		return model.CameraRules{}, err
	}
	var roi []byte
	if len(rules.ROI) > 0 {
		roi, err = json.Marshal(rules.ROI)
		if err != nil {
			return model.CameraRules{}, err
		}
	}
	_, err = p.pool.Exec(ctx, `
        INSERT INTO camera_rules (camera_id, confidence_threshold, dedupe_seconds, enabled_classes, roi, updated_at)
        VALUES ($1,$2,$3,$4,$5,now())
        ON CONFLICT (camera_id) DO UPDATE SET
            confidence_threshold=EXCLUDED.confidence_threshold,
            dedupe_seconds=EXCLUDED.dedupe_seconds,
            enabled_classes=EXCLUDED.enabled_classes,
            roi=EXCLUDED.roi,
            updated_at=now()`,
		rules.CameraID, rules.ConfidenceThreshold, rules.DedupeSeconds, classes, roi)
	if err != nil {
		return model.CameraRules{}, err
	}
	return p.GetCameraRules(ctx, rules.CameraID)
}

func (p *Postgres) CreateOrder(ctx context.Context, order model.Order) error {
	_, err := p.pool.Exec(ctx, `
        INSERT INTO orders (id, event_id, camera_id, title, status, priority, assignee, note, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		order.ID, order.EventID, order.CameraID, order.Title, order.Status, order.Priority,
		order.Assignee, order.Note, order.CreatedAt, order.UpdatedAt)
	return err
}

func (p *Postgres) GetOrder(ctx context.Context, id string) (model.Order, error) {
	return scanOrder(p.pool.QueryRow(ctx, orderSelect+` WHERE id=$1`, id))
}

func (p *Postgres) UpdateOrder(ctx context.Context, id string, update model.OrderUpdate) (model.Order, error) {
	current, err := p.GetOrder(ctx, id)
	if err != nil {
		return model.Order{}, err
	}
	now := time.Now().UTC()
	if update.Status != nil {
		current.Status = *update.Status
		if current.Status == "done" || current.Status == "cancelled" {
			closed := now
			current.ClosedAt = &closed
		}
	}
	if update.Assignee != nil {
		current.Assignee = *update.Assignee
	}
	if update.Note != nil {
		current.Note = *update.Note
	}
	_, err = p.pool.Exec(ctx, `
        UPDATE orders SET status=$2, assignee=$3, note=$4, updated_at=$5, closed_at=$6 WHERE id=$1`,
		id, current.Status, current.Assignee, current.Note, now, current.ClosedAt)
	if err != nil {
		return model.Order{}, err
	}
	return p.GetOrder(ctx, id)
}

func (p *Postgres) ListOrders(ctx context.Context, filter model.OrderFilter) ([]model.Order, error) {
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
	if filter.Status != "" {
		add("status = $%d", filter.Status)
	}
	if filter.Assignee != "" {
		add("assignee = $%d", filter.Assignee)
	}
	if filter.EventID != "" {
		add("event_id = $%d", filter.EventID)
	}
	if len(filter.CameraIDs) > 0 {
		add("camera_id = ANY($%d)", filter.CameraIDs)
	}
	query := orderSelect
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT %d", limit)
	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	orders := make([]model.Order, 0, limit)
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	return orders, rows.Err()
}

const orderSelect = `SELECT id, event_id, camera_id, title, status, priority, assignee, note, created_at, updated_at, closed_at FROM orders`

func scanOrder(row rowScanner) (model.Order, error) {
	var order model.Order
	err := row.Scan(&order.ID, &order.EventID, &order.CameraID, &order.Title, &order.Status, &order.Priority,
		&order.Assignee, &order.Note, &order.CreatedAt, &order.UpdatedAt, &order.ClosedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return model.Order{}, ErrNotFound
		}
		return model.Order{}, err
	}
	order.CreatedAt = order.CreatedAt.UTC()
	order.UpdatedAt = order.UpdatedAt.UTC()
	if order.ClosedAt != nil {
		closed := order.ClosedAt.UTC()
		order.ClosedAt = &closed
	}
	return order, nil
}

func (p *Postgres) GetSetting(ctx context.Context, key string) ([]byte, bool, error) {
	var value []byte
	err := p.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key=$1`, key).Scan(&value)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	return value, true, nil
}

func (p *Postgres) SetSetting(ctx context.Context, key string, value []byte) error {
	_, err := p.pool.Exec(ctx, `
        INSERT INTO settings (key, value, updated_at) VALUES ($1,$2,now())
        ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`, key, value)
	return err
}

func (p *Postgres) CreateNotificationLog(ctx context.Context, entry model.NotificationLog) error {
	_, err := p.pool.Exec(ctx, `
        INSERT INTO notification_log (id, at, kind, event_id, url, format, ok, status_code, error)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		entry.ID, entry.At, entry.Kind, entry.EventID, entry.URL, entry.Format,
		entry.OK, entry.StatusCode, entry.Error)
	return err
}

func (p *Postgres) ListNotificationLog(ctx context.Context, limit int) ([]model.NotificationLog, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := p.pool.Query(ctx, `
        SELECT id, at, kind, event_id, url, format, ok, status_code, error
        FROM notification_log ORDER BY at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.NotificationLog, 0, limit)
	for rows.Next() {
		var entry model.NotificationLog
		if err := rows.Scan(&entry.ID, &entry.At, &entry.Kind, &entry.EventID, &entry.URL,
			&entry.Format, &entry.OK, &entry.StatusCode, &entry.Error); err != nil {
			return nil, err
		}
		entry.At = entry.At.UTC()
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (p *Postgres) EscalateStale(ctx context.Context, before time.Time, severities []string) ([]model.Event, error) {
	if len(severities) == 0 {
		return nil, nil
	}
	rows, err := p.pool.Query(ctx, `
        UPDATE events SET escalated_at = now()
        WHERE status <> 'closed' AND escalated_at IS NULL
          AND occurred_at < $1 AND severity = ANY($2)
        RETURNING id, camera_id, camera_code, camera_name, type, severity, confidence,
                  x, y, z, occurred_at, status, closed_at, escalated_at, payload`, before, severities)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]model.Event, 0, 8)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (p *Postgres) CreateDutyGroup(ctx context.Context, group model.DutyGroup) error {
	areas, _ := json.Marshal(group.Areas)
	operators, _ := json.Marshal(group.Operators)
	shifts, _ := json.Marshal(group.Shifts)
	_, err := p.pool.Exec(ctx, `
        INSERT INTO duty_groups (id, name, areas, operators, rotation, shifts, active, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		group.ID, group.Name, areas, operators, group.Rotation, shifts, group.Active, group.CreatedAt, group.UpdatedAt)
	return err
}

func (p *Postgres) GetDutyGroup(ctx context.Context, id string) (model.DutyGroup, error) {
	return scanDutyGroup(p.pool.QueryRow(ctx, dutySelect+` WHERE id=$1`, id))
}

func (p *Postgres) ListDutyGroups(ctx context.Context) ([]model.DutyGroup, error) {
	rows, err := p.pool.Query(ctx, dutySelect+` ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]model.DutyGroup, 0, 4)
	for rows.Next() {
		group, err := scanDutyGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func (p *Postgres) UpdateDutyGroup(ctx context.Context, id string, group model.DutyGroup) (model.DutyGroup, error) {
	areas, _ := json.Marshal(group.Areas)
	operators, _ := json.Marshal(group.Operators)
	shifts, _ := json.Marshal(group.Shifts)
	_, err := p.pool.Exec(ctx, `
        UPDATE duty_groups SET name=$2, areas=$3, operators=$4, rotation=$5, shifts=$6, active=$7, updated_at=now()
        WHERE id=$1`, id, group.Name, areas, operators, group.Rotation, shifts, group.Active)
	if err != nil {
		return model.DutyGroup{}, err
	}
	return p.GetDutyGroup(ctx, id)
}

func (p *Postgres) DeleteDutyGroup(ctx context.Context, id string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM duty_groups WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const dutySelect = `SELECT id, name, areas, operators, rotation, shifts, active, created_at, updated_at FROM duty_groups`

type dutyScanner interface {
	Scan(dest ...any) error
}

func scanDutyGroup(row dutyScanner) (model.DutyGroup, error) {
	var group model.DutyGroup
	var areas, operators, shifts []byte
	err := row.Scan(&group.ID, &group.Name, &areas, &operators, &group.Rotation, &shifts, &group.Active, &group.CreatedAt, &group.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return model.DutyGroup{}, ErrNotFound
		}
		return model.DutyGroup{}, err
	}
	if len(areas) > 0 {
		_ = json.Unmarshal(areas, &group.Areas)
	}
	if len(operators) > 0 {
		_ = json.Unmarshal(operators, &group.Operators)
	}
	if len(shifts) > 0 {
		_ = json.Unmarshal(shifts, &group.Shifts)
	}
	group.CreatedAt = group.CreatedAt.UTC()
	group.UpdatedAt = group.UpdatedAt.UTC()
	return group, nil
}

func (p *Postgres) GetUserAccountByUsername(ctx context.Context, username string) (model.UserAccount, bool, error) {
	account, err := scanUserAccount(p.pool.QueryRow(ctx, userSelect+` WHERE username=$1`, username))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return model.UserAccount{}, false, nil
		}
		return model.UserAccount{}, false, err
	}
	return account, true, nil
}

func (p *Postgres) GetUserAccountByID(ctx context.Context, id string) (model.UserAccount, bool, error) {
	account, err := scanUserAccount(p.pool.QueryRow(ctx, userSelect+` WHERE id=$1`, id))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return model.UserAccount{}, false, nil
		}
		return model.UserAccount{}, false, err
	}
	return account, true, nil
}

func (p *Postgres) ListUserAccounts(ctx context.Context) ([]model.UserAccount, error) {
	rows, err := p.pool.Query(ctx, userSelect+` ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := make([]model.UserAccount, 0, 8)
	for rows.Next() {
		account, err := scanUserAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func (p *Postgres) CreateUserAccount(ctx context.Context, account model.UserAccount) error {
	areas, _ := json.Marshal(account.Areas)
	_, err := p.pool.Exec(ctx, `
        INSERT INTO users (id, username, display_name, role, areas, active, password_hash, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		account.ID, account.Username, account.DisplayName, account.Role, areas, account.Active,
		account.PasswordHash, account.CreatedAt, account.UpdatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return model.ErrConflict
	}
	return err
}

func (p *Postgres) UpdateUserAccount(ctx context.Context, account model.UserAccount) error {
	areas, _ := json.Marshal(account.Areas)
	tag, err := p.pool.Exec(ctx, `
        UPDATE users SET username=$2, display_name=$3, role=$4, areas=$5, active=$6,
                         password_hash=$7, updated_at=now()
        WHERE id=$1`,
		account.ID, account.Username, account.DisplayName, account.Role, areas,
		account.Active, account.PasswordHash)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return model.ErrConflict
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) DeleteUserAccount(ctx context.Context, id string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const userSelect = `SELECT id, username, display_name, role, areas, active, password_hash, created_at, updated_at FROM users`

func scanUserAccount(row rowScanner) (model.UserAccount, error) {
	var account model.UserAccount
	var areas []byte
	err := row.Scan(&account.ID, &account.Username, &account.DisplayName, &account.Role,
		&areas, &account.Active, &account.PasswordHash, &account.CreatedAt, &account.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return model.UserAccount{}, ErrNotFound
		}
		return model.UserAccount{}, err
	}
	if len(areas) > 0 {
		_ = json.Unmarshal(areas, &account.Areas)
	}
	account.CreatedAt = account.CreatedAt.UTC()
	account.UpdatedAt = account.UpdatedAt.UTC()
	return account, nil
}

func (p *Postgres) FindOpenOrderForEvent(ctx context.Context, eventID string) (model.Order, bool, error) {
	order, err := scanOrder(p.pool.QueryRow(ctx, orderSelect+`
        WHERE event_id=$1 AND status IN ('open','assigned','processing')
        ORDER BY created_at DESC LIMIT 1`, eventID))
	if err != nil {
		if err == ErrNotFound {
			return model.Order{}, false, nil
		}
		return model.Order{}, false, err
	}
	return order, true, nil
}

func (p *Postgres) ReEscalateStale(ctx context.Context, before time.Time, severities []string) ([]model.Event, error) {
	if len(severities) == 0 {
		return nil, nil
	}
	rows, err := p.pool.Query(ctx, `
        UPDATE events SET re_escalated_at = now()
        WHERE status <> 'closed' AND escalated_at IS NOT NULL AND re_escalated_at IS NULL
          AND escalated_at < $1 AND severity = ANY($2)
        RETURNING id, camera_id, camera_code, camera_name, type, severity, confidence,
                  x, y, z, occurred_at, status, closed_at, escalated_at, re_escalated_at, payload`, before, severities)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]model.Event, 0, 4)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (p *Postgres) CreateRefreshToken(ctx context.Context, token model.RefreshToken) error {
	_, err := p.pool.Exec(ctx, `
        INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at, created_at, revoked, ip, user_agent)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		token.ID, token.UserID, token.TokenHash, token.ExpiresAt, token.CreatedAt,
		token.Revoked, token.IP, token.UserAgent)
	return err
}

func (p *Postgres) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (model.RefreshToken, bool, error) {
	token, err := scanRefreshToken(p.pool.QueryRow(ctx, refreshSelect+` WHERE token_hash=$1`, tokenHash))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return model.RefreshToken{}, false, nil
		}
		return model.RefreshToken{}, false, err
	}
	return token, true, nil
}

func (p *Postgres) RevokeRefreshToken(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `UPDATE refresh_tokens SET revoked=true WHERE id=$1`, id)
	return err
}

func (p *Postgres) RevokeUserRefreshTokens(ctx context.Context, userID string) error {
	_, err := p.pool.Exec(ctx, `UPDATE refresh_tokens SET revoked=true WHERE user_id=$1`, userID)
	return err
}

func (p *Postgres) ListUserRefreshTokens(ctx context.Context, userID string) ([]model.RefreshToken, error) {
	rows, err := p.pool.Query(ctx, refreshSelect+` WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tokens := make([]model.RefreshToken, 0, 4)
	for rows.Next() {
		token, err := scanRefreshToken(rows)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

const refreshSelect = `SELECT id, user_id, token_hash, expires_at, created_at, revoked, ip, user_agent FROM refresh_tokens`

func scanRefreshToken(row rowScanner) (model.RefreshToken, error) {
	var token model.RefreshToken
	err := row.Scan(&token.ID, &token.UserID, &token.TokenHash, &token.ExpiresAt,
		&token.CreatedAt, &token.Revoked, &token.IP, &token.UserAgent)
	if err != nil {
		if err == pgx.ErrNoRows {
			return model.RefreshToken{}, ErrNotFound
		}
		return model.RefreshToken{}, err
	}
	token.ExpiresAt = token.ExpiresAt.UTC()
	token.CreatedAt = token.CreatedAt.UTC()
	return token, nil
}

func (p *Postgres) CreatePatrolRoute(ctx context.Context, route model.PatrolRoute) error {
	waypoints, _ := json.Marshal(route.Waypoints)
	cameras, _ := json.Marshal(route.CameraIDs)
	_, err := p.pool.Exec(ctx, `
        INSERT INTO patrol_routes (id, name, waypoints, camera_ids, interval_seconds, created_by, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		route.ID, route.Name, waypoints, cameras, route.IntervalSeconds, route.CreatedBy,
		route.CreatedAt, route.UpdatedAt)
	return err
}

func (p *Postgres) GetPatrolRoute(ctx context.Context, id string) (model.PatrolRoute, bool, error) {
	route, err := scanPatrolRoute(p.pool.QueryRow(ctx, patrolSelect+` WHERE id=$1`, id))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return model.PatrolRoute{}, false, nil
		}
		return model.PatrolRoute{}, false, err
	}
	return route, true, nil
}

func (p *Postgres) ListPatrolRoutes(ctx context.Context) ([]model.PatrolRoute, error) {
	rows, err := p.pool.Query(ctx, patrolSelect+` ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	routes := make([]model.PatrolRoute, 0, 4)
	for rows.Next() {
		route, err := scanPatrolRoute(rows)
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	return routes, rows.Err()
}

func (p *Postgres) UpdatePatrolRoute(ctx context.Context, route model.PatrolRoute) error {
	waypoints, _ := json.Marshal(route.Waypoints)
	cameras, _ := json.Marshal(route.CameraIDs)
	tag, err := p.pool.Exec(ctx, `
        UPDATE patrol_routes SET name=$2, waypoints=$3, camera_ids=$4, interval_seconds=$5, updated_at=now()
        WHERE id=$1`, route.ID, route.Name, waypoints, cameras, route.IntervalSeconds)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) DeletePatrolRoute(ctx context.Context, id string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM patrol_routes WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const patrolSelect = `SELECT id, name, waypoints, camera_ids, interval_seconds, created_by, created_at, updated_at FROM patrol_routes`

func scanPatrolRoute(row rowScanner) (model.PatrolRoute, error) {
	var route model.PatrolRoute
	var waypoints, cameras []byte
	err := row.Scan(&route.ID, &route.Name, &waypoints, &cameras, &route.IntervalSeconds,
		&route.CreatedBy, &route.CreatedAt, &route.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return model.PatrolRoute{}, ErrNotFound
		}
		return model.PatrolRoute{}, err
	}
	if len(waypoints) > 0 {
		_ = json.Unmarshal(waypoints, &route.Waypoints)
	}
	if len(cameras) > 0 {
		_ = json.Unmarshal(cameras, &route.CameraIDs)
	}
	route.CreatedAt = route.CreatedAt.UTC()
	route.UpdatedAt = route.UpdatedAt.UTC()
	return route, nil
}

func (p *Postgres) CreatePatrolSchedule(ctx context.Context, schedule model.PatrolSchedule) error {
	_, err := p.pool.Exec(ctx, `
        INSERT INTO patrol_schedules (id, name, route_id, interval_minutes, enabled, created_by, created_at, updated_at, next_run_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		schedule.ID, schedule.Name, schedule.RouteID, schedule.IntervalMinutes, schedule.Enabled,
		schedule.CreatedBy, schedule.CreatedAt, schedule.UpdatedAt, schedule.NextRunAt)
	return err
}

func (p *Postgres) GetPatrolSchedule(ctx context.Context, id string) (model.PatrolSchedule, bool, error) {
	schedule, err := scanPatrolSchedule(p.pool.QueryRow(ctx, patrolScheduleSelect+` WHERE id=$1`, id))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return model.PatrolSchedule{}, false, nil
		}
		return model.PatrolSchedule{}, false, err
	}
	return schedule, true, nil
}

func (p *Postgres) ListPatrolSchedules(ctx context.Context) ([]model.PatrolSchedule, error) {
	rows, err := p.pool.Query(ctx, patrolScheduleSelect+` ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	schedules := make([]model.PatrolSchedule, 0, 4)
	for rows.Next() {
		schedule, err := scanPatrolSchedule(rows)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, schedule)
	}
	return schedules, rows.Err()
}

func (p *Postgres) UpdatePatrolSchedule(ctx context.Context, schedule model.PatrolSchedule) error {
	tag, err := p.pool.Exec(ctx, `
        UPDATE patrol_schedules SET name=$2, route_id=$3, interval_minutes=$4, enabled=$5, updated_at=now(), next_run_at=$6
        WHERE id=$1`, schedule.ID, schedule.Name, schedule.RouteID, schedule.IntervalMinutes, schedule.Enabled, schedule.NextRunAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) DeletePatrolSchedule(ctx context.Context, id string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM patrol_schedules WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) CreatePatrolExecution(ctx context.Context, execution model.PatrolExecution) error {
	_, err := p.pool.Exec(ctx, `
        INSERT INTO patrol_executions (id, schedule_id, route_id, route_name, status, assignee, result, note, order_id, scheduled_at, created_at, started_at, finished_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		execution.ID, execution.ScheduleID, execution.RouteID, execution.RouteName, execution.Status,
		execution.Assignee, execution.Result, execution.Note, execution.OrderID,
		execution.ScheduledAt, execution.CreatedAt, execution.StartedAt, execution.FinishedAt)
	return err
}

func (p *Postgres) GetPatrolExecution(ctx context.Context, id string) (model.PatrolExecution, bool, error) {
	execution, err := scanPatrolExecution(p.pool.QueryRow(ctx, patrolExecutionSelect+` WHERE id=$1`, id))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return model.PatrolExecution{}, false, nil
		}
		return model.PatrolExecution{}, false, err
	}
	return execution, true, nil
}

func (p *Postgres) ListPatrolExecutions(ctx context.Context, filter model.PatrolExecutionFilter) ([]model.PatrolExecution, error) {
	where := " WHERE 1=1"
	args := make([]any, 0, 3)
	if filter.Status != "" {
		args = append(args, filter.Status)
		where += fmt.Sprintf(" AND status=$%d", len(args))
	}
	if filter.ScheduleID != "" {
		args = append(args, filter.ScheduleID)
		where += fmt.Sprintf(" AND schedule_id=$%d", len(args))
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	args = append(args, limit)
	rows, err := p.pool.Query(ctx, patrolExecutionSelect+where+fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	executions := make([]model.PatrolExecution, 0, limit)
	for rows.Next() {
		execution, err := scanPatrolExecution(rows)
		if err != nil {
			return nil, err
		}
		executions = append(executions, execution)
	}
	return executions, rows.Err()
}

func (p *Postgres) UpdatePatrolExecution(ctx context.Context, execution model.PatrolExecution) error {
	tag, err := p.pool.Exec(ctx, `
        UPDATE patrol_executions SET status=$2, assignee=$3, result=$4, note=$5, order_id=$6, started_at=$7, finished_at=$8
        WHERE id=$1`, execution.ID, execution.Status, execution.Assignee, execution.Result,
		execution.Note, execution.OrderID, execution.StartedAt, execution.FinishedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const patrolScheduleSelect = `SELECT id, name, route_id, interval_minutes, enabled, created_by, created_at, updated_at, next_run_at FROM patrol_schedules`

func scanPatrolSchedule(row rowScanner) (model.PatrolSchedule, error) {
	var schedule model.PatrolSchedule
	err := row.Scan(&schedule.ID, &schedule.Name, &schedule.RouteID, &schedule.IntervalMinutes,
		&schedule.Enabled, &schedule.CreatedBy, &schedule.CreatedAt, &schedule.UpdatedAt, &schedule.NextRunAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return model.PatrolSchedule{}, ErrNotFound
		}
		return model.PatrolSchedule{}, err
	}
	schedule.CreatedAt = schedule.CreatedAt.UTC()
	schedule.UpdatedAt = schedule.UpdatedAt.UTC()
	schedule.NextRunAt = schedule.NextRunAt.UTC()
	return schedule, nil
}

const patrolExecutionSelect = `SELECT id, schedule_id, route_id, route_name, status, assignee, result, note, order_id, scheduled_at, created_at, started_at, finished_at FROM patrol_executions`

func scanPatrolExecution(row rowScanner) (model.PatrolExecution, error) {
	var execution model.PatrolExecution
	err := row.Scan(&execution.ID, &execution.ScheduleID, &execution.RouteID, &execution.RouteName,
		&execution.Status, &execution.Assignee, &execution.Result, &execution.Note, &execution.OrderID,
		&execution.ScheduledAt, &execution.CreatedAt, &execution.StartedAt, &execution.FinishedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return model.PatrolExecution{}, ErrNotFound
		}
		return model.PatrolExecution{}, err
	}
	execution.ScheduledAt = execution.ScheduledAt.UTC()
	execution.CreatedAt = execution.CreatedAt.UTC()
	if execution.StartedAt != nil {
		stamp := execution.StartedAt.UTC()
		execution.StartedAt = &stamp
	}
	if execution.FinishedAt != nil {
		stamp := execution.FinishedAt.UTC()
		execution.FinishedAt = &stamp
	}
	return execution, nil
}
