# QR dinàmics

Dynamic QR codes for small businesses: create a QR code, print it, and change where it points whenever you like. The redirect service keeps answering when the panel or the database are down.

## Architecture

Two Go binaries, one Docker image, and Postgres:

| Binary | Port | What it does |
| --- | --- | --- |
| `/panel` (`cmd/panel`) | 8080 | Owner's panel (server-rendered HTML): sign-up, QR codes, scan statistics. Applies the database migrations. Can be restricted to Cloudflare Access. |
| `/redirect` (`cmd/redirect`) | 8081 | `GET /{code}` → 302 to the current destination. Answers from an in-memory table synced every `SYNC_INTERVAL` and saved to a local snapshot, so it starts and answers while the database is down. Records scans in batches, off the request path. |

Code is organized by domain: `internal/accounts`, `internal/qrcodes`, `internal/redirect`, `internal/privacy` (export, erasure, retention), `internal/legal` (legal pages), `internal/safety`, `internal/access` and `internal/web`; `internal/panel` assembles the panel.

## Development

Before your first commit: `pre-commit install` (gitleaks blocks commits that contain secrets).

```sh
make up                # Postgres + panel (http://localhost:8080) + redirect (http://localhost:8081)
make test              # unit tests
make test-integration  # unit + integration tests against the compose Postgres
make lint
```

In dev, e-mails (the verification link) are written to the log: `docker compose logs panel`.

The panel's user interface is in Catalan; code, comments and documentation are in English.

## Configuration

| Variable | Service | Required | Notes |
| --- | --- | --- | --- |
| `APP_ENV` | both | yes | `dev` or `prod` |
| `DATABASE_URL` | both | yes | |
| `CONTACT_EMAIL` | both | yes | published contact: legal pages, abuse and copyright reports, data requests |
| `PANEL_BASE_URL` | both | yes | public URL of the panel (e-mail and legal-page links) |
| `REDIRECT_BASE_URL` | both | yes | the host every QR code encodes |
| `LEGAL_HOLDER_NAME`, `LEGAL_HOLDER_TAX_ID`, `LEGAL_HOLDER_ADDRESS` | both | before going public | service provider in the legal notice (LSSI art. 10); a prod panel without Cloudflare Access refuses to start without them |
| `SAFE_BROWSING_API_KEY` | panel | in prod | without it (dev only) destination URLs are not checked |
| `CF_ACCESS_TEAM_DOMAIN`, `CF_ACCESS_AUD` | panel | no | both or neither; every request must carry a valid Cloudflare Access JWT |
| `SNAPSHOT_PATH` | redirect | yes | its directory must be writable |
| `SYNC_INTERVAL` | redirect | no | default `10s`; must be under 1 minute |
| `COUNTRY_HEADER` | redirect | no | header set by the edge proxy with the country (e.g. `CF-IPCountry`); IPs are never stored |

## Legal and compliance

What the code guarantees, and where:

| Requirement | How |
| --- | --- |
| Legal notice, privacy policy, terms, cookie policy, third-party licences | `internal/legal`, served at `/legal/*` by both binaries; linked from every panel page and the neutral page |
| Terms accepted explicitly, marketing consent separate | unticked checkboxes at sign-up; every decision is stored with its version and time in `consents` |
| Data export and erasure (GDPR arts. 15, 17, 20) | `/compte`: JSON of everything, CSV of QR codes, account deletion (password + typed confirmation) |
| Retention | `internal/privacy`: scans 24 months, unverified accounts 30 days, expired sessions and tokens; purged daily. Tested against the numbers the privacy policy shows |
| No trackers | one first-party, strictly necessary session cookie; no analytics, so no consent banner. Adding any tracker requires a banner that blocks it until accepted |
| E-mails | transactional only, with a footer saying why they are sent; marketing e-mails will need a one-click unsubscribe link and `List-Unsubscribe` |
| Accessibility (WCAG 2.1 AA) | colour tokens checked by `TestPaletteMeetsWCAG`; skip link, visible focus, labelled form controls, alt text on the QR image, keyboard-only operable |
| Open-source licences | `make licenses` regenerates `internal/legal/third_party_licenses.txt` (all permissive: MIT, BSD-3-Clause, Apache-2.0); CI fails if it is out of date |

The legal texts are a starting point written without legal advice; have them reviewed before opening the panel to the public.

## Releases and deployment

Every push to `main` runs the tests and publishes `fikua/qr-dinamics:latest` and `:sha-<commit>` to Docker Hub ([release.yml](.github/workflows/release.yml)). CodeQL and SonarQube Cloud analyse every push and pull request.

Deployment configuration lives in `fikua-platform-iac/projects/qr-dinamics/`: redirect at `https://qr.fikua.com`, panel at `https://qr-app.fikua.com` (closed beta behind Cloudflare Access).

## Status

Done: US-01 to US-07, RF-01 to RF-06 and RF-09 to RF-11 of the PRD.

Pending:
- US-08, US-09, RF-07, RF-08: Stripe billing and its e-mails.
- US-10: GDPR export and deletion.
- RF-12: admin view to block QR codes or accounts.
- Downgrade rule when a subscription ends (depends on Stripe).
- SMTP mailer for prod: the panel refuses to start with `APP_ENV=prod` until it exists.
- Spanish user interface (Catalan only for now).
- Login attempt limiting.
- Open PRD decisions that affect the code: the free quota (3 for now) and what a paused QR shows (the neutral page for now).

## License

Source available, not open source: all rights reserved. See [LICENSE](LICENSE). Licensing enquiries: hola@fikua.com.
