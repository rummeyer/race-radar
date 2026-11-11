'use strict';

const $ = (id) => document.getElementById(id);

/** Makes an element: el('li', 'ticket', child, 'text', …); falsy children are left out. */
function el(tag, className, ...children) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  for (const child of children) {
    if (child !== null && child !== undefined && child !== false && child !== '') node.append(child);
  }
  return node;
}

function button(text, className, onClick) {
  const b = el('button', className, text);
  b.type = 'button';
  b.addEventListener('click', () => onClick(b));
  return b;
}

function tag(text, kind) {
  return el('span', `tag ${kind || ''}`, text);
}

async function api(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : {},
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
  return data;
}

const never = (iso) => !iso || iso.startsWith('0001');

/** "3 min ago", "just now". */
function ago(iso) {
  if (never(iso)) return 'not yet';
  const s = Math.round((Date.now() - new Date(iso)) / 1000);
  if (s < 45) return 'just now';
  if (s < 3600) return `${Math.round(s / 60)} min ago`;
  if (s < 86400) return `${Math.round(s / 3600)} h ago`;
  return new Date(iso).toLocaleDateString();
}

const clock = (iso) => new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

// ── Tabs ───────────────────────────────────────────────────────────────────

function showTab() {
  const name = (location.hash || '#dashboard').slice(1);
  const known = ['dashboard', 'add', 'settings'].includes(name) ? name : 'dashboard';
  for (const section of document.querySelectorAll('.tab')) section.hidden = section.id !== `tab-${known}`;
  for (const a of document.querySelectorAll('.tabs a')) a.classList.toggle('on', a.dataset.tab === known);
  window.scrollTo({ top: 0 });
}
window.addEventListener('hashchange', showTab);
showTab();

// ── Dashboard ──────────────────────────────────────────────────────────────

let state = null;

async function refresh() {
  try {
    state = await api('GET', '/api/state');
  } catch (err) {
    $('live').className = 'live down';
    $('live-text').textContent = 'Radar unreachable';
    return;
  }
  renderLive();
  renderStats();
  renderWatches();
  renderSearches();
  renderLog();
  renderSettings();
  $('version').textContent = state.version;
}

function renderLive() {
  const live = $('live');
  if (state.running) {
    live.className = 'live busy';
    $('live-text').textContent = `Checking ${state.running.subject}`;
    return;
  }
  live.className = 'live';
  const next = never(state.next) ? null : Math.max(0, Math.round((new Date(state.next) - Date.now()) / 1000));
  $('live-text').textContent = next === null ? 'Live' : next < 60 ? `Live · next in ${next}s` : `Live · next in ${Math.round(next / 60)} min`;
}

function wantedOf(w) {
  return (w.tickets ?? []).filter((t) => !w.wanted?.length || w.wanted.includes(t.name));
}

function renderStats() {
  const active = state.watches.filter((w) => !w.paused);
  $('s-races').textContent = active.length;
  $('s-open').textContent = active.flatMap(wantedOf).filter((t) => t.available).length;
  $('s-checks').textContent = state.stats.checks;
  $('s-checks-lbl').textContent = state.stats.failures ? `Checks · ${state.stats.failures} failed` : 'Checks since start';
  $('s-alerts').textContent = state.stats.alerts;
  $('s-alerts-lbl').textContent = never(state.stats.lastAlert) ? 'Alerts sent' : `Alerts · last ${ago(state.stats.lastAlert)}`;
}

function renderWatches() {
  const box = $('watches');
  $('no-watches').hidden = state.watches.length > 0;
  box.replaceChildren(...state.watches.map((w) => {
    const wanted = wantedOf(w);
    const open = wanted.some((t) => t.available);
    const status = w.paused ? tag('Paused') : w.error ? tag('Check failing', 'bad') : open ? tag('Available', 'ok') : never(w.checked) ? tag('First check…', 'wait') : tag('Sold out');
    const running = state.running?.subject === w.event;
    const shop = w.ticketsUrl || w.url;
    const title = el('a', 'race-link', w.event || 'Race');
    title.href = shop;
    title.target = '_blank';
    title.rel = 'noopener';
    title.title = 'Open the ticket shop';
    const top = el('div', 'race-top',
      el('div', '',
        el('h3', '', title),
        el('div', 'meta', status, w.dates && el('span', '', w.dates), el('span', '', running ? 'checking now…' : `checked ${ago(w.checked)}`))),
      el('div', 'race-actions',
        shopLink(shop),
        button('Check now', 'text', async (b) => {
          b.textContent = 'Checking…';
          b.disabled = true;
          await api('POST', `/api/watches/${w.id}/check`).catch((e) => alert(e.message));
          refresh();
        }),
        button(w.paused ? 'Resume' : 'Pause', 'text', async () => {
          await api('PATCH', `/api/watches/${w.id}`, { paused: !w.paused }).catch((e) => alert(e.message));
          refresh();
        }),
        button('Remove', 'text danger', async () => {
          if (!confirm(`Stop watching ${w.event}?`)) return;
          await api('DELETE', `/api/watches/${w.id}`).catch((e) => alert(e.message));
          refresh();
        })));
    const steps = w.steps?.length ? w.steps : (w.path ?? []).map((value) => ({ value }));
    const chips = el('ol', 'chips', ...steps.map((s) => el('li', '', s.label && el('span', 'lbl', s.label), s.value)));
    const shown = wanted.length ? wanted : (w.wanted ?? []).map((name) => ({ name, unseen: true }));
    const list = el('ul', 'tickets', ...shown.map((t) =>
      el('li', `ticket ${t.available ? 'available' : ''}`,
        el('span', '', el('span', 'name', t.name), t.price && el('span', 'price', t.price)),
        t.available ? buyLink(shop) : tag(t.unseen ? 'Not listed' : 'Sold out'))));
    const classes = ['race', open && 'open', w.error && 'failing', w.paused && 'paused'].filter(Boolean).join(' ');
    return el('article', classes, top, chips, list, w.error && el('p', 'error', w.error));
  }));
}

