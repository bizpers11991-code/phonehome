// phonehome dashboard. Vanilla JS, no build step, no network beyond this server.
'use strict';

// First-run snippets. Keep in sync with internal/config.
const SNIPPETS = {
  'Pi-hole': {
    note: 'On the Pi-hole machine itself phonehome finds the database on its own. Elsewhere, point it at a copy:',
    code: ['sources:\n  - type: pihole-db\n    path: /etc/pihole/pihole-FTL.db'],
  },
  'AdGuard Home': {
    note: 'phonehome reads the query log AdGuard Home already writes, including the rotated file:',
    code: ['sources:\n  - type: adguard-querylog\n    path: /opt/AdGuardHome/data/querylog.json'],
  },
  dnsmasq: {
    note: 'Turn on query logging in dnsmasq (OpenWrt: System › Logging), then point phonehome at the log:',
    code: [
      '# /etc/dnsmasq.conf\nlog-queries\nlog-facility=/var/log/dnsmasq.log',
      'sources:\n  - type: dnsmasq-log\n    path: /var/log/dnsmasq.log',
    ],
  },
};

const KINDS = {
  tv: 'TV', streamer: 'Streaming player', speaker: 'Smart speaker', camera: 'Camera',
  vacuum: 'Robot vacuum', plug: 'Plug or bulb', hub: 'Smart-home hub', appliance: 'Appliance',
  console: 'Games console', phone: 'Phone', computer: 'Computer', network: 'Network gear',
  unknown: 'Unknown device',
};

const PERIODS = { 1: 'in the last 24 hours', 7: 'this week', 30: 'in the last 30 days' };

const state = { days: 7, report: null, status: null, cats: new Map() };

// ---------- DOM helpers (textContent only: domains and labels are untrusted) ----------

function h(tag, props, ...kids) {
  const el = document.createElement(tag);
  setProps(el, props);
  el.append(...kids.flat().filter((k) => k != null && k !== false));
  return el;
}

function s(tag, props, ...kids) {
  const el = document.createElementNS('http://www.w3.org/2000/svg', tag);
  setProps(el, props);
  el.append(...kids);
  return el;
}

function setProps(el, props) {
  for (const [k, v] of Object.entries(props || {})) {
    if (v == null || v === false) continue;
    if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (k === 'text') el.textContent = v;
    else el.setAttribute(k, v === true ? '' : v);
  }
}

function icon(name, cls) {
  return s('svg', { class: 'ico' + (cls ? ' ' + cls : ''), 'aria-hidden': 'true' },
    s('use', { href: 'assets/icons.svg#' + name }));
}

const nf = new Intl.NumberFormat();
const fmt = (n) => nf.format(Math.round(n));

function pct(x) {
  if (x > 0 && x < 0.01) return '<1%';
  return Math.round(x * 100) + '%';
}

function flag(cc) {
  if (!/^[A-Za-z]{2}$/.test(cc || '')) return '';
  return String.fromCodePoint(...[...cc.toUpperCase()].map((c) => 0x1f1a5 + c.charCodeAt(0)));
}

function every(sec) {
  if (sec < 90) return `every ${Math.round(sec)} s`;
  if (sec < 5400) return `every ${Math.round(sec / 60)} min`;
  return `every ${Math.round(sec / 3600)} h`;
}

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });
function ago(ts) {
  if (!ts) return 'never';
  const sec = (Date.parse(ts) - Date.now()) / 1000;
  const units = [[60, 'second'], [3600, 'minute'], [86400, 'hour'], [Infinity, 'day']];
  let div = 1;
  for (const [limit, unit] of units) {
    if (Math.abs(sec) < limit) return rtf.format(Math.round(sec / div), unit);
    div = limit;
  }
  return '';
}

function catClass(id) { return 'cat-' + (state.cats.has(id) ? id : 'unknown'); }
function catLabel(id) { return state.cats.get(id)?.label ?? id; }

let toastTimer;
function toast(msg) {
  const t = document.getElementById('toast');
  t.textContent = msg;
  t.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.remove('show'), 3200);
}

async function api(path, opts) {
  const res = await fetch(path, { credentials: 'same-origin', ...opts });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `${res.status} ${res.statusText}`);
  }
  return res.status === 204 ? null : res.json();
}

