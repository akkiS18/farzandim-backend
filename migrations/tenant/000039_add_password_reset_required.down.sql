-- Down Migration: Remove password_reset_required column from users table
ALTER TABLE users DROP COLUMN IF EXISTS password_reset_required;
