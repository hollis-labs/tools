'use strict';
// Caster UI. Talks to the same /v1 routes as the CLI and MCP wrappers. Note
// text is untrusted: it is only ever put into the page with textContent and
// createElement, never innerHTML.

const $ = (id) => document.getElementById(id);
const TOKEN_KEY = 'caster.token';
let token = null;
try { token = sessionStorage.getItem(TOKEN_KEY); } catch (_) { /* storage blocked: pair again each load */ }
let items = [];
let current = null; // {name, rev, ref}

function el(tag, props, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (k === 'class') e.className = v;
    else if (k.startsWith('on')) e.addEventListener(k.slice(2), v);
    else e[k] = v;
  }
  for (const k of kids) e.append(k);
  return e;
}

async function api(path, opts = {}) {
  const headers = { Authorization: 'Bearer ' + token };
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json';
  const r = await fetch(path, { method: opts.method || 'GET', headers, body: opts.body === undefined ? undefined : JSON.stringify(opts.body), redirect: 'error' });
  const body = await r.json().catch(() => ({}));
  if (r.status === 401) { signOut(true); throw new Error('not signed in'); }
  if (!r.ok && !(body && body.state)) throw new Error((body.error && body.error.message) || ('HTTP ' + r.status));
  return body;
}

// ---- pairing -------------------------------------------------------------
function setToken(t) {
  token = t;
  try { t ? sessionStorage.setItem(TOKEN_KEY, t) : sessionStorage.removeItem(TOKEN_KEY); } catch (_) {}
}
function signOut(prompt) {
  setToken(null);
  $('who').textContent = ''; $('signout').hidden = true;
  $('list').replaceChildren(); $('doc').replaceChildren(el('p', { class: 'muted' }, 'Not paired.'));
  if (prompt) openPair();
}
function openPair(code) {
  if (code) $('code').value = code;
  $('pairerr').textContent = '';
  if (!$('pair').open) $('pair').showModal();
}
$('pairform').addEventListener('submit', async (ev) => {
  ev.preventDefault();
  const code = $('code').value.trim();
  $('pairerr').textContent = '';
  try {
    const r = await fetch('/v1/pair/exchange', { method: 'POST', headers: { 'Content-Type': 'application/json' }, redirect: 'error',
      body: JSON.stringify({ code, label: $('label').value.trim() || 'browser' }) });
    const b = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(r.status === 401 ? 'That code is invalid, expired or already used.' : ((b.error && b.error.message) || 'HTTP ' + r.status));
    setToken(b.token); $('code').value = '';
    $('pair').close();
    start();
  } catch (e) { $('pairerr').textContent = e.message; }
});
$('signout').addEventListener('click', () => signOut(false));

// ---- note list -----------------------------------------------------------
async function loadList() {
  const r = await api('/v1/list');
  items = r.items || [];
  renderList(items.map((i) => ({ name: nameOf(i.ref), title: i.title, rev: i.rev })));
}
const nameOf = (ref) => ref.replace(/^caster:\/\//, '').replace(/@\d+$/, '');

function renderList(rows) {
  const nav = $('list'); nav.replaceChildren();
  if (!rows.length) { nav.append(el('p', { class: 'muted', style: 'padding:1rem' }, 'Nothing here.')); return; }
  let ns = null;
  for (const row of rows) {
    const [n, ...rest] = row.name.split('/');
    if (n !== ns) { ns = n; nav.append(el('div', { class: 'ns' }, ns)); }
    const a = el('a', { class: current && current.name === row.name ? 'on' : '', title: row.name, onclick: () => openNote(row.name) }, rest.join('/') || n);
    if (row.title) a.append(el('small', {}, row.title));
    nav.append(a);
  }
}

let searchTimer;
$('q').addEventListener('input', () => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(async () => {
    const q = $('q').value.trim();
    if (!q) return loadList().catch(showErr);
    try {
      const r = await api('/v1/search?q=' + encodeURIComponent(q) + '&limit=30');
      const rows = [];
      for (const h of r.hits || []) for (const n of h.names && h.names.length ? h.names : []) rows.push({ name: nameOf(n), title: (h.snippet || '').slice(0, 60) });
      renderList(rows);
    } catch (e) { showErr(e); }
  }, 250);
});