const receiptURL = (id, ext) =>
  `receipt/${id ? encodeURIComponent(id) : 'home'}.${ext}?days=${state.days}`;

// ---------- Charts ----------

function stack(cats, total, mini) {
  const bar = h('div', { class: 'stack' + (mini ? ' mini' : ''), role: 'img',
    'aria-label': cats.filter((c) => c.count).map((c) => `${c.label} ${pct(c.count / total)}`).join(', ') });
  for (const c of cats) {
    if (!c.count) continue;
    const seg = h('span', { class: catClass(c.id), title: `${c.label}: ${fmt(c.count)} (${pct(c.count / total)})` });
    seg.style.flexGrow = c.count;
    bar.append(seg);
  }
  return bar;
}

function inQuiet(hour, q) {
  if (q.startHour == null) return false;
  return q.startHour <= q.endHour
    ? hour >= q.startHour && hour < q.endHour
    : hour >= q.startHour || hour < q.endHour;
}

function sparkline(d) {
  const W = 240, H = 52, gap = 2, bw = W / 24 - gap;
  const max = Math.max(1, ...d.hourly);
  const q = d.quiet;
  const svg = s('svg', { class: 'spark', viewBox: `0 0 ${W} ${H}`, preserveAspectRatio: 'none',
    role: 'img', 'aria-label': `Lookups by hour of day, busiest at ${String(d.hourly.indexOf(max)).padStart(2, '0')}:00` });
  if (q.startHour != null) {
    const bands = q.startHour <= q.endHour ? [[q.startHour, q.endHour]] : [[q.startHour, 24], [0, q.endHour]];
    for (const [a, b] of bands) {
      svg.append(s('rect', { class: 'band', x: (a * W) / 24, y: 0, width: ((b - a) * W) / 24, height: H, rx: 3 }));
    }
  }
  d.hourly.forEach((n, hr) => {
    const bh = n ? Math.max(1.5, (n / max) * (H - 4)) : 0;
    const bar = s('rect', { class: 'bar' + (inQuiet(hr, q) ? ' night' : ''),
      x: hr * (W / 24) + gap / 2, y: H - bh, width: bw, height: bh, rx: 1.5 });
    bar.append(s('title', {}, `${String(hr).padStart(2, '0')}:00 – ${fmt(n)} lookups`));
    svg.append(bar);
  });
  return h('figure', {},
    svg,
    h('div', { class: 'axis', 'aria-hidden': 'true' }, ...['00', '06', '12', '18', '24'].map((t) => h('span', { text: t }))),
    q.label ? h('figcaption', { class: 'spark-meta' },
      h('span', { text: 'By hour of day' }),
      h('span', {}, icon('moon'), `${fmt(q.lookups)} while you sleep (${q.label})`)) : null);
}

// ---------- Page sections ----------

function renderBanner() {
  const el = document.getElementById('banner');
  el.replaceChildren();
  if (state.report?.demo) {
    el.append(h('p', { class: 'notice' }, icon('info'),
      h('span', {}, h('b', { text: "You're looking at demo data. " }),
        'Point phonehome at your Pi-hole to see your own home.')));
  }
}

function renderHero() {
  const r = state.report;
  const hero = document.getElementById('hero');
  const snoop = r.categories.filter((c) => c.snooping && c.count);
  const rest = r.categories.filter((c) => !c.snooping && c.count);
  const about = r.snooping === 0 ? ' None of it was about you.' : null;
  const blocked = r.devices.reduce((n, d) => n + d.blocked, 0);

  hero.replaceChildren(
    h('h1', {},
      'Your devices called home ', h('strong', { text: fmt(r.total) }), ` times ${PERIODS[state.days]}.`,
      about ?? [' ', h('strong', { class: 'about-you', text: pct(r.snoopShare) }), ' of that was about you.']),
    h('p', { class: 'sub', text: `${r.devices.length} ${r.devices.length === 1 ? 'device' : 'devices'}` +
      ` · ${fmt(r.snooping)} lookups for advertising, tracking and telemetry` +
      (blocked ? ` · ${fmt(blocked)} blocked by your DNS filter` : '') }),
    stack(r.categories, r.total),
    h('div', { class: 'legend' },
      legendGroup('About you', snoop, r.total),
      legendGroup('For you, or not yet known', rest, r.total)),
    h('div', { class: 'hero-actions' },
      h('button', { type: 'button', class: 'btn primary', onclick: () => go('receipt', '') },
        icon('receipt'), 'Home receipt'),
      h('span', { class: 'hint', text: 'A one-page summary you can share.' })));
}

