CREATE TABLE IF NOT EXISTS ai_weekly_reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    student_id INT NOT NULL REFERENCES students(id) ON DELETE CASCADE,
    year INT NOT NULL,
    week_number INT NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    report_text TEXT NOT NULL,
    summary_json JSONB DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    CONSTRAINT uk_ai_report_student_year_week UNIQUE (student_id, year, week_number)
);

CREATE INDEX IF NOT EXISTS idx_ai_weekly_reports_student ON ai_weekly_reports(student_id);
CREATE INDEX IF NOT EXISTS idx_ai_weekly_reports_year_week ON ai_weekly_reports(year, week_number);
