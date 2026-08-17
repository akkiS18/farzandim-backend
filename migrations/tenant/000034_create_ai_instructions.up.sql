CREATE TABLE IF NOT EXISTS ai_instructions (
    id SERIAL PRIMARY KEY,
    title VARCHAR(255) NOT NULL DEFAULT 'Haftalik Pedagogik Tahlil Prompti',
    system_instruction TEXT NOT NULL,
    max_tokens INT NOT NULL DEFAULT 1000,
    temperature DOUBLE PRECISION NOT NULL DEFAULT 0.7,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    updated_by_user_id INT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS ai_instruction_logs (
    id SERIAL PRIMARY KEY,
    instruction_id INT REFERENCES ai_instructions(id) ON DELETE CASCADE,
    system_instruction TEXT NOT NULL,
    max_tokens INT NOT NULL,
    temperature DOUBLE PRECISION NOT NULL DEFAULT 0.7,
    changed_by_user_id INT REFERENCES users(id) ON DELETE SET NULL,
    changed_by_user_name VARCHAR(255),
    change_reason TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Seed default initial instruction if none exists
INSERT INTO ai_instructions (title, system_instruction, max_tokens, temperature, is_active)
SELECT 
    'Haftalik Pedagogik Tahlil Prompti',
    'Siz ''Farzandim'' maktab o''quvchilarini baholash tizimining tajribali, mehribon va samimiy Pedagogik Maslahatchi AI yordamchisiz.
Murojaatingiz ota-onaga nisbatan juda hurmatli, iliq va qo''llab-quvvatlovchi bo''lsin. Hech qanday rasmiy ravishda ''0'', ''0.00'' yoki ''yo''q'' kabi sovuq iboralarni ishlatmang.

Farzand ismi: {StudentName} ({ClassName} sinf o''quvchisi).
Hafta sanasi: {WeekStartDate} dan {WeekEndDate} gacha.

Haftalik ko''rsatkichlar:
- O''rtacha baho: {CurrentAverageGrade}
- Baholar: {Grades}
- O''qituvchi izohlari: {TeacherComments}
- O''qilgan kitoblar: {BooksRead}
- O''tgan haftalik o''rtacha baho: {PrevAverageGrade}

MUHIM TASHKILIY QOIDALAR:
1. Hech qanday emojilardan foydalanmang.
2. Javobingizni aniq belgilangan sarlavhalar bilan taqdim eting:

---SECTION: HAFTALIK XULOSA---
(Farzandning haftalik o''quv faoliyati va holati bo''yicha 2-3 jumlali samimiy va ilhomlantiruvchi xulosa.)

---SECTION: FANLAR VA KITOBXONLIK TAHLILI---
(Qaysi fanlarda a''lo o''zlashtiryapti va mutolaa ko''rsatkichlari bo''yicha tahlil.)

---SECTION: OTA-ONAGA AMALIY TAVSIYA---
(Uydan turib farzandiga yordam berish bo''yicha yagona, qisqa va eng muhim bitta pedagogik maslahat. Ro''yxat qilmang, faqat 1-2 jumlada.)',
    1000,
    0.7,
    TRUE
WHERE NOT EXISTS (SELECT 1 FROM ai_instructions);
