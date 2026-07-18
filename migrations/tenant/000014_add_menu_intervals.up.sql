-- Up Migration: Create menu_intervals table and link menu_cycles to it
CREATE TABLE IF NOT EXISTS menu_intervals (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    cycle_weeks INTEGER NOT NULL CHECK (cycle_weeks >= 1),
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Clean up old cycle template entries to avoid foreign key or constraint mismatch
TRUNCATE TABLE menu_cycles CASCADE;

-- Add interval_id to menu_cycles referencing menu_intervals
ALTER TABLE menu_cycles ADD COLUMN interval_id INTEGER NOT NULL REFERENCES menu_intervals(id) ON DELETE CASCADE;

-- Update uniqueness constraint to encompass interval_id
ALTER TABLE menu_cycles DROP CONSTRAINT IF EXISTS menu_cycles_week_number_day_of_week_key;
ALTER TABLE menu_cycles ADD CONSTRAINT menu_cycles_interval_week_day_unique UNIQUE (interval_id, week_number, day_of_week);
