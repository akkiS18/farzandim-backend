-- Down Migration: Remove target_levels and target_classes from school_holidays
ALTER TABLE school_holidays DROP COLUMN IF EXISTS target_levels;
ALTER TABLE school_holidays DROP COLUMN IF EXISTS target_classes;
