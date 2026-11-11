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

/** As the radar decides it (Watch.Wants): the tickets ticked, else those of the days chosen. */
function wants(w, name) {
  if (w.wanted?.length) return w.wanted.includes(name);
  const upper = name.toUpperCase();
  if (w.planned && (upper.includes('CHARITY') || upper.includes('ADAPTIVE'))) return false;
  return !w.days?.length || w.days.some((d) => upper.includes(d.toUpperCase()));
}

function wantedOf(w) {
  return (w.tickets ?? []).filter((t) => wants(w, t.name));
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
    const status = w.paused ? tag('Paused') : w.error ? tag('Check failing', 'bad') : w.waiting ? tag('Not on sale yet', 'wait')
      : open ? tag('Available', 'ok') : never(w.checked) ? tag('First check…', 'wait') : tag('Sold out');
    const running = state.running?.subject === w.event;
    const shop = w.ticketsUrl || w.url;
    const title = el('a', 'race-link', w.event || 'Race');
    title.href = w.racePage || shop;
    title.target = '_blank';
    title.rel = 'noopener';
    title.title = w.racePage ? 'Open the race page' : 'Open the ticket shop';
    const top = el('div', 'race-top',
      el('div', '',
        el('h3', '', title),
        el('div', 'meta', status, w.dates && el('span', '', w.dates), el('span', '', running ? 'checking now…' : `checked ${ago(w.checked)}`))),
      el('div', 'race-actions',
        shop && shopLink(shop),
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
    const days = w.days?.length ? [el('li', '', el('span', 'lbl', 'Days'), w.days.map((d) => d.slice(0, 3)).join(' · '))] : [];
    const chips = el('ol', 'chips', ...steps.map((s) => el('li', '', s.label && el('span', 'lbl', s.label), s.value)), ...days);
    const shown = wanted.length ? wanted : (w.wanted ?? []).map((name) => ({ name, unseen: true }));
    if (!shown.length && w.planned) {
      shown.push({ name: w.waiting ? 'Tickets show here once the shop opens' : 'None of these tickets listed', unseen: true, note: true });
    }
    const list = el('ul', 'tickets', ...shown.map((t) => t.note ? el('li', 'ticket note', el('span', 'muted', t.name)) :
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
const wizard = { url: '', path: [], page: null, event: null };

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

// A box the browser ticks again on reloading is not told of: the list follows the boxes whenever
// it is shown, not only when they change.
$('youth').addEventListener('change', renderEvents);
$('all').addEventListener('change', renderEvents);
window.addEventListener('pageshow', () => events.length && renderEvents());
$('event').addEventListener('change', () => {
  const url = $('event').value;
  if (url) start(url, events.find((e) => e.url === url));
});
$('open-link').addEventListener('click', () => {
  const url = $('link').value.trim();
  if (url) start(url, events.find((e) => e.url.replace(/\/$/, '') === url.replace(/\/$/, '')));
});

function start(url, event) {
  wizard.url = url;
  wizard.path = [];
  wizard.page = null;
  wizard.event = event || null;
  wizard.racePage = event?.url || (/^https:\/\/(www\.)?hyrox\.com\/event\//.test(url) ? url : '');
  $('wizard').hidden = false;
  $('manual').hidden = true;
  $('to-manual').hidden = true;
  $('w-title').textContent = event?.name || '';
  $('w-dates').textContent = event?.dates || '';
  $('w-path').replaceChildren();
  // No tickets on sale yet, as Find my race says: set up by hand at once.
  if (event && !event.onSale) {
    showManual('No tickets on sale for this race yet.');
    return;
  }
  browse();
}

function showManual(why) {
  $('w-busy').hidden = true;
  $('w-options').replaceChildren();
  $('w-tickets').hidden = true;
  $('to-manual').hidden = true;
  $('m-why').firstChild.textContent = `${why} Set the race up with the choices every HYROX shop has: the radar waits, tells you when the shop opens, and from then on watches these tickets.`;
  $('manual').hidden = !wizard.racePage;
  if (!wizard.racePage) {
    $('w-error').textContent = 'To set a race up by hand, choose it from the list: the radar finds its shop on its race page.';
    $('w-error').hidden = false;
  }
  syncManual();
}

/** As HYROX's shops have it: Pro for Singles and Doubles only, Mixed for Doubles and Relay. */
function syncManual() {
  const cat = $('m-cat').value;
  const pro = $('m-class').querySelector('option:last-child');
  pro.disabled = cat === 'Relay';
  if (pro.disabled && $('m-class').value === 'Pro') $('m-class').value = 'Open';
  const mixed = $('m-gender').querySelector('option:last-child');
  mixed.disabled = cat === 'Singles';
  if (mixed.disabled && $('m-gender').value === 'Mixed') $('m-gender').value = 'Men';
}
$('m-cat').addEventListener('change', syncManual);
$('to-manual').addEventListener('click', () => showManual('Not the right tickets, or the shop not open yet?'));

$('save-manual').addEventListener('click', async () => {
  const cat = $('m-cat').value;
  const steps = [
    { label: 'Category', value: cat },
    { label: 'Class', value: $('m-class').value },
    { label: 'Gender', value: $('m-gender').value },
  ];
  const days = [...document.querySelectorAll('.days input:checked')].map((b) => b.value);
  try {
    await api('POST', '/api/watches', {
      planned: true, racePage: wizard.racePage,
      event: wizard.event?.name || $('w-title').textContent || 'Race', dates: wizard.event?.dates || '',
      path: steps.map((s) => s.value), steps, days,
    });
  } catch (err) {
    alert(err.message);
    return;
  }
  done();
});

function done() {
  $('wizard').hidden = true;
  $('event').value = '';
  location.hash = '#dashboard';
  refresh();
}

async function browse() {
  $('w-busy').hidden = false;
  $('w-error').hidden = true;
  $('manual').hidden = true;
  $('to-manual').hidden = true;
  $('w-options').replaceChildren();
  $('w-tickets').hidden = true;
  renderPath();
  try {
    wizard.page = await api('POST', '/api/browse', { url: wizard.url, path: wizard.path });
    wizard.url = wizard.page.url;
    if (wizard.page.racePage) wizard.racePage = wizard.page.racePage;
  } catch (err) {
    $('w-error').textContent = err.message;
    $('w-error').hidden = false;
    if (wizard.racePage) $('to-manual').hidden = false;
    return;
  } finally {
    $('w-busy').hidden = true;
  }
  const page = wizard.page;
  $('w-title').textContent = wizard.event?.name || page.title;
  $('w-dates').textContent = wizard.event?.dates || page.dates;
  $('to-manual').hidden = !wizard.racePage;
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
      event: wizard.event?.name || page.title, dates: wizard.event?.dates || page.dates,
      url: wizard.url, racePage: wizard.racePage, ticketsUrl: page.ticketsUrl, path: wizard.path,
      steps: page.steps, wanted, tickets: page.tickets,
    });
  } catch (err) {
    alert(err.message);
    return;
  }
  done();
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
