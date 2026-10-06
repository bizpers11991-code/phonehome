// phonehome dashboard. Vanilla JS, no build step, no network beyond this server.
// Every user-facing string comes from the catalog in i18n.js, through t().
'use strict';

// First-run snippets. Keep in sync with internal/config.
const SNIPPETS = {
  'Pi-hole': {
    note: 'snip.pihole',
    code: ['sources:\n  - type: pihole-db\n    path: /etc/pihole/pihole-FTL.db'],
  },
  'AdGuard Home': {
    note: 'snip.adguard',
    code: ['sources:\n  - type: adguard-querylog\n    path: /opt/AdGuardHome/data/querylog.json'],
  },
  dnsmasq: {
    note: 'snip.dnsmasq',
    code: [
      '# /etc/dnsmasq.conf\nlog-queries\nlog-facility=/var/log/dnsmasq.log',
      'sources:\n  - type: dnsmasq-log\n    path: /var/log/dnsmasq.log',
    ],
  },
};

const KINDS = new Set(['tv', 'streamer', 'speaker', 'camera', 'vacuum', 'plug', 'hub', 'appliance',
  'console', 'phone', 'computer', 'network', 'unknown']);
const kindLabel = (k) => t('kind.' + (KINDS.has(k) ? k : 'unknown'));

const PERIODS = [1, 7, 30];

const state = { days: 7, report: null, status: null, cats: new Map() };

// ---------- Language ----------

let LANG = 'en';

// pickLang prefers a saved choice, then the browser's languages in order.
function pickLang() {
  let saved = '';
  try { saved = localStorage.getItem('phonehome.lang') || ''; } catch { /* private mode */ }
  for (const tag of [saved, ...(navigator.languages || [navigator.language])]) {
    const base = String(tag || '').toLowerCase().split('-')[0];
    if (MESSAGES[base]) return base;
  }
  return 'en';
}

function message(key) { return MESSAGES[LANG][key] ?? MESSAGES.en[key] ?? key; }

// A plural message is chosen by vars.count (the raw number behind {n}).
function plural(m, vars) {
  if (typeof m !== 'object') return m;
  return m[new Intl.PluralRules(LANG).select(Number(vars?.count ?? 0))] ?? m.other;
}

// t renders a message as text.
function t(key, vars) {
  return plural(message(key), vars).replace(/\{(\w+)\}/g, (all, k) => (vars && k in vars ? String(vars[k]) : all));
}

// tn renders a message as a list of strings and elements, so placeholders
// can hold <code> or <strong> without building HTML from strings.
function tn(key, vars) {
  const m = plural(message(key), vars);
  const out = [];
  let last = 0;
  m.replace(/\{(\w+)\}/g, (all, k, at) => {
    out.push(m.slice(last, at), vars && k in vars ? vars[k] : all);
    last = at + all.length;
    return all;
  });
  out.push(m.slice(last));
  return out.filter((x) => x !== '');
}

// Category names inside a sentence: lower case, except in German, which
// capitalises nouns.
const catInline = (id) => (LANG === 'de' ? catLabel(id) : catLabel(id).toLowerCase());

// applyStatic translates the page's fixed text (data-i18n attributes).
function applyStatic() {
  document.documentElement.lang = LANG;
  for (const el of document.querySelectorAll('[data-i18n]')) el.textContent = t(el.dataset.i18n);
  for (const el of document.querySelectorAll('[data-i18n-label]')) el.setAttribute('aria-label', t(el.dataset.i18nLabel));
  const sel = document.getElementById('lang');
  if (sel) sel.value = LANG;
}

function setLang(lang) {
  if (!MESSAGES[lang] || lang === LANG) return;
  LANG = lang;
  try { localStorage.setItem('phonehome.lang', lang); } catch { /* private mode */ }
  formats();
  applyStatic();
  if (state.report) {
    render();
    route(); // an open dialog is drawn again in the new language
  }
}

// ---------- DOM helpers (textContent only: domains and labels are untrusted) ----------

