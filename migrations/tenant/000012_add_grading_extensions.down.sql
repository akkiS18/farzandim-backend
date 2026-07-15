-- Down Migration: Remove grade_type, grade_category and lesson_number columns from grades table
ALTER TABLE grades DROP COLUMN IF EXISTS lesson_number;
ALTER TABLE grades DROP COLUMN IF EXISTS grade_category;
ALTER TABLE grades DROP COLUMN IF EXISTS grade_type;
