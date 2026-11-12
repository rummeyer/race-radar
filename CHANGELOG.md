# Changelog

## Unreleased

## 1.3.0 — 2026-11-12

- The current shop design again: since 12.11.2026 vivenu puts the steps in a bar of their own,
  and the choices came after the bar, not right after the steps — every check said "the shop
  showed neither choices nor tickets". They are taken from the first block with buttons after the
  steps now, in either place.
- Renamed from HYROX Radar to **Race Radar**, without the trademark in its name: the repository
  is `rummeyer/race-radar`, the program `race-radar`, the service `race-radar.service`, the data
  `~/.race-radar` — `~/.hyrox-radar` is moved there on the first start.

## 1.2.0 — 2026-11-11

- Every region's shop: the classic checkout too, as the DACH, Spanish and other shops have it,
  and shops whose choices carry a status badge (Korea, China, Benelux, Australia).
- A race's shop is found among its page's links by preference for the region's shop and the
  season's event, passing over hotel, volunteer and local Youngstars links (Singapore, Hong Kong,
  Minneapolis).
- Races set up by hand before their shop opens: category, class, gender and days. They wait
  quietly, tell when the shop opens, and are watched from then on.
- A race's name links to its page on hyrox.com, *Open shop* to its ticket shop.
- The *Races without tickets yet* box works when the browser ticks it again on reloading.

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
