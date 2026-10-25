# Volunteer Directory

The Volunteer Directory extension collects public volunteer and vendor submissions for a Cannon site.

## What it provides

- A `volunteer-directory` page that renders the public form.
- Skill checkboxes stored as one JSON array in the extension settings table.
- An admin list that searches name, email, phone, address, skills, other skill, and note.
- A detail view and delete action for each person.
- The `onVolunteerDirectorySubmit` notification event, which System → Notifications can subscribe to after the extension is activated.

## Public form

Bind a site route, for example `/volunteer`, to the `volunteer-directory` page. The form posts to `POST /data/volunteer/submit`.

Required fields are name, address, phone, email, and note. A submission also needs at least one checked skill or text in Other skill.

## Skills

Open Volunteer Directory → Edit skills. Enter one skill per line. Install seeds kitchen, maintenance, first aid, childcare, music, photography, teaching, and setup and cleanup.

## Notifications

The event name is `onVolunteerDirectorySubmit`. Placeholders are `name`, `email`, `phone`, `address`, `skills`, `other_skill`, `note`, `admin_url`, and `site_id`. `skills` is a comma-separated list of the selected labels.
