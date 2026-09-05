-- 1. Deduplicate existing PARENT users who share the same normalized passport
DO $$
DECLARE
    rec RECORD;
    primary_id INT;
    dup_id INT;
BEGIN
    -- Loop through each passport that has more than 1 active parent
    FOR rec IN 
        SELECT UPPER(TRIM(passport)) AS clean_passport
        FROM users u
        JOIN roles r ON u.role_id = r.id
        WHERE r.name = 'PARENT' 
          AND passport IS NOT NULL 
          AND TRIM(passport) != '' 
          AND TRIM(passport) != '-' 
          AND LOWER(TRIM(passport)) != 'yo''q'
          AND u.is_deleted = false
        GROUP BY UPPER(TRIM(passport))
        HAVING COUNT(*) > 1
    LOOP
        -- Choose the earliest created user as primary
        SELECT u.id INTO primary_id
        FROM users u
        JOIN roles r ON u.role_id = r.id
        WHERE r.name = 'PARENT' 
          AND UPPER(TRIM(u.passport)) = rec.clean_passport 
          AND u.is_deleted = false
        ORDER BY u.id ASC
        LIMIT 1;

        -- For each duplicate user of this passport
        FOR dup_id IN
            SELECT u.id
            FROM users u
            JOIN roles r ON u.role_id = r.id
            WHERE r.name = 'PARENT' 
              AND UPPER(TRIM(u.passport)) = rec.clean_passport 
              AND u.is_deleted = false
              AND u.id != primary_id
        LOOP
            -- If child is already linked to primary parent, delete redundant link from duplicate parent
            DELETE FROM student_parents 
            WHERE parent_id = dup_id 
              AND student_id IN (SELECT student_id FROM student_parents WHERE parent_id = primary_id);

            -- Re-link remaining children of duplicate parent to primary parent
            UPDATE student_parents 
            SET parent_id = primary_id 
            WHERE parent_id = dup_id;

            -- Also update menu comments or audit references if any
            UPDATE menu_comments SET parent_id = primary_id WHERE parent_id = dup_id;

            -- Mark duplicate parent as deleted and null out passport so unique index won't conflict
            UPDATE users 
            SET is_deleted = true, 
                deleted_at = NOW(), 
                passport = NULL, 
                document_no = NULL 
            WHERE id = dup_id;
        END LOOP;
    END LOOP;
END $$;

-- 2. Drop global unique constraint on users(phone) so parents can share phone numbers
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_phone_key;

-- 3 & 4. Create partial unique indexes using dynamic SQL (PostgreSQL disallows subqueries in index predicates)
DO $$
DECLARE
    parent_role_id INT;
    staff_ids TEXT;
BEGIN
    SELECT id INTO parent_role_id FROM roles WHERE name = 'PARENT';
    SELECT string_agg(id::text, ',') INTO staff_ids FROM roles WHERE name IN ('ADMIN', 'TEACHER', 'DIRECTOR', 'MAIN_TEACHER', 'SUBJECT_TEACHER');

    IF staff_ids IS NOT NULL AND staff_ids != '' THEN
        EXECUTE format('
            CREATE UNIQUE INDEX IF NOT EXISTS idx_staff_unique_phone 
            ON users(phone) 
            WHERE role_id IN (%s) 
              AND phone IS NOT NULL 
              AND TRIM(phone) != '''' 
              AND is_deleted = false', staff_ids);
    END IF;

    IF parent_role_id IS NOT NULL THEN
        EXECUTE format('
            CREATE UNIQUE INDEX IF NOT EXISTS idx_parents_unique_passport 
            ON users(UPPER(TRIM(passport))) 
            WHERE role_id = %s 
              AND passport IS NOT NULL 
              AND TRIM(passport) != '''' 
              AND is_deleted = false', parent_role_id);
    END IF;
END $$;

-- 5. Create student_transfer_requests table
CREATE TABLE IF NOT EXISTS student_transfer_requests (
    id SERIAL PRIMARY KEY,
    student_id INTEGER NOT NULL REFERENCES students(id),
    from_class_id INTEGER NOT NULL REFERENCES classes(id),
    to_class_id INTEGER NOT NULL REFERENCES classes(id),
    requested_by INTEGER NOT NULL REFERENCES users(id),
    target_teacher_id INTEGER REFERENCES users(id),
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
    message TEXT,
    reject_reason TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    responded_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_transfer_requests_target ON student_transfer_requests(target_teacher_id, status);
CREATE INDEX IF NOT EXISTS idx_transfer_requests_student ON student_transfer_requests(student_id, status);
CREATE INDEX IF NOT EXISTS idx_transfer_requests_from_class ON student_transfer_requests(from_class_id);
CREATE INDEX IF NOT EXISTS idx_transfer_requests_to_class ON student_transfer_requests(to_class_id);