function h(tag, props, ...kids) {
  const el = document.createElement(tag);
  setProps(el, props);
  el.append(...kids.flat(Infinity).filter((k) => k != null && k !== false)); // nested lists too: never stringify an element
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

let nf, pf, rtf, df, dtf;
function formats() {
  nf = new Intl.NumberFormat(LANG);
  pf = new Intl.NumberFormat(LANG, { style: 'percent' });
  rtf = new Intl.RelativeTimeFormat(LANG, { numeric: 'auto' });
  df = new Intl.DateTimeFormat(LANG, { month: 'short', day: 'numeric', year: 'numeric' });
  dtf = new Intl.DateTimeFormat(LANG, { dateStyle: 'medium', timeStyle: 'short' });
}
const fmt = (n) => nf.format(Math.round(n));
const when = (ts) => (ts ? dtf.format(new Date(ts)) : '—');

function pct(x) {
  if (x > 0 && x < 0.01) return '<' + pf.format(0.01);
  return pf.format(Math.round(x * 100) / 100);
}

function flag(cc) {
  if (!/^[A-Za-z]{2}$/.test(cc || '')) return '';
  return String.fromCodePoint(...[...cc.toUpperCase()].map((c) => 0x1f1a5 + c.charCodeAt(0)));
}

function every(sec) {
  if (sec < 90) return t('every.s', { n: Math.round(sec) });
  if (sec < 5400) return t('every.min', { n: Math.round(sec / 60) });
  return t('every.h', { n: Math.round(sec / 3600) });
}

// ---------- Compared with the previous period ----------
// The server only sends `previous` when the period before has enough data
// (docs/grading.md); these helpers word it so the direction never relies on
// colour alone.

function prevLabel(p) {
  const d = Math.round(p.days * 10) / 10;
  return d === 1 ? t('prev.day') : t('prev.days', { n: nf.format(d) });
}

function trend(p) {
  if (!p.seen) return h('span', { class: 'trend new', text: t('trend.new') });
  if (p.change == null) return h('span', { class: 'trend worse' }, h('span', { 'aria-hidden': 'true', text: '↑ ' }), t('trend.upFromNone'));
  const n = Math.round(p.change * 100);
  if (n === 0) return h('span', { class: 'trend same', text: t('trend.same') });
  const down = n < 0;
  return h('span', { class: 'trend ' + (down ? 'better' : 'worse') },
    h('span', { 'aria-hidden': 'true', text: down ? '↓ ' : '↑ ' }),
    pct(Math.abs(n) / 100), h('span', { class: 'visually-hidden', text: t(down ? 'trend.less' : 'trend.more') }));
}

function gradeShift(before, now) {
  return h('span', { class: 'grade-shift', 'aria-label': t('grade.shift', { before: before || t('grade.none'), now: now || t('grade.none') }) },
    h('b', { class: 'mini-grade grade-' + (before || 'c').toLowerCase(), text: before || '?' }),
    h('span', { 'aria-hidden': 'true', text: ' → ' }),
    h('b', { class: 'mini-grade grade-' + (now || 'c').toLowerCase(), text: now || '?' }));
}

function stoppedList(beats, max) {
  if (!beats.length) return null;
  const top = beats.slice(0, max);
  return h('ul', { class: 'stopped', 'aria-label': t('stopped.aria') },
    top.map((b) => h('li', {}, icon('pulse'), h('span', {},
      ...tn('stopped.item', { domain: h('code', { text: b.domain }), heartbeat: t('heartbeat', { category: catInline(b.category) }) })))),
    beats.length > max ? h('li', { class: 'muted', text: t('more', { n: fmt(beats.length - max), count: beats.length - max }) }) : null);
}

// sinceCard is the one-line change on a device card.
function sinceCard(d) {
  const p = d.previous;
  if (!p) return null;
  if (!p.seen) return h('div', { class: 'since' }, h('p', {}, trend(p), t('since.notSeen', { prev: prevLabel(p) })));
  return h('div', { class: 'since' },
    h('p', {}, trend(p), t('since.vs', { prev: prevLabel(p) }), p.grade !== d.grade ? [' · ', gradeShift(p.grade, d.grade)] : null),
    stoppedList(p.stopped, 1));
}

function ago(ts) {
  if (!ts) return t('never');
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
function catLabel(id) { return t('cat.' + (state.cats.has(id) || MESSAGES.en['cat.' + id] ? id : 'unknown')); }

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
    'aria-label': cats.filter((c) => c.count).map((c) => `${catLabel(c.id)} ${pct(c.count / total)}`).join(', ') });
  for (const c of cats) {
    if (!c.count) continue;
    const seg = h('span', { class: catClass(c.id), title: t('stack.title', { label: catLabel(c.id), count: fmt(c.count), pct: pct(c.count / total) }) });
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
    role: 'img', 'aria-label': t('spark.aria', { hour: String(d.hourly.indexOf(max)).padStart(2, '0') }) });
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
    bar.append(s('title', {}, t('spark.bar', { hour: String(hr).padStart(2, '0'), n: fmt(n), count: n })));
    svg.append(bar);
  });
  return h('figure', {},
    svg,
    h('div', { class: 'axis', 'aria-hidden': 'true' }, ...['00', '06', '12', '18', '24'].map((t) => h('span', { text: t }))),
    q.label ? h('figcaption', { class: 'spark-meta' },
      h('span', { text: t('spark.byHour') }),
      h('span', {}, icon('moon'), t('spark.sleep', { n: fmt(q.lookups), count: q.lookups, window: q.label }))) : null);
}

// ---------- Page sections ----------

function renderBanner() {
  const el = document.getElementById('banner');
  el.replaceChildren();
  if (state.report?.demo) {
    el.append(h('p', { class: 'notice' }, icon('info'),
      h('span', {}, h('b', { text: t('banner.demo.b') }), t('banner.demo'))));
  }
}

function renderHero() {
  const r = state.report;
  const hero = document.getElementById('hero');
  const snoop = r.categories.filter((c) => c.snooping && c.count);
  const rest = r.categories.filter((c) => !c.snooping && c.count);
  const blocked = r.devices.reduce((n, d) => n + d.blocked, 0);

  hero.replaceChildren(
    h('h1', {},
      ...tn('hero.title', { total: h('strong', { text: fmt(r.total) }), when: t('when.' + state.days), count: r.total }), ' ',
      ...(r.snooping === 0 ? [t('hero.none')]
        : tn('hero.about', { share: h('strong', { class: 'about-you', text: pct(r.snoopShare) }) }))),
    h('p', { class: 'sub', text: [
      t('hero.devices', { n: fmt(r.devices.length), count: r.devices.length }),
      t('hero.snooping', { n: fmt(r.snooping), count: r.snooping }),
      blocked ? t('hero.blocked', { n: fmt(blocked), count: blocked }) : null,
    ].filter(Boolean).join(' · ') }),
    sinceHero(r) ?? '', // replaceChildren would print null
    stack(r.categories, r.total),
    h('div', { class: 'legend' },
      legendGroup(t('legend.about'), snoop, r.total),
      legendGroup(t('legend.rest'), rest, r.total)),
    h('div', { class: 'hero-actions' },
      h('button', { type: 'button', class: 'btn primary', onclick: () => go('receipt', '') },
        icon('receipt'), t('hero.receipt')),
      h('span', { class: 'hint', text: t('hero.receiptHint') })),
    h('div', { class: 'hero-actions export' },
      h('span', { class: 'lead', id: 'export-label', text: t('export.label') }),
      h('button', { type: 'button', class: 'btn', 'aria-describedby': 'export-hint', onclick: () => exportReport('csv') },
        icon('download'), t('export.csv')),
      h('button', { type: 'button', class: 'btn', 'aria-describedby': 'export-hint', onclick: () => exportReport('json') },
        icon('download'), t('export.json')),
      h('span', { class: 'hint', id: 'export-hint', text: t('export.hint') })));
}

// sinceHero sums up the change for the whole home.
function sinceHero(r) {
  const p = r.previous;
  if (!p) return null;
  // One line per device: "Living Room TV: 4 heartbeats stopped, incl. acr-…".
  const stopped = r.devices.filter((d) => d.previous?.stopped.length).map((d) => d.previous.stopped);
  const notes = [];
  const days = (x) => nf.format(Math.round(x * 10) / 10);
  if (p.partial) notes.push(t('since.partial', { data: days(p.dataDays), days: days(p.days) }));
  if (p.gone.length) notes.push(t('since.gone', { names: p.gone.map((g) => g.name).join(', ') }));
  return h('div', { class: 'since since-hero' },
    h('p', {}, trend(p), t('since.vs', { prev: prevLabel(p) }), ' ',
      h('span', { class: 'muted', text: t('since.perDay', { before: fmt(p.perDay), now: fmt(p.nowPerDay) }) }),
      t('since.homeGrade'), gradeShift(p.grade, r.grade)),
    stopped.length ? h('ul', { class: 'stopped', 'aria-label': t('stopped.aria') },
      r.devices.filter((d) => d.previous?.stopped.length).slice(0, 3).map((d) => {
        const bs = d.previous.stopped;
        return h('li', {}, icon('pulse'), h('span', {},
          ...tn('since.stoppedLine', { device: h('b', { text: d.name }), domain: h('code', { text: bs[0].domain }),
            heartbeat: t('heartbeat', { category: catInline(bs[0].category) }) }),
          bs.length > 1 ? t('since.andMore', { n: fmt(bs.length - 1), count: bs.length - 1 }) : ''));
      })) : null,
    notes.length ? h('p', { class: 'since-note', text: notes.join(' ') }) : null);
}

