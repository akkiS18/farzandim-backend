ALTER TABLE users ADD COLUMN telegram_id VARCHAR(50) UNIQUE;

CREATE TABLE announcements (
    id SERIAL PRIMARY KEY,
    title VARCHAR(255) NOT NULL,
    content TEXT NOT NULL,
    author_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    is_deleted BOOLEAN DEFAULT FALSE,
    deleted_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE announcement_classes (
    announcement_id INT NOT NULL REFERENCES announcements(id) ON DELETE CASCADE,
    class_id INT NOT NULL REFERENCES classes(id) ON DELETE CASCADE,
    PRIMARY KEY (announcement_id, class_id)
);

CREATE TABLE announcement_levels (
    announcement_id INT NOT NULL REFERENCES announcements(id) ON DELETE CASCADE,
    level INT NOT NULL,
    PRIMARY KEY (announcement_id, level)
);

CREATE TABLE announcement_students (
    announcement_id INT NOT NULL REFERENCES announcements(id) ON DELETE CASCADE,
    student_id INT NOT NULL REFERENCES students(id) ON DELETE CASCADE,
    PRIMARY KEY (announcement_id, student_id)
);
