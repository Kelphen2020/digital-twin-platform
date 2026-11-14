package store

import "github.com/jackc/pgx/v5/pgxpool"

// Pool exposes the underlying connection pool for integration tests that need
// to arrange state (e.g. aging a heartbeat). It is intentionally not part of the
// Store interface.
func (p *Postgres) Pool() *pgxpool.Pool { return p.pool }