function legendGroup(title, cats, total) {
  if (!cats.length) return null;
  return h('div', { class: 'legend-group' },
    h('h3', { text: title }),
    h('ul', {}, cats.map((c) => h('li', {},
      h('span', { class: 'swatch ' + catClass(c.id) }), catLabel(c.id), h('b', { text: pct(c.count / total) })))));
}

function renderDevices() {
  const sec = document.getElementById('devices');
  const devs = state.report.devices;
  const grid = h('div', { class: 'grid', role: 'list', 'aria-describedby': 'devices-keys', onkeydown: cardKeys },
    devs.map((d, i) => card(d, i)));
  sec.replaceChildren(
    h('div', { class: 'section-head' },
      h('h2', { id: 'devices-title', text: t('devices.title') }),
      h('p', { text: t('devices.order') })),
    h('p', { class: 'visually-hidden', id: 'devices-keys', text: t('devices.keys') }),
    grid);
}

// cardKeys moves between device cards with the arrow keys (a roving
// tabindex: one card is in the tab order at a time) and opens one with
// Enter. Keys pressed on the buttons inside a card keep their usual meaning.
function cardKeys(e) {
  const cardEl = e.target.closest('.card');
  if (!cardEl || e.target !== cardEl) return;
  const cards = [...e.currentTarget.querySelectorAll('.card')];
  const i = cards.indexOf(cardEl);
  const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key];
  let next = null;
  if (step) next = cards[(i + step + cards.length) % cards.length];
  else if (e.key === 'Home') next = cards[0];
  else if (e.key === 'End') next = cards[cards.length - 1];
  else if (e.key === 'Enter') { e.preventDefault(); go('device', cardEl.dataset.id); return; }
  if (!next) return;
  e.preventDefault();
  cardEl.tabIndex = -1;
  next.tabIndex = 0;
  next.focus();
}

function subtitle(d) {
  const parts = [d.vendor, kindLabel(d.kind)];
  if (d.privateMac) parts.push(t('sub.private'));
  return parts.filter(Boolean).join(' · ');
}

function gradeBadge(g) {
  return h('span', { class: 'grade grade-' + (g || 'c').toLowerCase(), role: 'img',
    'aria-label': t('grade.aria', { grade: g }), title: t('grade.aria', { grade: g }), text: g });
}

// ---------- Why a grade ----------
// The server sends the rule that decided the grade and its numbers
// (docs/grading.md); these word it. Its English text is the fallback for
// rules the catalog does not know.

function band(r) {
  if (!r.bandFrom) return t('band.under', { grade: r.volumeGrade, to: fmt(r.bandTo) });
  if (!r.bandTo) return t('band.over', { grade: r.volumeGrade, from: fmt(r.bandFrom) });
  return t('band.range', { grade: r.volumeGrade, from: fmt(r.bandFrom), last: fmt(r.bandTo - 1) });
}

function reasonParts(r) {
  const n = r.lookups;
  switch (r.rule) {
    case 'volume': return tn('why.volume', { n: fmt(n), count: n, band: band(r) });
    case 'acr-heartbeat': return tn('why.acrBeat', { every: every(r.everySeconds) });
    case 'acr': return tn('why.acr', { n: fmt(r.count), count: r.count });
    case 'bypass': return tn('why.bypass', { evidence: h('code', { text: r.evidence }), n: fmt(n), count: n, volume: r.volumeGrade });
  }
  return r.text ? [r.text + '.'] : [];
}

// coveragePct formats the server's coveragePercent, rounded down there so
// that 49.6% never reads as the 50% it falls short of.
const coveragePct = (d) => pf.format(d.coveragePercent / 100);

// gradeWhy is the one line under a grade: what decided it, and whether it
// rests on too little data or on a minority of the device's lookups.
function gradeWhy(d) {
  const parts = reasonParts(d.reason);
  if (!parts.length) return null;
  const notes = [];
  if (d.reason.provisional) {
    const hours = Math.floor(d.reason.dataHours);
    notes.push(hours >= 1 ? t('why.provisional', { n: fmt(hours), count: hours }) : t('why.provisionalSoon'));
  }
  if (d.lowCoverage) notes.push(t('why.coverage', { pct: coveragePct(d) }));
  return h('p', { class: 'why' }, h('b', { text: t('why.label', { grade: d.grade }) }), ' ', ...parts,
    notes.length ? h('span', { class: 'why-note', text: ' ' + notes.join(' ') }) : null);
}

function nameButton(d) {
  const btn = h('button', { type: 'button', class: 'name', 'aria-label': t('rename.aria', { name: d.name }), title: t('rename.title') },
    h('span', { text: d.name }), icon('pencil'));
  btn.addEventListener('click', () => startRename(btn, d));
  return btn;
}

function card(d, i) {
  const companies = d.companies.slice(0, 3);
  return h('article', { class: 'card', role: 'listitem', tabindex: i === 0 ? '0' : '-1', 'data-id': d.id, 'aria-label': d.name },
    h('div', { class: 'dev-head' },
      h('span', { class: 'kind' }, icon(KINDS.has(d.kind) ? d.kind : 'unknown')),
      h('div', { class: 'dev-title' }, nameButton(d), h('p', { class: 'dev-sub', text: subtitle(d) })),
      gradeBadge(d.grade)),
    gradeWhy(d),
    h('div', { class: 'figure' },
      h('p', {}, h('span', { class: 'big', text: fmt(d.perDay) }), h('span', { class: 'unit', text: t('card.perDay') })),
      h('p', { class: 'share' }, ...tn('card.share', { share: h('b', { text: pct(d.snoopShare) }) }))),
    sinceCard(d),
    d.total ? stack(d.categories, d.total, true) : null,
    sparkline(d),
    d.heartbeats.length ? h('ul', { class: 'chips', 'aria-label': t('card.heartbeats') },
      d.heartbeats.slice(0, 3).map((b) => h('li', { class: 'chip ' + catClass(b.category), title: b.domain },
        icon('pulse'), `${catLabel(b.category)} · ${every(b.everySeconds)}`))) : null,
    d.bypasses.length ? h('p', { class: 'callout' }, icon('warning'),
      h('span', {}, h('b', { text: t('card.bypass.b') }), ...tn('card.bypass', { evidence: h('code', { text: d.bypasses[0].evidence }) }))) : null,
    companies.length ? h('div', { class: 'companies' },
      h('span', { class: 'lead', text: t('card.talksTo') }),
      h('ul', {}, companies.map((c) => h('li', {},
        h('span', { class: 'flag', title: c.country, 'aria-hidden': 'true', text: flag(c.country) }), c.name)),
        d.companies.length > 3 ? h('li', { class: 'muted', text: t('more', { n: fmt(d.companies.length - 3), count: d.companies.length - 3 }) }) : null)) : null,
    h('div', { class: 'dev-actions' },
      h('button', { type: 'button', class: 'btn', onclick: () => go('device', d.id) }, t('card.details'), icon('arrow')),
      h('button', { type: 'button', class: 'btn', onclick: () => go('receipt', d.id) }, icon('receipt'), t('card.receipt'))));
}

