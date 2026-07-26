-- Up Migration: Add target_levels column to subjects
ALTER TABLE subjects ADD COLUMN IF NOT EXISTS target_levels INT[];