// ---- viewing and editing -------------------------------------------------
function showErr(e) { $('doc').replaceChildren(el('p', { class: 'err' }, String(e.message || e))); }

async function openNote(name, rev) {
  try {
    const ref = 'caster://' + name + (rev ? '@' + rev : '');
    const g = await api('/v1/get?max_bytes=-1&ref=' + encodeURIComponent(ref));
    if (g.state && g.state !== 'resolved') return showErr(new Error(name + ': ' + g.state));
    current = { name, rev: rev || null, meta: g, item: items.find((i) => nameOf(i.ref) === name) };
    viewNote(g);
    renderList(items.map((i) => ({ name: nameOf(i.ref), title: i.title })));
  } catch (e) { showErr(e); }
}

function viewNote(g) {
  const doc = $('doc');
  const bar = el('div', { class: 'bar' }, el('h1', { class: 't' }, current.name + (current.rev ? ' @' + current.rev : '')));
  if (!g.binary) {
    bar.append(el('button', { onclick: () => editNote(g.content || '') }, current.rev ? 'Restore into editor' : 'Edit'));
    bar.append(el('button', { onclick: () => copyText(g.content || '') }, 'Copy'));
  }
  bar.append(el('button', { onclick: showHistory }, 'History'));
  const body = el('div', { class: 'md' });
  if (g.binary) body.append(el('p', { class: 'muted' }, 'Binary content (' + g.size + ' bytes).'));
  else renderMarkdown(body, g.content || '');
  if (g.truncated) body.append(el('p', { class: 'muted' }, 'Truncated.'));
  doc.replaceChildren(bar, body, el('div', { id: 'hist', class: 'hist' }));
}

async function copyText(t) { try { await navigator.clipboard.writeText(t); } catch (_) { /* clipboard unavailable on plain http origins */ } }

async function showHistory() {
  const box = $('hist'); if (!box) return;
  try {
    const r = await api('/v1/log?name=' + encodeURIComponent(current.name));
    box.replaceChildren(el('h3', {}, 'History'));
    for (const v of (r.revisions || []).slice().reverse())
      box.append(el('a', { href: '#', onclick: (ev) => { ev.preventDefault(); openNote(current.name, v.rev); } }, '@' + v.rev + '  ' + new Date(v.at).toLocaleString() + (v.title ? '  ' + v.title : '')));
  } catch (e) { box.replaceChildren(el('p', { class: 'err' }, e.message)); }
}

function editNote(content, isNew) {
  const nameIn = el('input', { placeholder: 'name or ns/name', value: isNew ? '' : current.name, readOnly: !isNew });
  const titleIn = el('input', { placeholder: 'Title', value: isNew ? '' : ((current.item && current.item.title) || current.meta.title || '') });
  const tagsIn = el('input', { placeholder: 'tags (space separated)', value: isNew ? '' : ((current.item && current.item.tags) || '') });
  const ta = el('textarea', { value: content, spellcheck: true });
  const msg = el('span', { class: 'muted' });
  const save = el('button', { onclick: async () => {
    const name = nameIn.value.trim();
    if (!name) { msg.textContent = 'Name required.'; return; }
    try {
      const r = await api('/v1/put', { method: 'POST', body: { name, title: titleIn.value.trim(), tags: tagsIn.value.trim(), mime: 'text/markdown', content: ta.value } });
      msg.textContent = 'Saved ' + r.ref;
      await loadList(); await openNote(nameOf(r.ref));
    } catch (e) { msg.textContent = e.message; msg.className = 'err'; }
  } }, 'Save');
  const cancel = el('button', { onclick: () => (current && !isNew ? openNote(current.name) : $('doc').replaceChildren()) }, 'Cancel');
  $('doc').replaceChildren(el('div', { class: 'fields' }, nameIn, titleIn, tagsIn), ta, el('div', { class: 'bar', style: 'margin-top:.5rem' }, save, cancel, msg));
  (isNew ? nameIn : ta).focus();
}
$('new').addEventListener('click', () => { current = null; editNote('', true); });

// ---- markdown (small, DOM-built, no HTML) --------------------------------
function safeHref(u) { return /^(https?:\/\/|caster:\/\/)/i.test(u) ? u : null; }

