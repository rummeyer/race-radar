# Changelog

## 1.1.0 — 2026-11-11

- Every race on the dashboard links to its ticket shop, sold out or not: its name and an
  *Open shop* button. Alerts and *Buy now* open the shop's tickets page, a click shorter than
  the event's.
- Waiting for a race takes plain text: `Berlin` finds every race whose name contains it, any
  case. No `*` needed; one typed anyway is ignored.

## 1.0.0 — 2026-11-11

- Watches HYROX ticket shops for sold-out tickets coming back, by opening them in headless
  Chromium as a visitor would: any race, any category, class and gender, any of its tickets.
- Races to pick from HYROX's Find my race, or any event or shop link pasted.
- Waits for races not announced yet: a name pattern such as `*Berlin*` tells of a new race
  that fits, and again when its tickets go on sale.
- Notifications by Pushover (to all devices or one) and by email (SMTP), each with a test.
- Dashboard: races and their tickets, what is checked right now, the next round, a live log,
  and the checks and alerts since start.
- Tells of a check that keeps failing, and of it working again.
