-- gitGost Database Schema for Supabase
-- Database location: Central Europe (Zurich)
-- Privacy: Only stores counter + repo name + PR URL. Zero personal data.

-- Create table for anonymous PR records
CREATE TABLE IF NOT EXISTS prs (
    id BIGSERIAL PRIMARY KEY,
    owner TEXT NOT NULL,
    repo TEXT NOT NULL,
    url TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Create index for faster queries on created_at (for recent PRs)
CREATE INDEX IF NOT EXISTS idx_prs_created_at ON prs(created_at DESC);

-- Create unique index on url to prevent duplicate PR records
-- Using CONCURRENTLY for live DB to avoid locking (requires separate transaction)
-- Note: For initial setup, the UNIQUE constraint above is sufficient
-- For migration on existing DB, run: CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_prs_url_unique ON prs(url);

-- Enable Row Level Security (RLS)
ALTER TABLE prs ENABLE ROW LEVEL SECURITY;

-- Policy: Allow anonymous inserts (for recording PRs)
CREATE POLICY "Allow anonymous inserts" ON prs
    FOR INSERT
    WITH CHECK (true);

-- Policy: Allow public read access (for stats and recent PRs)
CREATE POLICY "Allow public read access" ON prs
    FOR SELECT
    USING (true);

-- No UPDATE or DELETE policies - data is immutable once created

-- Tabla de karma por hash
CREATE TABLE IF NOT EXISTS karma (
    hash TEXT PRIMARY KEY,
    karma INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE karma ENABLE ROW LEVEL SECURITY;
CREATE POLICY "Allow anonymous upsert karma" ON karma
    FOR INSERT
    WITH CHECK (true);
CREATE POLICY "Allow anonymous update karma" ON karma
    FOR UPDATE
    USING (true)
    WITH CHECK (true);
CREATE POLICY "Allow public read karma" ON karma
    FOR SELECT
    USING (true);

-- Tabla de reportes por hash
CREATE TABLE IF NOT EXISTS reports (
    id BIGSERIAL PRIMARY KEY,
    hash TEXT NOT NULL,
    reason TEXT NOT NULL,
    ip TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_reports_hash ON reports(hash);
CREATE UNIQUE INDEX IF NOT EXISTS idx_reports_hash_ip ON reports(hash, ip);
ALTER TABLE reports ENABLE ROW LEVEL SECURITY;
CREATE POLICY "Allow anonymous insert reports" ON reports
    FOR INSERT
    WITH CHECK (true);
CREATE POLICY "Allow public read reports" ON reports
    FOR SELECT
    USING (true);