// ---------- Rename ----------

function startRename(btn, d) {
  const input = h('input', { type: 'text', maxlength: '64', value: d.label || d.name,
    'aria-label': t('rename.input', { name: d.name }), autocomplete: 'off', spellcheck: 'false' });
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
    toast(t('toast.renameFailed', { error: err.message }));
    return;
  }
  d.label = label;
  d.name = label || d.defaultName;
  for (const b of document.querySelectorAll('.name')) {
    if (b.closest('[data-id]')?.dataset.id !== d.id) continue;
    b.replaceWith(nameButton(d));
  }
  document.querySelector(`.card[data-id="${CSS.escape(d.id)}"]`)?.setAttribute('aria-label', d.name);
  toast(label ? t('toast.renamed', { label }) : t('toast.reset', { name: d.name }));
}

// ---------- Details drawer ----------

function regularity(j) {
  if (j < 0.1) return t('reg.clockwork');
  if (j < 0.3) return t('reg.very');
  return t('reg.rough');
}

// bypassText words a bypass finding in the current language; the server's
// English sentence is the fallback for kinds the catalog does not know.
function bypassText(b) {
  return MESSAGES.en['bypass.' + b.kind] ? tn('bypass.' + b.kind, { evidence: h('code', { text: b.evidence }) }) : [b.detail];
}

function details(d) {
  const sections = [];
  const why = gradeWhy(d);
  if (why) {
    sections.push(h('div', { class: 'why-block' }, why,
      d.acrUnseen ? h('p', { class: 'why-note', text: t('why.acrUnseen') }) : null));
  }
  sections.push(h('dl', { class: 'stats' },
    stat(t('stat.lookups'), fmt(d.total), d.total ? t('stat.recognised', { pct: coveragePct(d) }) : null),
    stat(t('stat.about'), fmt(d.snooping), pct(d.snoopShare)),
    stat(t('stat.blocked'), fmt(d.blocked), d.total ? pct(d.blocked / d.total) : null),
    stat(t('stat.sleep'), fmt(d.quiet.lookups))));

  if (d.previous) sections.push(sinceDetails(d));

  if (d.bypasses.length) {
    sections.push(h('section', {}, h('h3', { text: t('details.bypass') }),
      h('div', { class: 'bypasses' }, d.bypasses.map((b) => h('p', { class: 'callout' }, icon('warning'),
        h('span', {}, ...bypassText(b), ' ', t('bypass.confidence', { confidence: t('conf.' + b.confidence) })))))));
  }

  sections.push(h('section', {}, h('h3', { text: t('details.when') }), sparkline(d)));

  if (d.heartbeats.length) {
    sections.push(h('section', {}, h('h3', { text: t('details.beats') }),
      h('ul', { class: 'beats' }, d.heartbeats.map((b) => h('li', {},
        h('span', { class: 'pill ' + catClass(b.category), text: catLabel(b.category) }),
        h('span', { class: 'domain', text: b.domain }),
        h('span', { class: 'meta', text: t('beat.meta', { every: every(b.everySeconds), n: fmt(b.count), count: b.count, regularity: regularity(b.jitter) }) }))))));
  }

  sections.push(domainSection(d));

  sections.push(h('section', {}, h('h3', { text: t('details.fixes') }),
    d.fixes.length ? h('ol', { class: 'fixes' }, d.fixes.map((f) => h('li', {},
      h('h4', { text: f.title }),
      f.steps.length ? h('ol', {}, f.steps.map((st) => h('li', { text: st }))) : null,
      f.notes ? h('p', { class: 'notes', text: f.notes }) : null,
      f.evidence.length ? h('p', { class: 'evidence' }, f.evidence.map((u, i) =>
        h('a', { href: u, target: '_blank', rel: 'noopener noreferrer' },
          f.evidence.length > 1 ? t('fix.sourceN', { n: i + 1 }) : t('fix.source'), icon('external')))) : null)))
      : h('p', { class: 'empty-note', text: t('fix.none') })));

  if (d.unknown.length) sections.push(unclassified(d));

  const english = t('details.english');
  sections.push(h('p', { class: 'footnote' }, icon('lock'),
    h('span', { text: t('details.footnote') + (english ? ' ' + english : '') })));

  const dlg = document.getElementById('drawer');
  dlg.replaceChildren(
    h('header', { class: 'drawer-head' },
      h('span', { class: 'kind' }, icon(KINDS.has(d.kind) ? d.kind : 'unknown')),
      h('div', { class: 'dev-title' },
        h('h2', { id: 'drawer-title', tabindex: '-1', text: d.name }),
        h('p', { class: 'dev-sub', text: [subtitle(d), d.ips[0]].filter(Boolean).join(' · ') })),
      gradeBadge(d.grade),
      h('button', { type: 'button', class: 'icon-btn', 'aria-label': t('close'), onclick: () => dlg.close() }, icon('close'))),
    h('div', { class: 'drawer-body' }, sections));
  return dlg;
}

// ---------- Every domain a device looked up ----------
// The report carries the top 25; the full list, with each domain's first
// and last lookup and the rule that classified it, comes from
// api/devices/{id}/domains when the details open.

const DOMAIN_COLS = [
  { key: 'domain', label: 'col.domain', type: 'text' },
  { key: 'companyName', label: 'col.company', type: 'text', cls: 'company' },
  { key: 'category', label: 'col.category', type: 'cat' },
  { key: 'count', label: 'col.lookups', type: 'num', cls: 'num' },
  { key: 'blocked', label: 'col.blocked', type: 'num', cls: 'num' },
  { key: 'firstSeen', label: 'col.first', type: 'time' },
  { key: 'lastSeen', label: 'col.last', type: 'time' },
  { key: 'confidence', label: 'col.rule', type: 'conf' },
];
const CONF_RANK = { high: 3, medium: 2, low: 1, '': 0 };

