-- Up Migration: Add target_levels and target_classes to school_holidays
ALTER TABLE school_holidays ADD COLUMN IF NOT EXISTS target_levels INT[];
ALTER TABLE school_holidays ADD COLUMN IF NOT EXISTS target_classes INT[];