function legendGroup(title, cats, total) {
  if (!cats.length) return null;
  return h('div', { class: 'legend-group' },
    h('h3', { text: title }),
    h('ul', {}, cats.map((c) => h('li', {},
      h('span', { class: 'swatch ' + catClass(c.id) }), c.label, h('b', { text: pct(c.count / total) })))));
}

function renderDevices() {
  const sec = document.getElementById('devices');
  const devs = state.report.devices;
  sec.replaceChildren(
    h('div', { class: 'section-head' },
      h('h2', { id: 'devices-title', text: 'Your devices' }),
      h('p', { text: 'Worst grade first' })),
    h('div', { class: 'grid' }, devs.map(card)));
}

function subtitle(d) {
  const parts = [d.vendor, KINDS[d.kind] ?? KINDS.unknown];
  if (d.privateMac) parts.push('private address');
  return parts.filter(Boolean).join(' · ');
}

function gradeBadge(g) {
  return h('span', { class: 'grade grade-' + (g || 'c').toLowerCase(), role: 'img',
    'aria-label': `Privacy grade ${g}`, title: `Privacy grade ${g}`, text: g });
}

function nameButton(d) {
  const btn = h('button', { type: 'button', class: 'name', 'aria-label': `${d.name}, rename`, title: 'Rename' },
    h('span', { text: d.name }), icon('pencil'));
  btn.addEventListener('click', () => startRename(btn, d));
  return btn;
}

function card(d) {
  const companies = d.companies.slice(0, 3);
  return h('article', { class: 'card', 'data-id': d.id, 'aria-label': d.name },
    h('div', { class: 'dev-head' },
      h('span', { class: 'kind' }, icon(KINDS[d.kind] ? d.kind : 'unknown')),
      h('div', { class: 'dev-title' }, nameButton(d), h('p', { class: 'dev-sub', text: subtitle(d) })),
      gradeBadge(d.grade)),
    h('div', { class: 'figure' },
      h('p', {}, h('span', { class: 'big', text: fmt(d.perDay) }), h('span', { class: 'unit', text: 'snooping lookups a day' })),
      h('p', { class: 'share' }, h('b', { text: pct(d.snoopShare) }), ' of its traffic')),
    d.total ? stack(d.categories, d.total, true) : null,
    sparkline(d),
    d.heartbeats.length ? h('ul', { class: 'chips', 'aria-label': 'Heartbeats' },
      d.heartbeats.slice(0, 3).map((b) => h('li', { class: 'chip ' + catClass(b.category), title: b.domain },
        icon('pulse'), `${b.categoryLabel} · ${every(b.everySeconds)}`))) : null,
    d.bypasses.length ? h('p', { class: 'callout' }, icon('warning'),
      h('span', {}, h('b', { text: 'Can bypass your DNS filter. ' }), 'Seen using ',
        h('code', { text: d.bypasses[0].evidence }), '.')) : null,
    companies.length ? h('div', { class: 'companies' },
      h('span', { class: 'lead', text: 'Talks to' }),
      h('ul', {}, companies.map((c) => h('li', {},
        h('span', { class: 'flag', title: c.country, 'aria-hidden': 'true', text: flag(c.country) }), c.name)),
        d.companies.length > 3 ? h('li', { class: 'muted', text: `+${d.companies.length - 3} more` }) : null)) : null,
    h('div', { class: 'dev-actions' },
      h('button', { type: 'button', class: 'btn', onclick: () => go('device', d.id) }, 'Details', icon('arrow')),
      h('button', { type: 'button', class: 'btn', onclick: () => go('receipt', d.id) }, icon('receipt'), 'Receipt')));
}

// ---------- Rename ----------

