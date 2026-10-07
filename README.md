# webuntis-cli

Read-only [WebUntis](https://webuntis.com) client for the terminal. Works with any school.

## Setup

1. **Install** – download a binary from the [releases](https://github.com/dgrieser/web-untis-cli/releases)
   (Linux amd64 / arm64 / armv7, macOS arm64) or build it:

   ```sh
   go install github.com/dgrieser/web-untis-cli/cmd/webuntis@latest
   ```

2. **Log in** – the wizard asks for profile, school (live search), login method, credentials and default student:

   ```sh
   webuntis setup
   ```

   Non-interactive: `echo "$PW" | webuntis login ge-huellhorst --user me@example.com --password-stdin`

   **Microsoft / SSO accounts** (typically students) have no WebUntis password. Use the Untis Mobile key
   instead: log in to WebUntis in the browser, open *Profil → Freigaben → "Zugriff über Untis Mobile" → Anzeigen*,
   copy the key shown next to the QR code and pick "Microsoft / SSO" in the wizard, or:

   ```sh
   webuntis login -p kid1 ge-huellhorst -u max --secret          # prompts for the key
   echo "$KEY" | webuntis login -p kid1 ge-huellhorst -u max --secret-stdin
   ```

   If the school hides "Zugriff über Untis Mobile" for students, the account cannot be used with this tool.

3. **Shell completion** (optional): `source <(webuntis completion bash)` (also zsh, fish).

4. **Try it:** `webuntis today`

## Features

| Command | Shows |
| --- | --- |
| `today [--agenda] [--new]` | Heute → Nachrichten, last login/import, unread counts; `--agenda` adds today's lessons, homework, exams |
| `news [N] [--new]` | Messages of the day in full; 🆕 marks unseen items |
| `news forward` | E-mails each news item once via SMTP |
| `messages [inbox\|sent\|drafts]` | Mitteilungen; `show ID`, `attachments ID`, `--search`, `--unread` |
| `messages forward` | E-mails messages (with attachments + history) via SMTP |
| `timetable [DATE]` | Student timetable as week grid; `--class [NAME]`, `--day`, `--days N`, `--list`, `-o html\|pdf` |
| `absences` | Reported absences; `--open` for unexcused |
| `absence-times` | Fehlzeiten with totals per subject |
| `homework [--open]` | Homework by due date |
| `class-register` | Klassenbucheinträge |
| `class-services` | Dienste |
| `exams [--all]` | Prüfungen incl. grades |
| `exemptions` | Befreiungen |
| `contact-hours` | Sprechstunden |
| `klassengeld [-T]` | Klassengeld addin: balance, payments, transactions |
| `addins`, `addins open NAME` | Addins; open/download link addins (e.g. PDF, `--text`) |
| `students`, `status` | Children of the account, login status |
| `schools search Q` | Public school directory |
| `profiles`, `config`, `cache clear` | Multiple accounts, SMTP/default student/time zone, cache |
| `api get PATH`, `api rpc METHOD` | Raw read-only API access |

**Common flags**

- `-o pretty|markdown|json|yaml|ics|html|pdf`: pretty is the default on a terminal, markdown when piped. `ics` works for timetable, exams, homework and absences; `html` and `pdf` for timetable.
- `-s NAME`: pick the student when the account has several children.
- `-p PROFILE`: use a different account or school.
- `-r`: bypass the cache.
- `--debug`: log HTTP requests.

**Printing the timetable:** `-o html` writes a standalone page for the browser or printing. A week fits on
one A4 landscape page; `--list` and `--day` print as A4 portrait. `-o pdf` writes the same layout as PDF.
It uses a headless Chrome, Chromium or Edge if one is installed (`$WEBUNTIS_BROWSER` selects the executable),
otherwise a built-in renderer that needs no browser, so it also works on headless servers.
`--pdf-engine native|browser` (or `$WEBUNTIS_PDF_ENGINE`) forces one of them:

```sh
webuntis tt -o html > stundenplan.html
webuntis tt next-week -o pdf --file stundenplan.pdf   # without --file: stdout when piped, else stundenplan-<date>.pdf
```

**Dates:** `2026-09-21`, `21.09.`, `today`, `morgen`, `monday`, `+1w`, `next-week`.

Opening an unread message marks it as read, just like in the web UI. Nothing else is written to WebUntis.

## Forwarding via SMTP

```sh
webuntis config smtp                            # wizard: provider presets (Gmail, GMX, …), test mail
webuntis config smtp test                       # send a test mail
webuntis messages forward --mark-only           # skip existing messages once
webuntis messages forward --watch 10m           # or run from cron; same for `news forward`
```

Already forwarded items are tracked, so every item is sent only once. `--dry-run` previews and `--force` resends.
Scripts can use flags instead of the wizard: `config smtp --host … --user … --password-stdin --from … --to …`.

## Storage

Everything lives in `~/.cache/webuntis-cli/<profile>/` (override with `$WEBUNTIS_CLI_HOME`):

- `config.json`: school, username, SMTP settings (file mode 0600)
- `session.json`: the login session
- `cache/`: response cache
- `forwarded.json`, `news-seen.json`, `news-forwarded.json`: already sent and already seen items

**Passwords** (WebUntis and SMTP) and the Untis Mobile key are kept in the
system keyring: Secret Service on Linux, Keychain on macOS, Credential Manager
on Windows. They are stored under the services `webuntis-cli` /
`webuntis-cli-secret` / `webuntis-cli-smtp` with the profile name as account,
and only after the server accepted them. The tool reads them only when the
session has expired.


- `--no-keyring` / `$WEBUNTIS_NO_KEYRING=1` keeps passwords and the Untis Mobile key in
  `config.json` (e.g. headless machines without Secret Service).
- `--no-store-password` stores nothing; `$WEBUNTIS_PASSWORD` / `$WEBUNTIS_SECRET` /
  `$WEBUNTIS_SMTP_PASSWORD` override the stored credentials.
- `logout --forget` removes the keyring entries.

Environment variables: `WEBUNTIS_PROFILE`, `WEBUNTIS_OUTPUT`, `WEBUNTIS_STYLE`, `WEBUNTIS_FORM_THEME`, `WEBUNTIS_NO_KEYRING`, `WEBUNTIS_BROWSER`, `WEBUNTIS_PDF_ENGINE`.

## How it works

There is no public API for end users. The official JSON-RPC API (`/WebUntis/jsonrpc.do`) is partner-documented and limited. This tool logs in via JSON-RPC (`authenticate` with username + password; Microsoft/SSO accounts use the Untis Mobile app protocol instead: `jsonrpc_intern.do` `getUserData2017` with a TOTP derived from the Untis Mobile key, which yields the same session cookie) and then uses the same JSON endpoints as the web UI:

- `/WebUntis/api/token/new`: returns a Bearer JWT used for `/api/rest/view/v1/*` (timetable, messages, app data).
- `/api/classreg/*`, `/api/homeworks/lessons`, `/api/exams`, `/api/public/news|officehours/*`: these use the session cookie.
- Klassengeld: logs in via WebUntis single sign-on and reads the klassengeld.app pages.

See `internal/webuntis/`. These APIs are undocumented and may change.

The built-in PDF renderer embeds the [Inter](https://rsms.me/inter/) font (SIL Open Font License, `internal/views/fonts/OFL.txt`).

## Development

```sh
make test lint build
goreleaser release --snapshot --clean --skip=publish   # local release dry run
```

CI runs tests (incl. 32-bit), lint and a snapshot build. Pushing a tag `vX.Y.Z` publishes a release.
