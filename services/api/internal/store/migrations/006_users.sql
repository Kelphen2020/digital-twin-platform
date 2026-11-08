CREATE TABLE IF NOT EXISTS users (
    id            text PRIMARY KEY,
    username      text UNIQUE NOT NULL,
    display_name  text NOT NULL DEFAULT '',
    role          text NOT NULL,
    areas         jsonb NOT NULL DEFAULT '[]'::jsonb,
    active        boolean NOT NULL DEFAULT true,
    password_hash text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS users_username_idx ON users (username);