function startRename(btn, d) {
  const input = h('input', { type: 'text', maxlength: '64', value: d.label || d.name,
    'aria-label': `New name for ${d.name}`, autocomplete: 'off', spellcheck: 'false' });
  const form = h('form', { class: 'rename' }, input);
  let done = false;
  const finish = async (save) => {
    if (done) return;
    done = true;
    const label = input.value.trim();
    form.replaceWith(btn);
    if (save && label !== d.label && !(label === d.name && !d.label)) await saveLabel(d, label);
    btn.focus();
  };
  form.addEventListener('submit', (e) => { e.preventDefault(); finish(true); });
  input.addEventListener('keydown', (e) => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); finish(false); } });
  input.addEventListener('blur', () => finish(true));
  btn.replaceWith(form);
  input.focus();
  input.select();
}

async function saveLabel(d, label) {
  try {
    await api(`api/devices/${encodeURIComponent(d.id)}/label`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ label }),
    });
  } catch (err) {
    toast(`Couldn't rename: ${err.message}`);
    return;
  }
  d.label = label;
  d.name = label || d.defaultName;
  for (const b of document.querySelectorAll('.name')) {
    if (b.closest('[data-id]')?.dataset.id !== d.id) continue;
    b.replaceWith(nameButton(d));
  }
  document.querySelector(`.card[data-id="${CSS.escape(d.id)}"]`)?.setAttribute('aria-label', d.name);
  toast(label ? `Renamed to “${label}”` : `Name reset to “${d.name}”`);
}

// ---------- Details drawer ----------

function regularity(j) {
  if (j < 0.1) return 'like clockwork';
  if (j < 0.3) return 'very regular';
  return 'roughly regular';
}

function details(d) {
  const sections = [];
  sections.push(h('dl', { class: 'stats' },
    stat('Lookups', fmt(d.total)),
    stat('About you', fmt(d.snooping), pct(d.snoopShare)),
    stat('Blocked', fmt(d.blocked), d.total ? pct(d.blocked / d.total) : null),
    stat('While you sleep', fmt(d.quiet.lookups))));

  if (d.bypasses.length) {
    sections.push(h('section', {}, h('h3', { text: 'Bypassing your DNS' }),
      h('div', { class: 'bypasses' }, d.bypasses.map((b) => h('p', { class: 'callout' }, icon('warning'),
        h('span', {}, b.detail, ' Evidence: ', h('code', { text: b.evidence }), ` (${b.confidence} confidence).`))))));
  }

  sections.push(h('section', {}, h('h3', { text: 'When it calls' }), sparkline(d)));

  if (d.heartbeats.length) {
    sections.push(h('section', {}, h('h3', { text: 'Heartbeats — calls on a timer, whether you use it or not' }),
      h('ul', { class: 'beats' }, d.heartbeats.map((b) => h('li', {},
        h('span', { class: 'pill ' + catClass(b.category), text: b.categoryLabel }),
        h('span', { class: 'domain', text: b.domain }),
        h('span', { class: 'meta', text: `${every(b.everySeconds)} · ${fmt(b.count)} times · ${regularity(b.jitter)}` }))))));
  }

  sections.push(h('section', {}, h('h3', { text: 'Where it calls' }),
    d.topDomains.length ? h('div', { class: 'table-wrap' }, h('table', {},
      h('thead', {}, h('tr', {},
        h('th', { scope: 'col', text: 'Domain' }), h('th', { scope: 'col', class: 'company', text: 'Company' }),
        h('th', { scope: 'col', text: 'Category' }), h('th', { scope: 'col', class: 'num', text: 'Lookups' }),
        h('th', { scope: 'col', class: 'num', text: 'Blocked' }))),
      h('tbody', {}, d.topDomains.map((t) => h('tr', {},
        h('td', {}, h('div', { class: 'domain', text: t.domain }), t.purpose ? h('div', { class: 'purpose', text: t.purpose }) : null),
        h('td', { class: 'company', text: t.companyName || '—' }),
        h('td', {}, h('span', { class: 'pill ' + catClass(t.category), text: t.categoryLabel })),
        h('td', { class: 'num', text: fmt(t.count) }),
        h('td', { class: 'num' + (t.blocked ? '' : ' muted'), text: t.blocked ? fmt(t.blocked) : '—' }))))))
      : h('p', { class: 'empty-note', text: 'No lookups in this period.' })));

  sections.push(h('section', {}, h('h3', { text: 'What you can do' }),
    d.fixes.length ? h('ol', { class: 'fixes' }, d.fixes.map((f) => h('li', {},
      h('h4', { text: f.title }),
      f.steps.length ? h('ol', {}, f.steps.map((st) => h('li', { text: st }))) : null,
      f.notes ? h('p', { class: 'notes', text: f.notes }) : null,
      f.evidence.length ? h('p', { class: 'evidence' }, f.evidence.map((u, i) =>
        h('a', { href: u, target: '_blank', rel: 'noopener noreferrer' },
          f.evidence.length > 1 ? `Source ${i + 1}` : 'Source', icon('external')))) : null)))
      : h('p', { class: 'empty-note', text: 'No specific fixes known for this device yet. Blocking the snooping domains above in your DNS filter is a good start.' })));

  if (d.unknown.length) sections.push(unclassified(d));

  sections.push(h('p', { class: 'footnote' }, icon('lock'),
    h('span', { text: 'phonehome sees who and when, never what — traffic is encrypted. ' +
      'Purposes come from public research and vendor documentation; links are next to each fix.' })));

  const dlg = document.getElementById('drawer');
  dlg.replaceChildren(
    h('header', { class: 'drawer-head' },
      h('span', { class: 'kind' }, icon(KINDS[d.kind] ? d.kind : 'unknown')),
      h('div', { class: 'dev-title' },
        h('h2', { id: 'drawer-title', tabindex: '-1', text: d.name }),
        h('p', { class: 'dev-sub', text: [subtitle(d), d.ips[0]].filter(Boolean).join(' · ') })),
      gradeBadge(d.grade),
      h('button', { type: 'button', class: 'icon-btn', 'aria-label': 'Close', onclick: () => dlg.close() }, icon('close'))),
    h('div', { class: 'drawer-body' }, sections));
  return dlg;
}

