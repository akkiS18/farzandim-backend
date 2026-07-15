-- Up Migration: Add grade_type, grade_category and lesson_number columns to grades table
ALTER TABLE grades ADD COLUMN IF NOT EXISTS grade_type VARCHAR(50) NOT NULL DEFAULT 'MASTERY';
ALTER TABLE grades ADD COLUMN IF NOT EXISTS grade_category VARCHAR(50) NOT NULL DEFAULT 'DAILY';
ALTER TABLE grades ADD COLUMN IF NOT EXISTS lesson_number INTEGER;