function inline(parent, text) {
  const re = /(`[^`]+`)|(\*\*[^*]+\*\*)|(\*[^*\s][^*]*\*)|(\[[^\]]+\]\([^)\s]+\))|(caster:\/\/[\w./@-]+)/g;
  let last = 0, m;
  while ((m = re.exec(text))) {
    if (m.index > last) parent.append(text.slice(last, m.index));
    const s = m[0];
    if (m[1]) parent.append(el('code', {}, s.slice(1, -1)));
    else if (m[2]) parent.append(el('strong', {}, s.slice(2, -2)));
    else if (m[3]) parent.append(el('em', {}, s.slice(1, -1)));
    else if (m[4]) { const [, label, url] = /^\[([^\]]+)\]\(([^)\s]+)\)$/.exec(s); parent.append(link(label, url)); }
    else parent.append(link(s, s));
    last = m.index + s.length;
  }
  if (last < text.length) parent.append(text.slice(last));
}
function link(label, url) {
  const href = safeHref(url);
  if (!href) return document.createTextNode(label);
  if (/^caster:\/\//i.test(href)) {
    const m = /^caster:\/\/([^@]+)(?:@(\d+))?$/i.exec(href);
    return el('a', { href: '#', title: href, onclick: (ev) => { ev.preventDefault(); if (m) openNote(m[1], m[2]); } }, label);
  }
  return el('a', { href, target: '_blank', rel: 'noopener noreferrer' }, label);
}

function renderMarkdown(root, src) {
  const lines = src.replace(/\r\n?/g, '\n').split('\n');
  let i = 0;
  while (i < lines.length) {
    const ln = lines[i];
    if (/^```/.test(ln)) {
      const buf = []; i++;
      while (i < lines.length && !/^```/.test(lines[i])) buf.push(lines[i++]);
      i++; root.append(el('pre', {}, el('code', {}, buf.join('\n')))); continue;
    }
    const h = /^(#{1,6})\s+(.*)$/.exec(ln);
    if (h) { const n = el('h' + Math.min(h[1].length, 3)); inline(n, h[2]); root.append(n); i++; continue; }
    if (/^\s*([-*_])\1\1+\s*$/.test(ln)) { root.append(el('hr')); i++; continue; }
    if (/^>\s?/.test(ln)) {
      const buf = []; while (i < lines.length && /^>\s?/.test(lines[i])) buf.push(lines[i++].replace(/^>\s?/, ''));
      const q = el('blockquote'); inline(q, buf.join(' ')); root.append(q); continue;
    }
    const li = /^\s*([-*+]|\d+\.)\s+(.*)$/.exec(ln);
    if (li) {
      const list = el(/\d/.test(li[1]) ? 'ol' : 'ul');
      while (i < lines.length) {
        const m = /^\s*([-*+]|\d+\.)\s+(.*)$/.exec(lines[i]);
        if (!m) break;
        const item = el('li'); inline(item, m[2]); list.append(item); i++;
      }
      root.append(list); continue;
    }
    if (!ln.trim()) { i++; continue; }
    const buf = [];
    while (i < lines.length && lines[i].trim() && !/^(```|#{1,6}\s|>|\s*([-*+]|\d+\.)\s)/.test(lines[i])) buf.push(lines[i++]);
    const p = el('p'); inline(p, buf.join(' ')); root.append(p);
  }
}

// ---- start ---------------------------------------------------------------
async function start() {
  try {
    const w = await api('/v1/whoami');
    $('who').textContent = (w.label || w.id) + ' · ' + (w.scopes || []).join(' ');
    $('signout').hidden = false;
    $('doc').replaceChildren(el('p', { class: 'muted' }, 'Pick a note, or create one.'));
    await loadList();
  } catch (e) { if (token) showErr(e); }
}

function takePairFragment() {
  const m = /(?:^|[#&])pair=(ccg_[A-Za-z0-9_-]+)/.exec(location.hash);
  if (m) history.replaceState(null, '', location.pathname + location.search);
  return m ? m[1] : '';
}
// A pair link opened in a tab that is already running changes only the fragment.
window.addEventListener('hashchange', () => { const c = takePairFragment(); if (c) openPair(c); });

(function init() {
  // `caster pair -link` prints …/#pair=ccg_… . The fragment never reaches the
  // server, and is removed from the address bar before anything else happens.
  const code = takePairFragment();
  if (token && !code) start();
  else if (token) { start(); openPair(code); }
  else openPair(code);
})();
