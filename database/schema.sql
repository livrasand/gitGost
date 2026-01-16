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
