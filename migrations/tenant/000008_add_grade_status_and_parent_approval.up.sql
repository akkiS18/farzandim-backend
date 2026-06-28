ALTER TABLE grades ADD COLUMN status VARCHAR(50) NOT NULL DEFAULT 'marked';
ALTER TABLE grades ADD COLUMN approved_by_parent BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE grades ADD CONSTRAINT chk_grade_status CHECK (status IN ('marked', 'approved'));