function domainSection(d) {
  const sec = h('section', { class: 'domains' }, h('h3', { text: t('details.where') }),
    h('p', { class: 'empty-note', role: 'status', text: t('domains.loading') }));
  const days = state.days;
  api(`api/devices/${encodeURIComponent(d.id)}/domains?days=${days}`).then((res) => {
    if (days !== state.days || !sec.isConnected) return;
    sec.replaceChildren(h('h3', { text: t('details.where') }), domainTable(d, res));
  }).catch((err) => {
    sec.replaceChildren(h('h3', { text: t('details.where') }),
      h('p', { class: 'empty-note', text: t('domains.error', { error: err.message }) }));
  });
  return sec;
}

// DOMAIN_PAGE is how many rows the domain table draws at a time; the
// filter and the exports always cover every domain.
const DOMAIN_PAGE = 500;

function domainTable(d, res) {
  const all = res.domains;
  if (!all.length) return h('p', { class: 'empty-note', text: t('domains.none') });
  const view = { key: 'count', dir: -1, filter: '', limit: DOMAIN_PAGE };
  const tbody = h('tbody');
  const more = h('button', { type: 'button', class: 'btn small', hidden: true,
    onclick: () => { view.limit += DOMAIN_PAGE; draw(); } });
  let typing;
  const count = h('p', { class: 'table-count', role: 'status', 'aria-live': 'polite' });
  const heads = DOMAIN_COLS.map((c) => {
    const btn = h('button', { type: 'button', class: 'sort', onclick: () => sortBy(c.key) }, t(c.label),
      h('span', { class: 'sort-mark', 'aria-hidden': 'true' }));
    return h('th', { scope: 'col', class: c.cls || null, 'data-key': c.key }, btn);
  });
  const filter = h('input', { type: 'search', id: 'domain-filter', placeholder: t('domains.placeholder'),
    autocomplete: 'off', spellcheck: 'false', oninput: (e) => {
      // A phone can have thousands of domains a month: wait for a pause in
      // typing rather than redrawing on every key.
      clearTimeout(typing);
      typing = setTimeout(() => { view.filter = e.target.value.trim().toLowerCase(); view.limit = DOMAIN_PAGE; draw(); }, 150);
    } });

  function value(row, c) {
    if (c.type === 'cat') return catLabel(row.category);
    if (c.type === 'conf') return CONF_RANK[row.confidence] ?? 0;
    if (c.type === 'time') return Date.parse(row[c.key]) || 0; // offsets differ across DST
    return row[c.key] ?? '';
  }
  function sortBy(key) {
    view.dir = view.key === key ? -view.dir : (DOMAIN_COLS.find((c) => c.key === key).type === 'num' ? -1 : 1);
    view.key = key;
    draw();
  }
  function draw() {
    const col = DOMAIN_COLS.find((c) => c.key === view.key);
    const rows = all.filter((r) => !view.filter ||
      [r.domain, r.companyName, catLabel(r.category), r.purpose].some((x) => (x || '').toLowerCase().includes(view.filter)));
    rows.sort((a, b) => {
      const x = value(a, col), y = value(b, col);
      const c = typeof x === 'number' ? x - y : String(x).localeCompare(String(y), LANG);
      return c * view.dir || a.domain.localeCompare(b.domain);
    });
    for (const th of heads) {
      const on = th.dataset.key === view.key;
      if (on) th.setAttribute('aria-sort', view.dir > 0 ? 'ascending' : 'descending');
      else th.removeAttribute('aria-sort');
      th.querySelector('.sort-mark').textContent = on ? (view.dir > 0 ? ' ▲' : ' ▼') : '';
    }
    const shown = rows.slice(0, view.limit);
    tbody.replaceChildren(...shown.map(domainRow));
    const rest = rows.length - shown.length;
    more.hidden = rest <= 0;
    more.textContent = t('domains.more', { n: fmt(Math.min(rest, DOMAIN_PAGE)) });
    count.textContent = shown.length === all.length
      ? t('domains.count', { n: fmt(all.length), count: all.length })
      : (rows.length ? t('domains.shown', { shown: fmt(shown.length), total: fmt(all.length) }) : t('domains.noMatch'));
  }
  draw();

  return h('div', {},
    h('div', { class: 'table-tools' },
      h('label', { for: 'domain-filter', class: 'visually-hidden', text: t('domains.filter') }), filter, count),
    h('div', { class: 'table-wrap' }, h('table', { class: 'domain-table' }, h('thead', {}, h('tr', {}, heads)), tbody)),
    h('p', { class: 'table-more' }, more),
    h('p', { class: 'table-export' }, t('domains.export'), ' ',
      h('button', { type: 'button', class: 'btn small', onclick: () => exportDomains(d, res, 'csv') }, icon('download'), t('export.csv')),
      ' ',
      h('button', { type: 'button', class: 'btn small', onclick: () => exportDomains(d, res, 'json') }, icon('download'), t('export.json'))));
}

function domainRow(r) {
  return h('tr', {},
    h('td', {}, h('div', { class: 'domain', text: r.domain }), r.purpose ? h('div', { class: 'purpose', text: r.purpose }) : null),
    h('td', { class: 'company', text: r.companyName || '—' }),
    h('td', {}, h('span', { class: 'pill ' + catClass(r.category), text: catLabel(r.category) })),
    h('td', { class: 'num', text: fmt(r.count) }),
    h('td', { class: 'num' + (r.blocked ? '' : ' muted'), text: r.blocked ? fmt(r.blocked) : '—' }),
    h('td', { class: 'when', text: when(r.firstSeen) }),
    h('td', { class: 'when', text: when(r.lastSeen) }),
    h('td', { class: 'rule' }, r.rule
      ? [h('span', { text: t('rule.confidence', { confidence: t('conf.' + r.confidence) }) }),
        ...r.evidence.map((u, i) => [' ', h('a', { href: u, target: '_blank', rel: 'noopener noreferrer' },
          r.evidence.length > 1 ? t('rule.evidenceN', { n: i + 1 }) : t('rule.evidence'), icon('external'))])]
      : h('span', { class: 'muted', text: t('rule.none') })));
}

// ---------- Exports: made in the browser from data already loaded ----------

