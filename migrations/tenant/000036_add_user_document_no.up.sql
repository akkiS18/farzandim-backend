-- 1. Add document_no column to users table
ALTER TABLE users ADD COLUMN IF NOT EXISTS document_no VARCHAR(50);

-- 2. Create partial index for uppercase document_no lookup
CREATE INDEX IF NOT EXISTS idx_users_document_no ON users (UPPER(TRIM(document_no))) WHERE is_deleted = false AND document_no IS NOT NULL;

-- 3. Backfill passport column into document_no if available
UPDATE users SET document_no = UPPER(TRIM(passport)) WHERE passport IS NOT NULL AND passport <> '' AND (document_no IS NULL OR document_no = '');