// unclassified lists domains the knowledge base can't explain yet and offers a
// prefilled GitHub issue. The link is built server-side (internal/web/suggest.go)
// from domain names and the device's make and type only; phonehome never
// fetches it, the person clicks it.
function unclassified(d) {
  const top = d.unknown.slice(0, 8);
  const more = d.unknown.length - top.length;
  return h('section', { class: 'unclassified' },
    h('h3', { text: 'Unclassified' }),
    h('p', { class: 'lead-note', text: "phonehome doesn't know what these are for yet. If you do, a rule makes everyone's report better." }),
    h('ul', { class: 'unknown' }, top.map((u) => h('li', {},
      h('span', { class: 'domain', text: u.domain }),
      h('span', { class: 'meta', text: `${fmt(u.count)} lookups · last ${ago(u.lastSeen)}` })))),
    more > 0 ? h('p', { class: 'more' }, `+${more} more. Run `, h('code', { text: 'phonehome unknown' }), ' to see them all.') : null,
    d.suggestUrl ? h('div', { class: 'suggest' },
      h('a', { class: 'btn', href: d.suggestUrl, target: '_blank', rel: 'noopener noreferrer' }, 'Suggest a rule', icon('external')),
      h('p', { class: 'share-note', text: 'Opens a public issue form on GitHub. Your browser sends GitHub these domain names ' +
        "(ID-like parts replaced with *) and the device's make and type — no addresses, MACs or device names. " +
        'Check it, add what you know, then submit.' }))
      : state.report.demo ? h('p', { class: 'share-note', text: 'With your own data, a “Suggest a rule” link here opens a prefilled GitHub issue. It is off for demo data.' })
        : null);
}

function stat(label, value, extra) {
  return h('div', {}, h('dt', { text: label }), h('dd', {}, value, extra ? h('small', { text: extra }) : null));
}

// ---------- Receipt modal ----------