// csvCell quotes a value and defuses spreadsheet formulas: device names and
// domains are untrusted text.
function csvCell(v) {
  let s = v == null ? '' : String(v);
  if (/^[=+\-@\t\r]/.test(s)) s = "'" + s;
  return /[",\n\r]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s;
}

function csv(header, rows) {
  return [header, ...rows].map((r) => r.map(csvCell).join(',')).join('\r\n') + '\r\n';
}

function download(name, type, text) {
  const url = URL.createObjectURL(new Blob([text], { type }));
  const a = h('a', { href: url, download: name });
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
  toast(t('export.done', { file: name }));
}

const stampName = () => new Date().toISOString().slice(0, 10);
const CAT_IDS = ['acr', 'ads', 'tracking', 'telemetry', 'essential', 'content', 'unknown'];

// exportReport saves the report on screen: one row per device as CSV, or
// the whole API response as JSON. Column names stay English so scripts
// can rely on them.
function exportReport(kind) {
  const r = state.report;
  const base = `phonehome${r.demo ? '-demo' : ''}-report-${state.days}d-${stampName()}`;
  if (kind === 'json') {
    download(base + '.json', 'application/json', JSON.stringify(r, null, 2) + '\n');
    return;
  }
  const header = ['id', 'name', 'kind', 'vendor', 'grade', 'lookups', 'snooping_lookups', 'snooping_per_day', 'blocked',
    'quiet_hours_lookups', 'heartbeats', 'bypass_findings', ...CAT_IDS.map((c) => 'lookups_' + c)];
  const rows = r.devices.map((d) => {
    const byCat = Object.fromEntries(d.categories.map((c) => [c.id, c.count]));
    return [d.id, d.name, d.kind, d.vendor, d.grade, d.total, d.snooping, Math.round(d.perDay * 10) / 10, d.blocked,
      d.quiet.lookups, d.heartbeats.length, d.bypasses.length, ...CAT_IDS.map((c) => byCat[c] ?? 0)];
  });
  download(base + '.csv', 'text/csv', csv(header, rows));
}

function exportDomains(d, res, kind) {
  const slug = d.name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'device';
  const base = `phonehome${res.demo ? '-demo' : ''}-${slug}-domains-${state.days}d-${stampName()}`;
  if (kind === 'json') {
    download(base + '.json', 'application/json', JSON.stringify(res, null, 2) + '\n');
    return;
  }
  download(base + '.csv', 'text/csv', csv(
    ['domain', 'category', 'company', 'purpose', 'lookups', 'blocked', 'first_seen', 'last_seen', 'rule', 'rule_match', 'confidence', 'evidence'],
    res.domains.map((r) => [r.domain, r.category, r.companyName, r.purpose, r.count, r.blocked, r.firstSeen, r.lastSeen,
      // A rule "=host" matches only host; "host" matches its subdomains too.
      r.rule.replace(/^=/, ''), r.rule ? (r.rule.startsWith('=') ? 'exact' : 'subdomains') : '',
      r.confidence, r.evidence.join(' ')])));
}

// sinceDetails is the drawer's comparison with the previous period.
function sinceDetails(d) {
  const p = d.previous;
  const title = t('since.title', { prev: prevLabel(p) });
  if (!p.seen) {
    return h('section', {}, h('h3', { text: title }),
      h('p', { class: 'empty-note', text: t('since.notSeenDetail', { prev: prevLabel(p) }) }));
  }
  const cats = p.categories.filter((c) => c.snooping && Math.abs(c.deltaPerDay) >= 0.5);
  const perDay = (n) => (n > 0 ? '+' : n < 0 ? '−' : '') + fmt(Math.abs(n));
  return h('section', {}, h('h3', { text: title }),
    h('dl', { class: 'stats' },
      stat(t('stat.snoopDay'), `${fmt(p.perDay)} → ${fmt(p.nowPerDay)}`),
      stat(t('stat.change'), trend(p)),
      stat(t('stat.grade'), gradeShift(p.grade, d.grade))),
    cats.length ? h('ul', { class: 'deltas', 'aria-label': t('deltas.aria') }, cats.map((c) => h('li', {},
      h('span', { class: 'pill ' + catClass(c.id), text: catLabel(c.id) }),
      h('span', { class: 'num ' + (c.deltaPerDay < 0 ? 'better' : 'worse'), text: t('delta.perDay', { n: perDay(c.deltaPerDay) }) })))) : null,
    stoppedList(p.stopped, 10),
    p.started.length ? h('ul', { class: 'stopped started', 'aria-label': t('started.aria') }, p.started.map((b) => h('li', {},
      icon('pulse'), h('span', {}, ...tn('started.item', { domain: h('code', { text: b.domain }),
        heartbeat: t('heartbeat', { category: catInline(b.category) }), every: every(b.everySeconds) }))))) : null,
    p.partial ? h('p', { class: 'since-note', text: t('since.partialDetail', { data: nf.format(Math.round(p.dataDays * 10) / 10) }) }) : null);
}

// unclassified lists domains the knowledge base can't explain yet and offers a
// prefilled GitHub issue. The link is built server-side (internal/web/suggest.go)
// from domain names and the device's make and type only; phonehome never
// fetches it, the person clicks it.
function unclassified(d) {
  const top = d.unknown.slice(0, 8);
  const more = d.unknown.length - top.length;
  return h('section', { class: 'unclassified' },
    h('h3', { text: t('unc.title') }),
    h('p', { class: 'lead-note', text: t('unc.lead') }),
    h('ul', { class: 'unknown' }, top.map((u) => h('li', {},
      h('span', { class: 'domain', text: u.domain }),
      h('span', { class: 'meta', text: t('unc.meta', { n: fmt(u.count), count: u.count, ago: ago(u.lastSeen) }) })))),
    more > 0 ? h('p', { class: 'more' }, ...tn('unc.more', { n: fmt(more), count: more, cmd: h('code', { text: 'phonehome unknown' }) })) : null,
    d.suggestUrl ? h('div', { class: 'suggest' },
      h('a', { class: 'btn', href: d.suggestUrl, target: '_blank', rel: 'noopener noreferrer' }, t('unc.suggest'), icon('external')),
      h('p', { class: 'share-note', text: t('unc.share') }))
      : state.report.demo ? h('p', { class: 'share-note', text: t('unc.demo') })
        : null);
}

function stat(label, value, extra) {
  return h('div', {}, h('dt', { text: label }), h('dd', {}, value, extra ? h('small', { text: extra }) : null));
}

// ---------- Receipt modal ----------

function receipt(id) {
  const d = id ? state.report.devices.find((x) => x.id === id) : null;
  const name = d ? d.name : t('receipt.home');
  const dlg = document.getElementById('receipt');
  const slug = (d ? d.name : 'home').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'device';
  const canCopy = window.isSecureContext && navigator.clipboard && 'ClipboardItem' in window;
  dlg.replaceChildren(
    h('div', { class: 'modal-head' },
      h('div', {}, h('h2', { id: 'receipt-title', tabindex: '-1', text: t('receipt.title') }),
        h('p', { text: `${name} · ${t('when.' + state.days)}` })),
      h('button', { type: 'button', class: 'icon-btn', 'aria-label': t('close'), onclick: () => dlg.close() }, icon('close'))),
    h('div', { class: 'paper' }, h('img', { src: receiptURL(id, 'svg'), alt: t('receipt.alt', { name }) })),
    h('div', { class: 'modal-actions' },
      h('a', { class: 'btn primary', href: receiptURL(id, 'png'), download: `phonehome-${slug}-${state.days}d.png` },
        icon('download'), t('receipt.png')),
      canCopy ? h('button', { type: 'button', class: 'btn', onclick: () => copyReceipt(id) }, icon('copy'), t('receipt.copy')) : null),
    h('p', { class: 'nudge', text: (d ? t('receipt.nudgeDevice') : t('receipt.nudgeHome')) +
      (t('receipt.english') ? ' ' + t('receipt.english') : '') }));
  return dlg;
}

async function copyReceipt(id) {
  try {
    const blob = fetch(receiptURL(id, 'png')).then((r) => {
      if (!r.ok) throw new Error(r.statusText);
      return r.blob();
    });
    await navigator.clipboard.write([new ClipboardItem({ 'image/png': blob })]);
    toast(t('receipt.copied'));
  } catch {
    toast(t('receipt.copyFailed'));
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
// and no button looks pre-selected. Closing it returns focus to whatever
// opened it (the card's button, or the card itself after a re-render).
let opener = null;
function openDialog(dlg) {
  if (!dlg.open) opener = document.activeElement?.closest?.('[data-id]')?.dataset.id ?? document.activeElement;
  if (!dlg.open) dlg.showModal();
  dlg.scrollTop = 0;
  dlg.querySelector('h2').focus();
}

function restoreFocus() {
  const target = typeof opener === 'string'
    ? document.querySelector(`.card[data-id="${CSS.escape(opener)}"]`) : opener;
  opener = null;
  if (target?.isConnected) target.focus();
}

// trapFocus keeps Tab inside an open dialog: from the last control it goes
// back to the first, and the other way round.
function trapFocus(e) {
  if (e.key !== 'Tab') return;
  const dlg = e.currentTarget;
  const items = [...dlg.querySelectorAll('a[href], button:not([disabled]), input, select, textarea, [tabindex]:not([tabindex="-1"])')]
    .filter((el) => el.offsetParent !== null || el === document.activeElement);
  if (!items.length) return;
  const first = items[0], last = items[items.length - 1];
  if (e.shiftKey && (document.activeElement === first || !dlg.contains(document.activeElement) || document.activeElement === dlg.querySelector('h2'))) {
    e.preventDefault();
    last.focus();
  } else if (!e.shiftKey && document.activeElement === last) {
    e.preventDefault();
    first.focus();
  }
}

function clearHash() {
  if (location.hash && !document.querySelector('dialog[open]')) {
    history.replaceState(null, '', location.pathname + location.search);
  }
}

// ---------- Empty state ----------

// Setup guides, by source type. Plain links the person clicks; the
// dashboard itself never requests anything from GitHub.
const DOCS = 'https://github.com/bizpers11991-code/phonehome/blob/main/docs/';
const GUIDES = {
  'pihole-db': [['guide.piholeDocker', 'setup/pihole-docker.md'], ['guide.piholeHost', 'setup/pihole-bare-metal.md']],
  'pihole-api': [['guide.piholeAPI', 'setup/pihole-api.md']],
  'adguard-querylog': [['guide.adguard', 'setup/adguard-home.md']],
  'dnsmasq-log': [['guide.openwrt', 'setup/openwrt.md']],
  leases: [['guide.names', 'setup/README.md#configuration-in-one-minute']],
  conntrack: [['guide.conntrack', 'detectors.md#dns-bypass-routing-around-your-filter']],
};
const ALL_GUIDES = [
  ['guide.piholeDocker', 'setup/pihole-docker.md'], ['guide.piholeHost', 'setup/pihole-bare-metal.md'],
  ['guide.adguard', 'setup/adguard-home.md'], ['guide.openwrt', 'setup/openwrt.md'],
  ['guide.all', 'setup/README.md'],
];
const DNS_TYPES = new Set(['pihole-db', 'pihole-api', 'adguard-querylog', 'dnsmasq-log']);

function guideLinks(list) {
  return h('ul', { class: 'guides' }, list.map(([label, path]) => h('li', {},
    h('a', { href: DOCS + path, target: '_blank', rel: 'noopener noreferrer' }, t(label), icon('external')))));
}

// problemItem is one file auto-detection found but could not read.
function problemItem(p) {
  return h('li', { class: 'callout' + (p.optional ? ' optional' : '') }, icon(p.optional ? 'info' : 'warning'),
    h('div', {},
      h('p', {}, h('b', {}, ...tn('problem.found', { path: h('code', { text: p.path }), problem: p.problem })),
        p.optional ? t('problem.optional') : null),
      h('p', { text: capitalize(p.hint) + '.' }),
      GUIDES[p.type] ? guideLinks(GUIDES[p.type]) : null));
}

// codeList renders locations as "a, b, c" in code spans.
function codeList(locs) {
  return locs.flatMap((loc, i) => [i ? ', ' : null, h('code', { text: loc })]);
}

function capitalize(t) { return t ? t[0].toUpperCase() + t.slice(1) : ''; }

// sourceRow is one source in use, with its last check and any error.
function sourceRow(src) {
  const err = src.health === 'error' && src.lastError;
  return h('li', {},
    h('div', { class: 'src-line' },
      h('span', { class: 'dot ' + src.health, role: 'img', 'aria-label': healthLabel(src.health) }),
      h('b', { text: src.name }),
      h('span', { class: 'src-meta', text: t('src.meta', { n: fmt(src.records), count: src.records, ago: ago(src.lastRun) }) })),
    err ? h('p', { class: 'callout' }, icon('warning'), h('span', {},
      h('b', { text: t('src.failing') }), h('code', { text: src.lastError }),
      /permission denied/i.test(src.lastError) ? t('src.perm') : null)) : null);
}

function healthLabel(health) {
  return t('health.' + (health === 'ok' ? 'healthy' : health));
}

// setupStatus says what phonehome found: nothing usable yet (with every file
// it could not read and how to fix it), or sources that have not delivered
// a lookup yet. It returns null when status is unknown.
function setupStatus(st) {
  const setup = st?.setup;
  if (!setup || st.demo) return null;
  const required = setup.problems.filter((p) => !p.optional);
  const optional = setup.problems.filter((p) => p.optional);
  const hasDNS = setup.sources.some((src) => DNS_TYPES.has(src.type));
  const reading = setup.sources.map((src) => src.location);

  if (!hasDNS) {
    return h('section', { class: 'setup', 'aria-labelledby': 'setup-title' },
      h('h2', { id: 'setup-title', text: t(required.length ? 'setup.cantRead' : 'setup.notFound') }),
      required.length ? h('ul', { class: 'problems' }, required.map(problemItem)) : null,
      setup.autoDetect ? h('p', { class: 'setup-note', text: t('setup.looks', { ago: ago(setup.checkedAt) }) }) : null,
      reading.length ? h('p', { class: 'setup-note' }, ...tn('setup.also', { list: h('span', {}, codeList(reading)) })) : null,
      optional.length ? h('details', { class: 'optional-problems' },
        h('summary', { text: t('setup.optional', { n: fmt(optional.length), count: optional.length }) }),
        h('ul', { class: 'problems' }, optional.map(problemItem))) : null,
      h('div', { class: 'setup-guides' }, h('h3', { text: t('setup.guides') }), guideLinks(ALL_GUIDES)));
  }

  const used = new Set(setup.sources.map((src) => src.type));
  const errors = st.sources.filter((src) => src.health === 'error');
  return h('section', { class: 'setup', 'aria-labelledby': 'setup-title' },
    h('h2', { id: 'setup-title', text: t(errors.length ? 'setup.failing' : 'setup.waiting') }),
    h('p', { class: 'setup-note' }, ...tn('setup.reading', { list: h('span', {}, codeList(reading)) }),
      st.newest ? t('setup.newest', { ago: ago(st.newest) }) : t('setup.slow'),
      t('setup.updates')),
    st.sources.length ? h('ul', { class: 'src-list' }, st.sources.map(sourceRow))
      : h('p', { class: 'setup-note', text: t('setup.firstCheck') }),
    errors.length ? guideLinks([...used].flatMap((t) => GUIDES[t] ?? [])) : null);
}

function renderOnboarding() {
  document.getElementById('devices').replaceChildren();
  const status = setupStatus(state.status);
  const waiting = status && state.status.setup.sources.some((src) => DNS_TYPES.has(src.type));
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
    panel.replaceChildren(h('p', { text: t(snip.note) }), ...snip.code.map((c) => h('pre', {}, h('code', { text: c }))));
  }
  select(0);

  document.getElementById('hero').replaceChildren(h('div', { class: 'onboard' },
    h('div', {},
      h('h1', { text: t(waiting ? 'onb.almost' : 'onb.title') }),
      h('p', { class: 'lede', text: t('onb.lede') })),
    status,
    waiting ? null : h('ol', { class: 'steps' },
      h('li', {}, h('h2', { text: t('onb.step1') }),
        h('p', { text: t('onb.step1p') }),
        h('div', {}, h('div', { class: 'tabs', role: 'tablist', 'aria-label': t('onb.tabs') }, buttons), panel)),
      h('li', {}, h('h2', { text: t('onb.step2') }),
        h('p', {}, ...tn('onb.step2p', { ingest: h('code', { text: 'phonehome ingest --once' }), serve: h('code', { text: 'phonehome serve' }) }))),
      h('li', {}, h('h2', { text: t('onb.step3') }),
        h('p', { text: t('onb.step3p') }))),
    h('p', { class: 'notice' }, icon('info'),
      h('span', {}, ...tn('onb.demo', { cmd: h('code', { text: 'phonehome demo' }) })))));
}

// While the page is empty, poll the status and reload once something a
// first-run visitor would care about changes: a source found or fixed, an
// error, or the first lookups.
let emptyPoll;
const EMPTY_POLL_MS = 15000;

function statusKey(st) {
  if (!st) return '';
  return JSON.stringify([st.newest, st.setup?.sources, st.setup?.problems,
    st.sources.map((src) => [src.name, src.health, src.lastError, src.records])]);
}

function watchEmpty() {
  clearTimeout(emptyPoll);
  if (!state.report || state.report.devices.length || state.report.demo) return;
  const seen = statusKey(state.status);
  emptyPoll = setTimeout(async () => {
    const st = await api('api/status').catch(() => null);
    if (st && statusKey(st) !== seen) load();
    else watchEmpty();
  }, EMPTY_POLL_MS);
}
// ---------- Footer ----------

function renderHealth() {
  const st = state.status;
  const el = document.getElementById('health');
  if (!st) { el.replaceChildren(); return; }
  const items = st.sources.map((src) => h('li', { title: src.lastError || '' },
    h('span', { class: 'dot ' + src.health, role: 'img', 'aria-label': healthLabel(src.health) }),
    h('b', { text: src.name }),
    t('health.records', { n: fmt(src.records), count: src.records }) + ' · ' +
      (src.health === 'error' ? t('health.failing', { error: src.lastError }) : t('health.updated', { ago: ago(src.lastOk) }))));
  if (!items.length) {
    items.push(h('li', {}, h('span', { class: 'dot ' + (st.demo ? 'ok' : 'stale'), 'aria-hidden': 'true' }),
      t(st.demo ? 'health.demo' : 'health.none')));
  }
  if (st.oldest) items.push(h('li', { text: t('health.since', { date: df.format(new Date(st.oldest)) }) }));
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
      h('h1', { text: t('error.title') }),
      h('p', { text: err.message }),
      h('button', { type: 'button', class: 'btn', onclick: load, text: t('error.retry') })));
    return;
  }
  render();
  route();
  watchEmpty();
}

// render draws everything from the loaded report, in the current language.
function render() {
  renderBanner();
  document.getElementById('hero').classList.toggle('is-empty', !state.report.devices.length);
  if (state.report.devices.length) {
    renderHero();
    renderDevices();
  } else {
    renderOnboarding();
  }
  renderHealth();
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
  state.days = [fromURL, saved].find((d) => PERIODS.includes(d)) ?? 7;
  LANG = pickLang();
  formats();
  const sel = document.getElementById('lang');
  sel.replaceChildren(...Object.entries(LOCALES).map(([code, name]) => h('option', { value: code, lang: code, text: name })));
  sel.addEventListener('change', () => setLang(sel.value));
  applyStatic();

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
    dlg.addEventListener('close', () => {
      clearHash();
      // Moving from one dialog to another, the old one's close event comes
      // after the new one opened: keep the opener for when that one closes.
      if (!document.querySelector('dialog[open]')) restoreFocus();
    });
    dlg.addEventListener('keydown', trapFocus);
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