function shopLink(href) {
  const a = el('a', 'shop', 'Open shop ↗');
  a.href = href;
  a.target = '_blank';
  a.rel = 'noopener';
  return a;
}

function buyLink(href) {
  const a = el('a', 'buy', 'Buy now →');
  a.href = href;
  a.target = '_blank';
  a.rel = 'noopener';
  return a;
}

function renderSearches() {
  $('searches').replaceChildren(...state.searches.map((s) => {
    const matches = s.matches ?? [];
    const sub = matches.length
      ? `fits ${matches.map((m) => `${m.name} (${m.dates}${m.onSale ? ', on sale' : ''})`).join(' · ')}`
      : 'nothing fits yet — waiting';
    return el('li', '',
      el('span', '', el('span', 'name', s.pattern), el('br'), el('span', 'sub', sub)),
      button('Remove', 'text danger', async () => {
        await api('DELETE', `/api/searches/${s.id}`).catch((e) => alert(e.message));
        refresh();
      }));
  }));
}

$('search-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  try {
    await api('POST', '/api/searches', { pattern: $('pattern').value });
    $('pattern').value = '';
    refresh();
  } catch (err) {
    alert(err.message);
  }
});

const KIND = { check: 'Checked', search: 'Searched', alert: 'Alert sent', error: 'Failed' };

function renderLog() {
  $('now').hidden = !state.running;
  if (state.running) {
    const s = Math.round((Date.now() - new Date(state.running.since)) / 1000);
    $('now-what').textContent = `${state.running.subject} · ${s}s`;
  }
  $('no-log').hidden = state.log.length > 0;
  $('log').replaceChildren(...state.log.slice(0, 80).map((e) => {
    const time = el('time', '', clock(e.at));
    time.dateTime = e.at;
    return el('li', `k-${e.kind}`, time,
      el('span', 'what', `${KIND[e.kind] || e.kind} · ${e.subject}`),
      el('span', 'text', e.text + (e.seconds ? ` · ${e.seconds.toFixed(0)}s` : '')));
  }));
}

// ── Adding a race ──────────────────────────────────────────────────────────

let events = [];
const wizard = { url: '', path: [], page: null };

async function loadEvents() {
  try {
    events = await api('GET', '/api/events');
  } catch (err) {
    $('event').replaceChildren(el('option', '', `Find my race could not be read: ${err.message}`));
    return;
  }
  renderEvents();
}

function renderEvents() {
  const youth = $('youth').checked;
  const all = $('all').checked;
  const select = $('event');
  const first = el('option', '', 'Choose a race…');
  first.value = '';
  select.replaceChildren(first);
  const groups = new Map();
  for (const e of events) {
    if ((e.youth && !youth) || (!e.onSale && !all)) continue;
    const name = e.continent || 'Other';
    if (!groups.has(name)) {
      const group = el('optgroup');
      group.label = name;
      groups.set(name, group);
    }
    const option = el('option', '', `${e.dates} · ${e.name}${e.city ? ` (${e.city})` : ''}${e.onSale ? '' : ' · no tickets yet'}`);
    option.value = e.url;
    groups.get(name).append(option);
  }
  for (const name of [...groups.keys()].sort()) select.append(groups.get(name));
  select.disabled = false;
}

$('youth').addEventListener('change', renderEvents);
$('all').addEventListener('change', renderEvents);
$('event').addEventListener('change', () => {
  if ($('event').value) start($('event').value);
});
$('open-link').addEventListener('click', () => {
  if ($('link').value.trim()) start($('link').value.trim());
});

function start(url) {
  wizard.url = url;
  wizard.path = [];
  wizard.page = null;
  $('wizard').hidden = false;
  $('w-title').textContent = '';
  $('w-dates').textContent = '';
  browse();
}

