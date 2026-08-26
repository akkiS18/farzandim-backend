-- Down Migration: Drop enrollment_date column from students table
ALTER TABLE students DROP COLUMN IF EXISTS enrollment_date;
