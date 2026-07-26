-- Migration 000020: Create charge_plan_history table for auditing charge plan edits
CREATE TABLE IF NOT EXISTS charge_plan_history (
    id SERIAL PRIMARY KEY,
    charge_plan_id INTEGER NOT NULL REFERENCES charge_plans(id) ON DELETE CASCADE,
    edited_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    edited_by_user_name VARCHAR(255),
    edited_at TIMESTAMP NOT NULL DEFAULT NOW(),
    old_state JSONB NOT NULL DEFAULT '{}'::jsonb,
    new_state JSONB NOT NULL DEFAULT '{}'::jsonb,
    change_summary TEXT
);

CREATE INDEX IF NOT EXISTS idx_charge_plan_history_plan_id ON charge_plan_history(charge_plan_id);
