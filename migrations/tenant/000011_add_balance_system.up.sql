-- Up Migration: Add balance column and create payment_transactions table
ALTER TABLE students ADD COLUMN IF NOT EXISTS balance NUMERIC(12, 2) NOT NULL DEFAULT 0.00;

CREATE TABLE IF NOT EXISTS payment_transactions (
    id SERIAL PRIMARY KEY,
    student_id INTEGER NOT NULL REFERENCES students(id),
    amount NUMERIC(12, 2) NOT NULL,
    type VARCHAR(20) NOT NULL, -- 'PAYMENT' or 'CHARGE'
    description TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);
