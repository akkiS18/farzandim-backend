-- Add level column to classes table
ALTER TABLE classes ADD COLUMN IF NOT EXISTS level INTEGER NOT NULL DEFAULT 1 CHECK (level >= 0 AND level <= 13);

-- Create charge_plans table
CREATE TABLE IF NOT EXISTS charge_plans (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    amount NUMERIC(12, 2) NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    charge_day INTEGER NOT NULL CHECK (charge_day >= 1 AND charge_day <= 31),
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Create charge_plan_levels table (many-to-many relationship)
CREATE TABLE IF NOT EXISTS charge_plan_levels (
    charge_plan_id INTEGER NOT NULL REFERENCES charge_plans(id) ON DELETE CASCADE,
    level INTEGER NOT NULL,
    PRIMARY KEY (charge_plan_id, level)
);

-- Create charge_plan_classes table (many-to-many relationship)
CREATE TABLE IF NOT EXISTS charge_plan_classes (
    charge_plan_id INTEGER NOT NULL REFERENCES charge_plans(id) ON DELETE CASCADE,
    class_id INTEGER NOT NULL REFERENCES classes(id) ON DELETE CASCADE,
    PRIMARY KEY (charge_plan_id, class_id)
);

-- Create charge_plan_students table (many-to-many relationship)
CREATE TABLE IF NOT EXISTS charge_plan_students (
    charge_plan_id INTEGER NOT NULL REFERENCES charge_plans(id) ON DELETE CASCADE,
    student_id INTEGER NOT NULL REFERENCES students(id) ON DELETE CASCADE,
    PRIMARY KEY (charge_plan_id, student_id)
);

-- Create charge_logs table to track applied monthly charges per student per plan
CREATE TABLE IF NOT EXISTS charge_logs (
    id SERIAL PRIMARY KEY,
    charge_plan_id INTEGER NOT NULL REFERENCES charge_plans(id) ON DELETE CASCADE,
    student_id INTEGER NOT NULL REFERENCES students(id) ON DELETE CASCADE,
    billing_month DATE NOT NULL,
    transaction_id INTEGER REFERENCES payment_transactions(id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE (charge_plan_id, student_id, billing_month)
);
