-- P31: shared per-tenant ingest token buckets for multi-replica deployments.
CREATE TABLE IF NOT EXISTS tenant_rate_buckets (
    tenant_id  text PRIMARY KEY,
    tokens     double precision NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
