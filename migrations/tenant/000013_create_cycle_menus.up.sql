-- Up Migration: Create menu_cycles and menu_exceptions tables
CREATE TABLE IF NOT EXISTS menu_cycles (
    id SERIAL PRIMARY KEY,
    week_number INTEGER NOT NULL CHECK (week_number >= 1),
    day_of_week INTEGER NOT NULL CHECK (day_of_week BETWEEN 1 AND 6),
    meals JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE (week_number, day_of_week)
);

CREATE TABLE IF NOT EXISTS menu_exceptions (
    id SERIAL PRIMARY KEY,
    menu_date DATE UNIQUE NOT NULL,
    meals JSONB, -- NULL means no meals / holiday
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);
