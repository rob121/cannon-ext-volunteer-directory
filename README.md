# cannon-ext-volunteer-directory

Cannon extension for a public volunteer and vendor directory.

People submit their name, address, phone, email, skills, an optional other skill, and a required note. Staff search the saved list in the extension admin. A successful submit emits `onVolunteerDirectorySubmit` so System → Notifications can deliver it.

## Install

Build this module beside the Cannon checkout. `go.mod` replaces `github.com/rob121/cannon` with `../cannon`.

```bash
go test ./...
go build -o cannon-ext-volunteer-directory .
```

Register the binary with Cannon, then activate it for the site. Create a route such as `/volunteer` and bind the `volunteer-directory` page.

## Skills

The skill list is one JSON array in `volunteer_directory_settings`. The admin screen edits it as one skill per line. Install seeds:

- Kitchen
- Maintenance
- First aid
- Childcare
- Music
- Photography
- Teaching
- Setup and cleanup

## Notification event

| Event | Placeholders |
| --- | --- |
| `onVolunteerDirectorySubmit` | `name`, `email`, `phone`, `address`, `skills`, `other_skill`, `note`, `admin_url`, `site_id` |

`skills` is the selected labels joined with commas. Subscribe from System → Notifications after the extension is activated.
