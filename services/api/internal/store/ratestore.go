package store

import (
	"context"
	"time"
)

// RateStore is the cross-replica token source used by tenant ingest quotas
// (P31.0). Implementations must be safe for concurrent use.
type RateStore interface {
	// Take consumes one token for the tenant, refilling by elapsed time up to
	// burst. It returns false when the bucket is empty.
	Take(ctx context.Context, tenantID string, ratePerSecond, burst int) (bool, error)
}

// NewPostgresRateStore returns a rate store backed by tenant_rate_buckets.
func NewPostgresRateStore(p *Postgres) RateStore { return &pgRateStore{pg: p} }

type pgRateStore struct {
	pg *Postgres
}

func (s *pgRateStore) Take(ctx context.Context, tenantID string, ratePerSecond, burst int) (bool, error) {
	if ratePerSecond <= 0 {
		return true, nil
	}
	if burst <= 0 {
		burst = ratePerSecond
	}
	now := time.Now().UTC()
	var tokens float64
	var updated time.Time
	err := s.pg.pool.QueryRow(ctx,
		`SELECT tokens, updated_at FROM tenant_rate_buckets WHERE tenant_id=$1 FOR UPDATE`,
		tenantID).Scan(&tokens, &updated)
	if err != nil {
		// No row yet: create one with a full bucket minus the consumed token.
		_, insertErr := s.pg.pool.Exec(ctx,
			`INSERT INTO tenant_rate_buckets (tenant_id, tokens, updated_at) VALUES ($1,$2,$3)
             ON CONFLICT (tenant_id) DO NOTHING`, tenantID, float64(burst-1), now)
		if insertErr != nil {
			return false, insertErr
		}
		return true, nil
	}
	elapsed := now.Sub(updated).Seconds()
	if elapsed > 0 {
		tokens += elapsed * float64(ratePerSecond)
		if tokens > float64(burst) {
			tokens = float64(burst)
		}
	}
	allowed := tokens >= 1
	if allowed {
		tokens--
	}
	_, err = s.pg.pool.Exec(ctx,
		`UPDATE tenant_rate_buckets SET tokens=$2, updated_at=$3 WHERE tenant_id=$1`,
		tenantID, tokens, now)
	return allowed, err
}
