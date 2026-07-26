-- Down Migration: Remove target_levels column from subjects
ALTER TABLE subjects DROP COLUMN IF EXISTS target_levels;