async function browse() {
  $('w-busy').hidden = false;
  $('w-error').hidden = true;
  $('w-options').replaceChildren();
  $('w-tickets').hidden = true;
  renderPath();
  try {
    wizard.page = await api('POST', '/api/browse', { url: wizard.url, path: wizard.path });
    wizard.url = wizard.page.url;
  } catch (err) {
    $('w-error').textContent = err.message;
    $('w-error').hidden = false;
    return;
  } finally {
    $('w-busy').hidden = true;
  }
  const page = wizard.page;
  $('w-title').textContent = page.title;
  $('w-dates').textContent = page.dates;
  renderPath();
  if (page.tickets.length) {
    $('w-list').replaceChildren(...page.tickets.map((t, i) => {
      const box = el('input');
      box.type = 'checkbox';
      box.value = t.name;
      box.id = `t${i}`;
      const label = el('label', '', box, el('span', 'name', t.name), t.price && el('span', 'price', t.price));
      label.htmlFor = box.id;
      return el('li', `ticket ${t.available ? 'available' : ''}`, label, t.available ? tag('Available', 'ok') : tag('Sold out'));
    }));
    $('w-tickets').hidden = false;
  } else {
    $('w-options').replaceChildren(...page.options.map((o) => button(o, '', () => {
      wizard.path.push(o);
      browse();
    })));
  }
}

/** The choices so far; clicking one chooses again from there. */
function renderPath() {
  const steps = wizard.page?.steps ?? [];
  $('w-path').replaceChildren(...wizard.path.map((value, i) => {
    const back = button('', '', () => {
      wizard.path = wizard.path.slice(0, i);
      browse();
    });
    back.append(steps[i]?.label ? el('span', 'lbl', steps[i].label) : '', value);
    back.title = 'Choose again from here';
    return el('li', '', back);
  }));
}

$('save-watch').addEventListener('click', async () => {
  const page = wizard.page;
  const wanted = [...$('w-list').querySelectorAll('input:checked')].map((b) => b.value);
  try {
    await api('POST', '/api/watches', {
      event: page.title, dates: page.dates, url: wizard.url, ticketsUrl: page.ticketsUrl, path: wizard.path,
      steps: page.steps, wanted, tickets: page.tickets,
    });
  } catch (err) {
    alert(err.message);
    return;
  }
  $('wizard').hidden = true;
  $('event').value = '';
  location.hash = '#dashboard';
  refresh();
});

// ── Settings ───────────────────────────────────────────────────────────────

let settingsShown = false;

function renderSettings() {
  if (settingsShown) return;
  settingsShown = true;
  const { pushover: p, email: e, intervalMinutes } = state.settings;
  $('interval').value = String(intervalMinutes);
  $('p-on').checked = p.enabled;
  $('p-token').placeholder = p.tokenSet ? 'saved — type to replace' : 'pushover.net → Your Applications';
  $('p-user').placeholder = p.userSet ? 'saved — type to replace' : 'pushover.net → Your User Key';
  setDevice(p.device);
  if (p.tokenSet && p.userSet) loadDevices(p.device);
  $('e-on').checked = e.enabled;
  $('e-host').value = e.host;
  $('e-port').value = e.port || '';
  $('e-user').value = e.username;
  $('e-pass').placeholder = e.passwordSet ? 'saved — type to replace' : '';
  $('e-from').value = e.from;
  $('e-to').value = e.to;
}

function setDevice(device) {
  const select = $('p-device');
  if (device && ![...select.options].some((o) => o.value === device)) {
    const o = el('option', '', device);
    o.value = device;
    select.append(o);
  }
  select.value = device || '';
}

async function loadDevices(current) {
  try {
    const devices = await api('GET', '/api/pushover/devices');
    const all = el('option', '', 'All devices');
    all.value = '';
    $('p-device').replaceChildren(all, ...devices.map((d) => {
      const o = el('option', '', d);
      o.value = d;
      return o;
    }));
    setDevice(current);
  } catch {
    // Wrong keys say so on saving or testing.
  }
}

function settingsBody() {
  return {
    intervalMinutes: Number($('interval').value),
    pushover: { enabled: $('p-on').checked, token: $('p-token').value, user: $('p-user').value, device: $('p-device').value },
    email: {
      enabled: $('e-on').checked, host: $('e-host').value, port: Number($('e-port').value) || 0,
      username: $('e-user').value, password: $('e-pass').value, from: $('e-from').value, to: $('e-to').value,
    },
  };
}

async function saveSettings() {
  await api('PUT', '/api/settings', settingsBody());
  for (const id of ['p-token', 'p-user', 'e-pass']) $(id).value = '';
  settingsShown = false;
  await refresh();
  loadDevices($('p-device').value);
}

$('settings-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const note = $('settings-note');
  try {
    await saveSettings();
    note.textContent = 'Saved.';
  } catch (err) {
    note.textContent = err.message;
  }
});

for (const b of document.querySelectorAll('[data-test]')) {
  b.addEventListener('click', async () => {
    const channel = b.dataset.test;
    const note = document.querySelector(`[data-note="${channel}"]`);
    note.textContent = 'Saving and sending…';
    try {
      await saveSettings();
      await api('POST', `/api/test/${channel}`);
      note.textContent = 'Sent — check it arrived.';
    } catch (err) {
      note.textContent = err.message;
    }
  });
}

refresh();
loadEvents();
setInterval(refresh, 5_000);