function receipt(id) {
  const d = id ? state.report.devices.find((x) => x.id === id) : null;
  const name = d ? d.name : 'Your home';
  const dlg = document.getElementById('receipt');
  const slug = (d ? d.name : 'home').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'device';
  const canCopy = window.isSecureContext && navigator.clipboard && 'ClipboardItem' in window;
  dlg.replaceChildren(
    h('div', { class: 'modal-head' },
      h('div', {}, h('h2', { id: 'receipt-title', tabindex: '-1', text: 'Privacy receipt' }),
        h('p', { text: `${name} · ${PERIODS[state.days]}` })),
      h('button', { type: 'button', class: 'icon-btn', 'aria-label': 'Close', onclick: () => dlg.close() }, icon('close'))),
    h('div', { class: 'paper' }, h('img', { src: receiptURL(id, 'svg'), alt: `Privacy receipt for ${name}` })),
    h('div', { class: 'modal-actions' },
      h('a', { class: 'btn primary', href: receiptURL(id, 'png'), download: `phonehome-${slug}-${state.days}d.png` },
        icon('download'), 'Download PNG'),
      canCopy ? h('button', { type: 'button', class: 'btn', onclick: () => copyReceipt(id) }, icon('copy'), 'Copy image') : null),
    h('p', { class: 'nudge', text: d
      ? 'Share it. Most people have no idea their devices do this.'
      : 'Share it. Most people have no idea their home does this.' }));
  return dlg;
}

async function copyReceipt(id) {
  try {
    const blob = fetch(receiptURL(id, 'png')).then((r) => {
      if (!r.ok) throw new Error(r.statusText);
      return r.blob();
    });
    await navigator.clipboard.write([new ClipboardItem({ 'image/png': blob })]);
    toast('Receipt copied. Paste it anywhere.');
  } catch {
    toast("Your browser wouldn't copy the image. Use Download PNG instead.");
  }
}

// ---------- Routing: #device=<id> and #receipt=<id|home> ----------

function go(kind, id) {
  location.hash = `${kind}=${encodeURIComponent(id || 'home')}`;
}

function route() {
  if (!state.report) return;
  const m = /^#(device|receipt)=(.+)$/.exec(location.hash);
  for (const dlg of document.querySelectorAll('dialog[open]')) dlg.close();
  if (!m) return;
  const id = decodeURIComponent(m[2]);
  if (m[1] === 'receipt') {
    openDialog(receipt(id === 'home' ? '' : id));
    return;
  }
  const d = state.report.devices.find((x) => x.id === id);
  if (d) openDialog(details(d));
}

// openDialog shows a dialog with focus on its title, so screen readers announce it
// and no button looks pre-selected.
function openDialog(dlg) {
  dlg.showModal();
  dlg.scrollTop = 0;
  dlg.querySelector('h2').focus();
}

function clearHash() {
  if (location.hash && !document.querySelector('dialog[open]')) {
    history.replaceState(null, '', location.pathname + location.search);
  }
}

// ---------- Empty state ----------

function renderOnboarding() {
  document.getElementById('devices').replaceChildren();
  const tabs = Object.keys(SNIPPETS);
  const panel = h('div', { class: 'snippet', role: 'tabpanel', id: 'snippet' });
  const buttons = tabs.map((name, i) => h('button', {
    type: 'button', role: 'tab', id: `tab-${i}`, 'aria-controls': 'snippet', text: name,
    onclick: () => select(i),
    onkeydown: (e) => {
      const step = { ArrowRight: 1, ArrowLeft: -1 }[e.key];
      if (step) { select((i + step + tabs.length) % tabs.length); buttons[(i + step + tabs.length) % tabs.length].focus(); }
    },
  }));
  function select(i) {
    buttons.forEach((b, j) => { b.setAttribute('aria-selected', String(i === j)); b.tabIndex = i === j ? 0 : -1; });
    panel.setAttribute('aria-labelledby', `tab-${i}`);
    const snip = SNIPPETS[tabs[i]];
    panel.replaceChildren(h('p', { text: snip.note }), ...snip.code.map((c) => h('pre', {}, h('code', { text: c }))));
  }
  select(0);

  document.getElementById('hero').replaceChildren(h('div', { class: 'onboard' },
    h('div', {},
      h('h1', { text: "Let's see who your devices are calling." }),
      h('p', { class: 'lede', text: 'Every time a device on your network looks up a name, your DNS server writes it down. ' +
        'phonehome reads that log — it never changes it — and turns it into plain English.' })),
    h('ol', { class: 'steps' },
      h('li', {}, h('h2', { text: 'Point phonehome at your DNS log' }),
        h('p', { text: 'Add a source to phonehome.yaml:' }),
        h('div', {}, h('div', { class: 'tabs', role: 'tablist', 'aria-label': 'DNS server' }, buttons), panel)),
      h('li', {}, h('h2', { text: 'Import what is already there' }),
        h('p', {}, 'Run ', h('code', { text: 'phonehome ingest --once' }), ', or just leave ',
          h('code', { text: 'phonehome serve' }), ' running. This page fills in on its own.')),
      h('li', {}, h('h2', { text: 'Give it a day' }),
        h('p', { text: 'Heartbeats and night-time chatter only show up once phonehome has seen a full day.' }))),
    h('p', { class: 'notice' }, icon('info'),
      h('span', {}, 'Just want to look around? Run ', h('code', { text: 'phonehome demo' }), ' to explore a made-up household.'))));
}

