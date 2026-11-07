CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE IF NOT EXISTS areas (
    id        text PRIMARY KEY,
    name      text NOT NULL,
    type      text NOT NULL,
    polygon   jsonb NOT NULL DEFAULT '[]'::jsonb
);

CREATE TABLE IF NOT EXISTS cameras (
    id         text PRIMARY KEY,
    code       text UNIQUE NOT NULL,
    name       text NOT NULL,
    area_id    text NOT NULL DEFAULT '',
    x          double precision NOT NULL,
    y          double precision NOT NULL,
    z          double precision NOT NULL DEFAULT 0,
    heading    double precision NOT NULL DEFAULT 0,
    fov_h      double precision NOT NULL DEFAULT 60,
    fov_v      double precision NOT NULL DEFAULT 34,
    range_m    double precision NOT NULL DEFAULT 100,
    status     text NOT NULL DEFAULT 'online',
    stream_url text,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS events (
    id          text PRIMARY KEY,
    camera_id   text NOT NULL REFERENCES cameras(id),
    camera_code text NOT NULL,
    camera_name text NOT NULL,
    type        text NOT NULL,
    severity    text NOT NULL,
    confidence  double precision NOT NULL,
    x           double precision NOT NULL,
    y           double precision NOT NULL,
    z           double precision NOT NULL DEFAULT 0,
    occurred_at timestamptz NOT NULL,
    status      text NOT NULL DEFAULT 'new',
    payload     jsonb NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS events_occurred_at_idx ON events (occurred_at DESC);
CREATE INDEX IF NOT EXISTS events_status_idx ON events (status);
CREATE INDEX IF NOT EXISTS events_camera_idx ON events (camera_id);

INSERT INTO areas (id, name, type, polygon) VALUES
    ('area_building_a', '1号楼', 'building', '[[-70,20],[-10,20],[-10,60],[-70,60]]'::jsonb),
    ('area_building_b', '2号楼', 'building', '[[35,-35],[85,-35],[85,-5],[35,-5]]'::jsonb),
    ('area_gate',       '东门',   'zone',     '[[-120,-100],[-60,-100],[-60,-20],[-120,-20]]'::jsonb),
    ('area_parking',    '停车场', 'zone',     '[[10,-110],[70,-110],[70,-50],[10,-50]]'::jsonb)
ON CONFLICT (id) DO NOTHING;

INSERT INTO cameras (id, code, name, area_id, x, y, z, heading, fov_h, fov_v, range_m, status) VALUES
    ('cam_001', 'CAM-001', '东门入口', 'area_gate',      -90, -60, 6,  90, 60, 34, 120, 'online'),
    ('cam_002', 'CAM-002', '1号楼北侧','area_building_a', -40,  75, 8, 180, 55, 32, 100, 'online'),
    ('cam_003', 'CAM-003', '1号楼西侧','area_building_a', -80,  20, 8,  90, 55, 32, 100, 'online'),
    ('cam_004', 'CAM-004', '2号楼东侧','area_building_b',  95, -10, 7, 270, 55, 32,  90, 'degraded'),
    ('cam_005', 'CAM-005', '停车场',   'area_parking',      40, -80, 6,   0, 70, 40, 130, 'online'),
    ('cam_006', 'CAM-006', '主干道',   'area_gate',          0,   0, 9, 225, 65, 36, 150, 'online')
ON CONFLICT (id) DO NOTHING;
