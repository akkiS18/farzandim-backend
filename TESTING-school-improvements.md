# School improvements test branch

Branch: `test/school-improvements` (base: `main`). Use with the frontend branch of the same name.

## Deployment order

1. Deploy this backend to a test environment. Normal startup runs tenant migration 000041.
2. Verify the migration succeeded for every test tenant before deploying the matching frontend.
3. Use a dedicated test school and Telegram bot/chat. Creating an announcement sends actual notifications to the school's configured channel and targeted linked parents.

Migration adds nullable `users.primary_subject_id`, announcement `image_urls`, and an upload ownership registry. Existing teachers keep an unset primary subject until edited. Existing AI reports retain unknown provenance; new reports save `generation_source`, `generation_model` and `generation_reason` in `summary_json`.

Uploaded announcement images use the existing R2 configuration or the existing local `uploads` storage. JPEG/PNG only, up to 10 MB per image and 10 images per announcement. Local images are uploaded directly to Telegram; they do not need a public backend URL. Existing uploaded images are not automatically deleted when an announcement is deleted.

## Checks

- `go test ./...` includes mocked Gemini success, empty response, missing key, quota and network failure; single-photo and album payloads. These tests never contact Telegram or Gemini.
- Migration up/down and the adjacent-lesson SQL were exercised against an isolated PostgreSQL-compatible PGlite database, including same-day periods, forward/backward, cancellation, substitution, extra lessons, level holidays, schedule-period changes and no match.
- Navigation searches up to one year in either direction, in the same class and subject, and checks the teacher's actual class/subject assignment.

## Manual acceptance

- Create/edit a teacher with a primary subject. Assign to a class: primary subject is suggested, a different subject is permitted. Existing assignments must remain unchanged.
- As MAIN_TEACHER and SUBJECT_TEACHER, open a class journal with multiple subjects: only that teacher's assigned subjects should be selectable. Admin retains all subjects.
- Navigate both ways, including repeated same-day subject periods and holiday/replacement dates. Do not navigate while a grade is saving.
- Publish text-only, one-photo and multi-photo announcements. Verify portal and dedicated Telegram test recipients; also test a long caption and a poll with images.
- Generate reports with a working test AI key, missing key, and failed provider request; verify source badges. Old reports must show unknown source.

No live school database migration, real AI request, or Telegram delivery was performed during development.