// ---------- Footer ----------

function renderHealth() {
  const st = state.status;
  const el = document.getElementById('health');
  if (!st) { el.replaceChildren(); return; }
  const items = st.sources.map((src) => h('li', { title: src.lastError || '' },
    h('span', { class: 'dot ' + src.health, role: 'img', 'aria-label': src.health === 'ok' ? 'healthy' : src.health }),
    h('b', { text: src.name }),
    `${fmt(src.records)} records · ${src.health === 'error' ? 'failing: ' + src.lastError : 'updated ' + ago(src.lastOk)}`));
  if (!items.length) {
    items.push(h('li', {}, h('span', { class: 'dot ' + (st.demo ? 'ok' : 'stale'), 'aria-hidden': 'true' }),
      st.demo ? 'Demo data' : 'No sources yet'));
  }
  if (st.oldest) items.push(h('li', { text: `Data since ${new Date(st.oldest).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })}` }));
  if (st.version) items.push(h('li', { text: `v${st.version.replace(/^v/, '')}` }));
  el.replaceChildren(h('ul', { class: 'health' }, items));
}

// ---------- Loading ----------

function setPeriodButtons() {
  for (const b of document.querySelectorAll('.period button')) {
    const on = Number(b.dataset.days) === state.days;
    b.setAttribute('aria-checked', String(on));
    b.tabIndex = on ? 0 : -1;
  }
}

async function load() {
  setPeriodButtons();
  try {
    const [report, status] = await Promise.all([
      api(`api/report?days=${state.days}`),
      api('api/status').catch(() => null),
    ]);
    state.report = report;
    state.status = status;
    state.cats = new Map(report.categories.map((c) => [c.id, c]));
  } catch (err) {
    document.getElementById('hero').replaceChildren(h('div', { class: 'error-state' },
      h('h1', { text: "phonehome couldn't load your report." }),
      h('p', { text: err.message }),
      h('button', { type: 'button', class: 'btn', onclick: load, text: 'Try again' })));
    return;
  }
  renderBanner();
  document.getElementById('hero').classList.toggle('is-empty', !state.report.devices.length);
  if (state.report.devices.length) {
    renderHero();
    renderDevices();
  } else {
    renderOnboarding();
  }
  renderHealth();
  route();
}

function setDays(days) {
  if (days === state.days) return;
  state.days = days;
  const url = new URL(location.href);
  url.searchParams.set('days', days);
  history.replaceState(null, '', url);
  try { localStorage.setItem('phonehome.days', days); } catch { /* private mode */ }
  load();
}

function init() {
  const fromURL = Number(new URLSearchParams(location.search).get('days'));
  let saved = 0;
  try { saved = Number(localStorage.getItem('phonehome.days')); } catch { /* private mode */ }
  state.days = [fromURL, saved].find((d) => PERIODS[d]) ?? 7;

  const buttons = [...document.querySelectorAll('.period button')];
  buttons.forEach((b, i) => {
    b.addEventListener('click', () => setDays(Number(b.dataset.days)));
    b.addEventListener('keydown', (e) => {
      const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key];
      if (!step) return;
      e.preventDefault();
      const next = buttons[(i + step + buttons.length) % buttons.length];
      next.focus();
      setDays(Number(next.dataset.days));
    });
  });

  for (const dlg of document.querySelectorAll('dialog')) {
    dlg.addEventListener('close', clearHash);
    dlg.addEventListener('click', (e) => {
      const r = dlg.getBoundingClientRect();
      const inside = e.clientX >= r.left && e.clientX <= r.right && e.clientY >= r.top && e.clientY <= r.bottom;
      if (e.target === dlg && !inside) dlg.close();
    });
  }
  window.addEventListener('hashchange', route);
  load();
}

init();
