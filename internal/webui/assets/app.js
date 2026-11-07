'use strict';

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

const state = {
  view: 'overview',
  days: 14,
  q: '',
  counts: {},
  stateCounts: {},
  reviewSort: 'newest',
  offset: 0,
  total: 0,
  reviews: [],
  series: [],
  runtime: null,
  loading: false,
  reviewsLoaded: false,
  reposLoaded: false,
  repoStats: [],
  available: [],
  repoQuery: '',
  repoFilter: 'all',
  watch: { all: true, repos: [] },
  lastFocus: null,
};

const TITLES = {
  overview: 'Overview',
  reviews: 'Reviews',
  repos: 'Repositories',
  learnings: 'Learnings',
  settings: 'Runtime',
};

/* ── utilities ─────────────────────────────────────────────────────────── */

const esc = (value = '') =>
  String(value).replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));

const num = (n) => new Intl.NumberFormat().format(Number(n) || 0);

const reducedMotion = () => window.matchMedia('(prefers-reduced-motion: reduce)').matches;

function relativeTime(iso) {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '—';
  const seconds = (Date.now() - date.getTime()) / 1000;
  if (seconds < 60) return 'just now';
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  if (seconds < 604800) return `${Math.floor(seconds / 86400)}d ago`;
  return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

function shortDate(iso) {
  const date = new Date(`${iso}T00:00:00Z`);
  if (Number.isNaN(date.getTime())) return iso;
  return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' });
}

function duration(ms) {
  if (!ms) return '';
  return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(ms < 10000 ? 1 : 0)}s`;
}

function safeUrl(raw) {
  try {
    const url = new URL(raw);
    return url.protocol === 'http:' || url.protocol === 'https:' ? url.href : '#';
  } catch {
    return '#';
  }
}

const verdictLabel = (v) => ({
  approve: 'Approved',
  changes: 'Changes',
  suggestions: 'Suggestions',
  skipped: 'Skipped',
  legacy: 'Imported',
}[v] || (v ? v[0].toUpperCase() + v.slice(1) : 'Unknown'));

const verdictClass = (v) => ({
  approve: 'v-approve',
  changes: 'v-changes',
  suggestions: 'v-suggestions',
}[v] || 'v-neutral');

const emptyState = (title, copy) => `
  <div class="empty">
    <span class="empty-mark">${esc(title)}</span>
    <p>${esc(copy)}</p>
  </div>`;

const skeletonCards = (n) => Array.from({ length: n }, () => '<div class="skeleton-card"></div>').join('');

async function api(path, options = {}) {
  const init = {
    method: options.method || 'GET',
    credentials: 'same-origin',
    headers: { Accept: 'application/json' },
  };
  if (options.body !== undefined) {
    init.headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(options.body);
  }
  const res = await fetch(path, init);
  if (res.status === 401) {
    openGate();
    throw new Error('Unauthorized');
  }
  if (!res.ok) {
    let message = 'Request failed';
    try {
      const body = await res.json();
      message = body.error || message;
    } catch { /* keep default */ }
    throw new Error(message);
  }
  return res.json();
}

let toastTimer;
function toast(message) {
  const el = $('#toast');
  el.textContent = message;
  el.classList.add('is-visible');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove('is-visible'), 2600);
}

/* ── numbers ───────────────────────────────────────────────────────────── */

function animateNumber(el, target, suffix = '') {
  const to = Number(target) || 0;
  if (reducedMotion()) {
    el.textContent = num(to) + suffix;
    return;
  }
  const from = Number(el.dataset.value || 0);
  const start = performance.now();
  const span = 850;
  el.dataset.value = String(to);

  const tick = (now) => {
    const progress = Math.min(1, (now - start) / span);
    const eased = 1 - Math.pow(1 - progress, 4);
    el.textContent = num(Math.round(from + (to - from) * eased)) + suffix;
    if (progress < 1) requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
}

/* ── overview ──────────────────────────────────────────────────────────── */

async function loadOverview(silent = false) {
  const data = await api(`/api/overview?days=${state.days}`);
  state.runtime = data.runtime;
  state.series = Array.isArray(data.series) ? data.series : [];
  renderRuntime();
  renderOverview(data, silent);
  $('#app').setAttribute('aria-busy', 'false');
}

function renderRuntime() {
  const rt = state.runtime;
  if (!rt) return;
  $('#rail-mode').textContent = rt.mode === 'webhook' ? 'Webhook mode' : 'Polling mode';
  $('#rail-model').textContent = (rt.models && rt.models[0]) || 'no model';
  $('#rail-meta').textContent = `${rt.workers} workers · ${rt.dashboard_auth ? 'secured' : 'open access'}`;
  $('#rail-dot').classList.add('is-live');
  $('#runtime-mode').textContent = rt.mode === 'webhook' ? 'Webhook' : 'Polling';
  $('#security-notice').hidden = Boolean(rt.dashboard_auth);
}

function renderOverview(data, silent) {
  const totals = data.totals || {};
  const reviews = totals.reviews || 0;
  const rate = reviews ? Math.round((totals.approve || 0) / reviews * 100) : 0;

  if (silent) {
    $('#stat-reviews').textContent = num(reviews);
    $('#stat-approve').textContent = `${rate}%`;
    $('#stat-findings').textContent = num(totals.findings);
    $('#stat-merged').textContent = num(totals.merged);
  } else {
    animateNumber($('#stat-reviews'), reviews);
    animateNumber($('#stat-approve'), rate, '%');
    animateNumber($('#stat-findings'), totals.findings);
    animateNumber($('#stat-merged'), totals.merged);
  }
  setRing(rate);

  $('#pulse-window').textContent = `Last ${state.days} days`;
  $('#stat-reviews-sub').textContent = reviews
    ? `${num(totals.approve)} approved · ${num(totals.changes)} changes requested`
    : 'No reviews in this window yet';
  $('#stat-approve-sub').textContent = `${num(totals.approve)} of ${num(reviews)} reviews`;
  $('#stat-findings-sub').textContent = totals.files
    ? `across ${num(totals.files)} changed files`
    : 'validated and anchored';
  $('#stat-merged-sub').textContent = `${num(totals.repos)} repositories tracked`;

  renderSpark(state.series);
  renderFindingBars(state.series);
  renderChart(state.series);
  renderRecent(data.recent || []);
}

function setRing(rate) {
  const ring = $('#approve-ring');
  if (!ring) return;
  const clamped = Math.max(0, Math.min(100, rate));
  ring.style.strokeDashoffset = String(100 - clamped);
}

function renderSpark(series) {
  const host = $('#spark');
  if (!series.length) {
    host.innerHTML = '';
    return;
  }
  const window_ = series.slice(-24);
  const max = Math.max(1, ...window_.map((d) => d.reviews));
  host.innerHTML = `<div class="spark-bars">${window_.map((d, i) => `
    <span class="${d.reviews ? 'on' : ''}" style="--h:${Math.max(8, Math.round((d.reviews / max) * 100))}%;--d:${i * 26}ms"></span>`).join('')}</div>`;
}

function renderFindingBars(series) {
  const host = $('#finding-bars');
  if (!host) return;
  if (!series.length) {
    host.innerHTML = '';
    return;
  }
  const window_ = series.slice(-28);
  const max = Math.max(1, ...window_.map((d) => d.findings));
  host.innerHTML = window_.map((d, i) => {
    const height = Math.max(6, Math.round((d.findings / max) * 100));
    return `<span style="--h:${height}%;--o:${d.findings ? 1 : .22};--d:${i * 22}ms"></span>`;
  }).join('');
}

function smoothPath(points) {
  if (!points.length) return '';
  if (points.length === 1) return `M ${points[0][0]} ${points[0][1]}`;
  let d = `M ${points[0][0].toFixed(1)} ${points[0][1].toFixed(1)}`;
  for (let i = 0; i < points.length - 1; i += 1) {
    const p0 = points[i - 1] || points[i];
    const p1 = points[i];
    const p2 = points[i + 1];
    const p3 = points[i + 2] || p2;
    const c1x = p1[0] + (p2[0] - p0[0]) / 6;
    const c1y = p1[1] + (p2[1] - p0[1]) / 6;
    const c2x = p2[0] - (p3[0] - p1[0]) / 6;
    const c2y = p2[1] - (p3[1] - p1[1]) / 6;
    d += ` C ${c1x.toFixed(1)} ${c1y.toFixed(1)}, ${c2x.toFixed(1)} ${c2y.toFixed(1)}, ${p2[0].toFixed(1)} ${p2[1].toFixed(1)}`;
  }
  return d;
}

function renderChart(series) {
  const host = $('#chart');
  if (!series.length) {
    host.innerHTML = emptyState('No activity', 'Reviews will appear here as soon as the bot runs.');
    return;
  }

  const W = 760;
  const H = 220;
  const L = 34;
  const R = 12;
  const T = 16;
  const B = 30;
  const plotW = W - L - R;
  const plotH = H - T - B;
  const maxReviews = Math.max(1, ...series.map((d) => d.reviews));
  const maxFindings = Math.max(1, ...series.map((d) => d.findings));
  const step = plotW / Math.max(1, series.length - 1);

  const points = series.map((d, i) => [L + i * step, T + plotH * (1 - d.reviews / maxReviews)]);
  const line = smoothPath(points);
  const area = `${line} L ${points[points.length - 1][0].toFixed(1)} ${T + plotH} L ${points[0][0].toFixed(1)} ${T + plotH} Z`;

  const grid = [0, 0.5, 1].map((ratio) => {
    const y = T + plotH * ratio;
    return `<line class="chart-grid" x1="${L}" y1="${y}" x2="${W - R}" y2="${y}"/>
      <text class="chart-label" x="0" y="${y + 3}">${num(Math.round(maxReviews * (1 - ratio)))}</text>`;
  }).join('');

  const bars = series.map((d, i) => {
    const height = Math.max(0, (d.findings / maxFindings) * 24);
    if (height < 1) return '';
    const x = L + i * step - Math.max(1.5, step * 0.16);
    return `<rect x="${x.toFixed(1)}" y="${(T + plotH - height).toFixed(1)}"
      width="${Math.max(2, step * 0.32).toFixed(1)}" height="${height.toFixed(1)}"
      rx="1.4" fill="rgba(255,176,103,.32)"/>`;
  }).join('');

  const labelIndexes = series.length > 2
    ? [0, Math.floor((series.length - 1) / 2), series.length - 1]
    : series.map((_, i) => i);
  const xLabels = labelIndexes.map((i) => {
    const x = L + i * step;
    const anchor = i === 0 ? 'start' : i === series.length - 1 ? 'end' : 'middle';
    return `<text class="chart-label" x="${x}" y="${H - 8}" text-anchor="${anchor}">${shortDate(series[i].date)}</text>`;
  }).join('');

  const last = points[points.length - 1];
  const totals = series.reduce((acc, d) => ({ reviews: acc.reviews + d.reviews, findings: acc.findings + d.findings }), { reviews: 0, findings: 0 });
  const empty = totals.reviews === 0 ? '<div class="chart-empty">No reviews in this window</div>' : '';

  host.innerHTML = `
    <svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="none" role="img" aria-label="Reviews per day">
      <defs>
        <linearGradient id="areaFill" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="#d9ff57" stop-opacity=".16"/>
          <stop offset="100%" stop-color="#d9ff57" stop-opacity="0"/>
        </linearGradient>
      </defs>
      ${grid}
      ${bars}
      <path class="chart-area" d="${area}"/>
      <path class="chart-line" d="${line}" pathLength="1"/>
      <line class="chart-cursor" x1="${L}" y1="${T}" x2="${L}" y2="${T + plotH}"/>
      <circle class="chart-halo" cx="${last[0].toFixed(1)}" cy="${last[1].toFixed(1)}" r="4"/>
      <circle class="chart-dot" cx="${last[0].toFixed(1)}" cy="${last[1].toFixed(1)}" r="3.4"/>
      ${xLabels}
    </svg>
    <div class="chart-tip"></div>
    ${empty}`;

  const svg = host.querySelector('svg');
  const cursor = host.querySelector('.chart-cursor');
  const tip = host.querySelector('.chart-tip');

  host.onmousemove = (event) => {
    const rect = host.getBoundingClientRect();
    if (!rect.width) return;
    const x = ((event.clientX - rect.left) / rect.width) * W;
    let index = Math.round((x - L) / step);
    index = Math.max(0, Math.min(series.length - 1, index));
    const day = series[index];
    const px = (points[index][0] / W) * rect.width;
    const clamped = Math.min(Math.max(px, 64), rect.width - 64);

    cursor.setAttribute('x1', points[index][0]);
    cursor.setAttribute('x2', points[index][0]);
    tip.style.left = `${clamped}px`;
    tip.innerHTML = `<strong>${num(day.reviews)}</strong> reviews · ${num(day.findings)} findings<br>${shortDate(day.date)}`;
    host.classList.add('is-hovering');
  };
  host.onmouseleave = () => host.classList.remove('is-hovering');
  if (svg) svg.setAttribute('aria-hidden', 'true');
}

function renderRecent(recent) {
  const host = $('#recent');
  if (!recent.length) {
    host.innerHTML = emptyState('Nothing yet', 'Your first review will show up here.');
    return;
  }
  host.innerHTML = recent.map((r) => `
    <button class="feed-item" data-id="${r.id}">
      <span class="feed-dot ${verdictClass(r.verdict)}"></span>
      <span class="feed-main">
        <span class="feed-repo">${esc(r.repo)} · #${r.pr}</span>
        <span class="feed-title">${esc(r.title || 'Untitled pull request')}</span>
      </span>
      <span class="feed-time">${relativeTime(r.created_at)}</span>
    </button>`).join('');

  $$('.feed-item', host).forEach((el) => {
    el.addEventListener('click', () => openReview(el.dataset.id));
  });
}

/* ── reviews ───────────────────────────────────────────────────────────── */

let reviewRequest = 0;

const SEVERITY_RANK = { critical: 4, high: 3, medium: 2, low: 1 };

const isBlocking = (review) => review.verdict === 'changes';
const isDone = (review) => review.state === 'merged' || review.state === 'closed';

function priorityOf(review) {
  const severity = SEVERITY_RANK[String(review.severity || '').toLowerCase()] || 0;
  if (isBlocking(review) && severity >= 3) return 'p0';
  if (isBlocking(review) || review.verdict === 'suggestions') return 'p1';
  return 'p2';
}

function queueReason(review) {
  if (review.state === 'merged') return 'Merged — this review is finished.';
  if (review.state === 'closed') return 'Closed without merging — no action needed.';
  const severity = String(review.severity || '').toLowerCase();
  const label = severity ? `${severity[0].toUpperCase()}${severity.slice(1)}-severity` : '';
  if (isBlocking(review)) {
    return label
      ? `${label} finding blocks this PR — fix it before the next review pass.`
      : 'Changes were requested — resolve the open findings before re-review.';
  }
  if (review.verdict === 'suggestions') return 'Advisory notes only — safe to merge once someone has read them.';
  if (review.verdict === 'approve') return 'Clean review — nothing blocking, ready to merge.';
  return 'Skipped or imported review — no live verdict was recorded.';
}

function queueCard(review) {
  const done = isDone(review);
  const priority = done ? 'p2' : priorityOf(review);
  const badge = done ? (review.state === 'merged' ? 'MERGED' : 'CLOSED') : priority.toUpperCase();
  const tags = [
    `<span class="queue-tag ${verdictClass(review.verdict)}">${esc(verdictLabel(review.verdict))}</span>`,
    `<span class="queue-tag">${num(review.findings)} finding${review.findings === 1 ? '' : 's'}</span>`,
  ];
  if (review.files) tags.push(`<span class="queue-tag">${num(review.files)} file${review.files === 1 ? '' : 's'}</span>`);
  else if (review.duration_ms) tags.push(`<span class="queue-tag">${duration(review.duration_ms)} review</span>`);

  return `<button type="button" class="queue-card" data-id="${review.id}">
    <span class="queue-top">
      <span class="prio ${priority}"><i aria-hidden="true"></i>${badge}</span>
      <span class="queue-time">${relativeTime(review.created_at)}</span>
    </span>
    <span class="queue-name">${esc(review.title || `Pull request #${review.pr}`)}</span>
    <span class="queue-why">${esc(queueReason(review))}</span>
    <span class="queue-tags">${tags.join('')}</span>
    <span class="queue-foot">
      <svg><use href="#i-repo"/></svg>
      <span class="queue-repo">${esc(review.repo)}</span>
      <span class="queue-pr">#${review.pr}</span>
      <span class="queue-chev"><svg><use href="#i-chevron"/></svg></span>
    </span>
  </button>`;
}

function renderQueueColumn(host, reviews, emptyTitle, emptyCopy) {
  if (!reviews.length) {
    host.innerHTML = `<div class="queue-empty"><strong>${esc(emptyTitle)}</strong><span>${esc(emptyCopy)}</span></div>`;
    return;
  }
  if (state.reviewSort !== 'repo') {
    host.innerHTML = reviews.map(queueCard).join('');
    return;
  }
  let lastRepo = '';
  const parts = [];
  for (const review of reviews) {
    const key = String(review.repo || '').toLowerCase();
    if (key !== lastRepo) {
      lastRepo = key;
      parts.push(`<div class="queue-group"><svg><use href="#i-repo"/></svg><span>${esc(review.repo)}</span></div>`);
    }
    parts.push(queueCard(review));
  }
  host.innerHTML = parts.join('');
}

function renderReviews() {
  const counts = state.counts || {};
  const done = state.reviews.filter(isDone);
  const active = state.reviews.filter((review) => !isDone(review));
  const stateCounts = state.stateCounts || {};
  const doneTotal = (stateCounts.merged || 0) + (stateCounts.closed || 0);
  const activeTotal = Math.max(0, state.total - doneTotal);
  const blockingTotal = typeof counts.changes === 'number'
    ? counts.changes
    : active.filter(isBlocking).length;
  const restTotal = Math.max(0, activeTotal - blockingTotal);

  const blocking = active.filter(isBlocking);
  if (state.reviewSort !== 'repo') {
    blocking.sort((a, b) => {
      const rank = { p0: 0, p1: 1, p2: 2 };
      return rank[priorityOf(a)] - rank[priorityOf(b)];
    });
  }
  const rest = active.filter((review) => !isBlocking(review));

  $('#review-count').textContent = !state.total
    ? 'No reviews'
    : state.q
      ? `${num(state.total)} matching review${state.total === 1 ? '' : 's'}`
      : `${num(state.total)} review${state.total === 1 ? '' : 's'} · ${num(blockingTotal)} need${blockingTotal === 1 ? 's' : ''} attention`;

  const label = (selector, loaded, total) => {
    const el = $(selector);
    if (!el) return;
    el.textContent = loaded < total ? `${num(loaded)}/${num(total)}` : num(total);
    el.title = `${num(total)} in this column`;
  };
  label('#count-now', blocking.length, blockingTotal);
  label('#count-next', rest.length, restTotal);
  label('#count-done', done.length, doneTotal);

  renderQueueColumn($('#review-now'), blocking,
    'Nothing blocking',
    state.q ? 'No blocking reviews match your search.' : 'Open a pull request and blocking reviews land here first.');
  renderQueueColumn($('#review-next'), rest,
    'Nothing waiting',
    state.q ? 'No reviews in this column match your search.' : 'Approved and advisory reviews queue up here.');
  renderQueueColumn($('#review-done'), done,
    'Nothing finished yet',
    state.q ? 'No merged or closed reviews match your search.' : 'Merged and closed pull requests land here.');

  $('#load-more').hidden = state.reviews.length >= state.total;
}

async function loadReviews(reset) {
  if (state.loading && !reset) return;
  const request = ++reviewRequest;
  state.loading = true;

  if (reset) {
    state.offset = 0;
    $('#review-now').innerHTML = skeletonCards(3);
    $('#review-next').innerHTML = skeletonCards(3);
    $('#review-done').innerHTML = skeletonCards(3);
    $('#load-more').hidden = true;
    $('#review-count').textContent = 'Loading…';
  }

  const params = new URLSearchParams({
    limit: '100',
    offset: String(reset ? 0 : state.offset),
    sort: state.reviewSort,
  });
  if (state.q) params.set('q', state.q);

  try {
    const data = await api(`/api/reviews?${params.toString()}`);
    if (request !== reviewRequest) return;
    const incoming = Array.isArray(data.reviews) ? data.reviews : [];
    state.reviews = reset ? incoming : state.reviews.concat(incoming);
    state.total = Number(data.total) || 0;
    state.counts = data.counts && typeof data.counts === 'object' ? data.counts : {};
    state.stateCounts = data.state_counts && typeof data.state_counts === 'object' ? data.state_counts : {};
    state.offset = state.reviews.length;
    renderReviews();
  } catch (error) {
    if (request === reviewRequest && error.message !== 'Unauthorized') {
      const copy = `<div class="queue-empty"><strong>Could not load reviews</strong><span>${esc(error.message)}</span></div>`;
      $('#review-now').innerHTML = copy;
      $('#review-next').innerHTML = '';
      $('#review-done').innerHTML = '';
      toast(error.message);
    }
  } finally {
    if (request === reviewRequest) state.loading = false;
  }
}

function movePill(selector = '#repo-filter') {
  const seg = $(selector);
  if (!seg) return;
  const active = seg.querySelector('.seg-btn.is-active');
  const pill = seg.querySelector('.seg-pill');
  if (!active || !pill) return;
  pill.style.width = `${active.offsetWidth}px`;
  pill.style.transform = `translateX(${active.offsetLeft}px)`;
}

function moveActivePill() {
  if (state.view === 'repos') movePill('#repo-filter');
  if (state.view === 'reviews') movePill('#review-sort');
}

/* ── repositories ──────────────────────────────────────────────────────── */

function isWatched(repo) {
  if (state.watch.all) return true;
  return state.watch.repos.some((entry) => entry.toLowerCase() === repo.toLowerCase());
}

// repoBars draws review density from the real counts. No filler segments, so a
// repository with no reviews gets a flat, visibly empty track.
function repoBars(findings, reviews) {
  const slots = 14;
  if (!reviews) {
    return { empty: true, label: 'No reviews yet', html: Array.from({ length: slots }, () => '<span></span>').join('') };
  }
  const density = findings / reviews;
  const filled = Math.min(slots, Math.round(density * 2));
  const hot = density >= 4;
  const html = Array.from({ length: slots }, (_, i) => {
    if (i >= filled) return '<span></span>';
    return hot ? '<span class="warm"></span>' : '<span class="on"></span>';
  }).join('');
  return { empty: filled === 0, label: `${density.toFixed(1)} findings / review`, html };
}

// repoList merges review stats with the repositories the bot can actually
// reach, so an installed repo is selectable before its first review.
function repoList() {
  const stats = new Map(state.repoStats.map((repo) => [repo.repo.toLowerCase(), repo]));
  const names = state.available.length ? state.available : state.repoStats.map((repo) => repo.repo);
  const seen = new Set();
  const out = [];
  for (const name of names) {
    const key = String(name).toLowerCase();
    if (!key || seen.has(key)) continue;
    seen.add(key);
    const stat = stats.get(key);
    out.push(stat ? { ...stat, reviewed: true } : { repo: name, reviews: 0, findings: 0, reviewed: false });
  }
  return out;
}

function paintWatchState() {
  const list = repoList();
  const known = list.length;
  const all = state.watch.all;
  const watched = list.filter((repo) => isWatched(repo.repo)).length;

  const allBtn = $('#watch-all');
  if (allBtn) {
    allBtn.classList.toggle('is-on', all);
    allBtn.setAttribute('aria-checked', String(all));
  }

  const count = $('#watch-count');
  if (count) {
    count.textContent = !known
      ? 'No repositories yet'
      : all
        ? `Watching all ${known} repositor${known === 1 ? 'y' : 'ies'}`
        : `${watched} of ${known} watched`;
  }

  $$('.repo-card').forEach((card) => {
    const repo = card.dataset.repo;
    const on = isWatched(repo);
    card.classList.toggle('is-paused', !on);
    const label = card.querySelector('.repo-state');
    if (label) {
      label.textContent = on ? 'Watching' : 'Paused';
      label.classList.toggle('is-on', on);
    }
    const toggle = card.querySelector('.repo-toggle');
    if (!toggle) return;
    toggle.classList.toggle('is-on', on);
    toggle.setAttribute('aria-checked', String(on));
    toggle.disabled = all;
    toggle.title = all ? 'Every repository is watched' : on ? 'Stop watching this repository' : 'Watch this repository';
  });
}

function renderRepos() {
  const host = $('#repo-grid');
  const all = repoList();
  const query = state.repoQuery.trim().toLowerCase();
  const list = all.filter((repo) => {
    if (query && !repo.repo.toLowerCase().includes(query)) return false;
    if (state.repoFilter === 'watched') return isWatched(repo.repo);
    if (state.repoFilter === 'paused') return !isWatched(repo.repo);
    if (state.repoFilter === 'new') return repo.reviewed === false;
    return true;
  });

  const count = $('#repo-count');
  if (count) {
    count.textContent = !all.length
      ? 'No repositories'
      : list.length === all.length
        ? `${num(all.length)} repositor${all.length === 1 ? 'y' : 'ies'}`
        : `${num(list.length)} of ${num(all.length)} repositories`;
  }

  if (!all.length) {
    host.innerHTML = emptyState('No repositories yet', 'Install the GitHub App on repositories and they appear here, reviewed or not.');
    paintWatchState();
    return;
  }

  if (!list.length) {
    host.innerHTML = emptyState('Nothing matches', 'Clear the search or pick another filter.');
    paintWatchState();
    requestAnimationFrame(() => movePill('#repo-filter'));
    return;
  }

  host.innerHTML = list.map((repo) => {
    const watched = isWatched(repo.repo);
    const reviewed = repo.reviewed !== false;
    const meter = repoBars(repo.findings, repo.reviews);
    const chip = reviewed
      ? `<span class="verdict ${verdictClass(repo.last_verdict)}">${esc(verdictLabel(repo.last_verdict))}</span>`
      : '<span class="chip">Not reviewed</span>';
    return `<article class="repo-card ${watched ? 'is-watched' : 'is-paused'}" data-repo="${esc(repo.repo)}">
      <div class="repo-top">
        <span class="repo-name" title="${esc(repo.repo)}">${esc(repo.repo)}</span>
      </div>
      <div class="repo-sub">
        ${chip}
        <span class="repo-time">${reviewed ? `Last review ${relativeTime(repo.last_review_at)}` : 'No pull requests yet'}</span>
      </div>
      <div class="repo-nums">
        <div><strong>${num(repo.reviews)}</strong><span>Reviews</span></div>
        <div><strong>${num(repo.findings)}</strong><span>Findings</span></div>
      </div>
      <div class="repo-meter">
        <div class="bars ${meter.empty ? 'is-empty' : ''}" aria-hidden="true">${meter.html}</div>
        <span class="meter-label">${meter.label}</span>
      </div>
      <div class="repo-foot">
        <span class="repo-state ${watched ? 'is-on' : ''}">${watched ? 'Watching' : 'Paused'}</span>
        <button class="switch repo-toggle ${watched ? 'is-on' : ''}" role="switch" data-repo="${esc(repo.repo)}"
          aria-checked="${watched}" aria-label="Watch ${esc(repo.repo)}" ${state.watch.all ? 'disabled' : ''}>
          <span class="knob"></span>
        </button>
      </div>
    </article>`;
  }).join('');

  paintWatchState();
  requestAnimationFrame(() => movePill('#repo-filter'));
}

async function loadRepos() {
  const host = $('#repo-grid');
  host.innerHTML = '<div class="skeleton-block"></div><div class="skeleton-block"></div><div class="skeleton-block"></div>';
  try {
    const data = await api('/api/repos');
    state.repoStats = Array.isArray(data.repos) ? data.repos : [];
    state.available = Array.isArray(data.available) ? data.available : [];
    const watch = data.watch || {};
    state.watch = {
      all: watch.all !== false,
      repos: Array.isArray(watch.repos) ? watch.repos : [],
    };
    renderRepos();
  } catch (error) {
    if (error.message !== 'Unauthorized') {
      host.innerHTML = emptyState('Could not load repositories', error.message);
      toast(error.message);
    }
  }
}

async function saveWatch(patch) {
  const body = {
    all: patch.all === undefined ? state.watch.all : patch.all,
    repos: patch.repos === undefined ? state.watch.repos : patch.repos,
  };
  const allBtn = $('#watch-all');
  if (allBtn) allBtn.disabled = true;
  try {
    const data = await api('/api/watch', { method: 'PUT', body });
    state.watch = { all: !!data.all, repos: Array.isArray(data.repos) ? data.repos : [] };
    if (state.repoFilter === 'watched' || state.repoFilter === 'paused') renderRepos();
    else paintWatchState();
    toast(state.watch.all
      ? 'Watching every repository'
      : state.watch.repos.length
        ? `Watching ${state.watch.repos.length} repositor${state.watch.repos.length === 1 ? 'y' : 'ies'}`
        : 'All repositories paused');
  } catch (error) {
    if (error.message !== 'Unauthorized') toast(error.message);
  } finally {
    if (allBtn) allBtn.disabled = false;
  }
}

/* ── settings ──────────────────────────────────────────────────────────── */

async function loadRuntime() {
  if (!state.runtime) {
    try {
      state.runtime = await api('/api/runtime');
      renderRuntime();
    } catch (error) {
      if (error.message !== 'Unauthorized') toast(error.message);
      return;
    }
  }
  renderSettings();
}

function renderSettings() {
  const rt = state.runtime;
  if (!rt) return;

  const rows = [
    ['AI', rt.provider || 'Ekravo', rt.provider_url || ''],
    ['Mode', rt.mode === 'webhook' ? 'Webhook deliveries' : 'Polling'],
    ['Models', (rt.models || []).join(' → ') || '—'],
    ['Handle', `@${rt.handle}`],
    ['Workers', String(rt.workers)],
  ];
  if (rt.context_mode && rt.context_mode !== 'off') {
    rows.push(['Repository context', `${rt.context_mode} · ${rt.context_fetch || 'auto'}`]);
  }

  $('#runtime-rows').innerHTML = rows.map(([label, value, href]) => `
    <div class="setting-row">
      <span class="setting-label">${esc(label)}</span>
      ${href
        ? `<a class="setting-value setting-link" href="${esc(safeUrl(href))}" target="_blank" rel="noreferrer noopener">${esc(value)}<svg><use href="#i-external"/></svg></a>`
        : `<span class="setting-value">${esc(value)}</span>`}
    </div>`).join('');

  const switches = [
    ['Auto-approve', 'Submit APPROVE / REQUEST_CHANGES reviews', rt.auto_approve, 'AUTO_APPROVE'],
    ['Auto-merge', 'Merge a clean approval without a human', rt.auto_merge, 'AUTO_MERGE'],
    ['Web evidence', 'Check external claims against sources', rt.web_research, 'WEB_RESEARCH'],
  ];

  $('#switch-rows').innerHTML = switches.map(([label, hint, on, env]) => `
    <div class="setting-row">
      <span class="setting-text">
        <span class="setting-label">${esc(label)}</span>
        <span class="setting-hint">${esc(hint)}</span>
      </span>
      <button class="switch ${on ? 'is-on' : ''}" role="switch" aria-checked="${on}" aria-label="${esc(label)}" data-env="${env}" data-value="${on}">
        <span class="knob"></span>
      </button>
    </div>`).join('');

  $$('#switch-rows .switch').forEach((el) => {
    el.addEventListener('click', () => {
      const env = el.dataset.env;
      const next = el.dataset.value === 'true' ? 'false' : 'true';
      toast(`Set ${env}=${next} in .env, then restart.`);
    });
  });

  $('#security-notice').hidden = Boolean(rt.dashboard_auth);
}

/* ── learnings ─────────────────────────────────────────────────────────── */

const learnings = {
  repo: '',
  repos: [],
  items: [],
  editing: 0,
};

async function loadLearningsView() {
  const select = $('#learn-repo');
  if (!learnings.repos.length) {
    try {
      const data = await api('/api/repos');
      learnings.repos = (Array.isArray(data.repos) ? data.repos : []).map((r) => r.repo).filter(Boolean);
    } catch (error) {
      if (error.message !== 'Unauthorized') toast(error.message);
    }
    select.innerHTML = learnings.repos.length
      ? learnings.repos.map((repo) => `<option value="${esc(repo)}">${esc(repo)}</option>`).join('')
      : '<option value="">No repositories yet</option>';
  }
  if (!learnings.repos.includes(learnings.repo)) learnings.repo = learnings.repos[0] || '';
  select.value = learnings.repo;
  select.disabled = learnings.repos.length === 0;
  await loadLearnings();
}

async function loadLearnings() {
  const host = $('#learn-list');
  if (!learnings.repo) {
    learnings.items = [];
    host.innerHTML = emptyState('No repository', 'Learnings are scoped to a repository, and none has been reviewed yet.');
    $('#learn-count').textContent = '';
    return;
  }
  host.innerHTML = '<div class="skeleton-block"></div>';
  try {
    const data = await api(`/api/learnings?repo=${encodeURIComponent(learnings.repo)}`);
    learnings.items = Array.isArray(data.learnings) ? data.learnings : [];
    renderLearnings();
  } catch (error) {
    if (error.message !== 'Unauthorized') {
      host.innerHTML = emptyState('Could not load learnings', error.message);
      toast(error.message);
    }
  }
}

function renderLearnings() {
  const host = $('#learn-list');
  const items = learnings.items;
  $('#learn-count').textContent = items.length
    ? `${num(items.length)} rule${items.length === 1 ? '' : 's'} for ${learnings.repo}`
    : '';

  if (!items.length) {
    host.innerHTML = emptyState('No learnings yet', 'Add a rule and every future review of this repository follows it.');
    return;
  }

  host.innerHTML = items.map((item) => `
    <article class="learn-item${item.enabled ? '' : ' is-off'}">
      <div class="learn-main">
        <p class="learn-text">${esc(item.text)}</p>
        <div class="learn-meta">
          <span class="learn-glob">${esc(item.path_glob || 'every file')}</span>
          <span>${esc(item.source)}</span>
          ${item.hits ? `<span>${num(item.hits)} hits</span>` : ''}
          <span>${relativeTime(item.created_at)}</span>
        </div>
      </div>
      <div class="learn-actions">
        <button class="switch ${item.enabled ? 'is-on' : ''}" role="switch"
          aria-checked="${item.enabled}" aria-label="Enable learning" data-toggle="${item.id}">
          <span class="knob"></span>
        </button>
        <button class="ghost" data-edit="${item.id}">Edit</button>
        <button class="ghost danger" data-delete="${item.id}">Delete</button>
      </div>
    </article>`).join('');

  $$('[data-toggle]', host).forEach((el) => {
    el.addEventListener('click', () => setLearningEnabled(Number(el.dataset.toggle), !el.classList.contains('is-on')));
  });
  $$('[data-edit]', host).forEach((el) => {
    el.addEventListener('click', () => startEditLearning(Number(el.dataset.edit)));
  });
  $$('[data-delete]', host).forEach((el) => {
    el.addEventListener('click', () => deleteLearning(Number(el.dataset.delete)));
  });
}

async function submitLearning(event) {
  event.preventDefault();
  const text = $('#learn-text').value.trim();
  if (!text || !learnings.repo) return;
  const button = $('#learn-submit');
  button.disabled = true;
  const body = { repo: learnings.repo, text, path_glob: $('#learn-glob').value.trim() };
  try {
    if (learnings.editing) {
      await api('/api/learnings', { method: 'PATCH', body: { ...body, id: learnings.editing } });
      toast('Learning updated');
    } else {
      await api('/api/learnings', { method: 'POST', body });
      toast('Learning added');
    }
    resetLearningForm();
    await loadLearnings();
  } catch (error) {
    if (error.message !== 'Unauthorized') toast(error.message);
  } finally {
    button.disabled = false;
  }
}

async function setLearningEnabled(id, enabled) {
  try {
    await api('/api/learnings', { method: 'PATCH', body: { repo: learnings.repo, id, enabled } });
    await loadLearnings();
  } catch (error) {
    if (error.message !== 'Unauthorized') toast(error.message);
  }
}

async function deleteLearning(id) {
  try {
    await api(`/api/learnings?repo=${encodeURIComponent(learnings.repo)}&id=${id}`, { method: 'DELETE' });
    if (learnings.editing === id) resetLearningForm();
    toast('Learning removed');
    await loadLearnings();
  } catch (error) {
    if (error.message !== 'Unauthorized') toast(error.message);
  }
}

function startEditLearning(id) {
  const item = learnings.items.find((entry) => entry.id === id);
  if (!item) return;
  learnings.editing = id;
  $('#learn-text').value = item.text;
  $('#learn-glob').value = item.path_glob || '';
  $('#learn-submit').textContent = 'Save changes';
  $('#learn-cancel').hidden = false;
  $('#learn-text').focus();
}

function resetLearningForm() {
  learnings.editing = 0;
  $('#learn-text').value = '';
  $('#learn-glob').value = '';
  $('#learn-submit').textContent = 'Add learning';
  $('#learn-cancel').hidden = true;
}

/* ── drawer ────────────────────────────────────────────────────────────── */

async function openReview(id) {
  state.lastFocus = document.activeElement;
  $('#scrim').hidden = false;
  $('#drawer').classList.add('is-open');
  $('#drawer').setAttribute('aria-hidden', 'false');
  $('#drawer-kicker').textContent = 'Review';
  $('#drawer-title').textContent = 'Loading…';
  $('#drawer-body').innerHTML = '<div class="skeleton-block skeleton-tall"></div>';
  setTimeout(() => $('#drawer-close').focus(), 60);

  try {
    const data = await api(`/api/reviews/${encodeURIComponent(id)}`);
    renderReviewDetail(data.review, data.findings || []);
  } catch (error) {
    if (error.message !== 'Unauthorized') {
      $('#drawer-title').textContent = 'Could not load review';
      $('#drawer-body').innerHTML = emptyState('Unavailable', error.message);
    }
  }
}

function renderReviewDetail(review, findings) {
  $('#drawer-kicker').textContent = `${review.repo} · #${review.pr}`;
  $('#drawer-title').textContent = review.title || `Pull request #${review.pr}`;

  const chips = [
    `<span class="verdict ${verdictClass(review.verdict)}">${esc(verdictLabel(review.verdict))}</span>`,
    `<span class="detail-chip">${num(review.findings)} findings</span>`,
    review.model ? `<span class="detail-chip">${esc(review.model)}</span>` : '',
    review.files ? `<span class="detail-chip">${num(review.files)} files · +${num(review.additions)} / -${num(review.deletions)}</span>` : '',
    review.duration_ms ? `<span class="detail-chip"><svg><use href="#i-clock"/></svg>${duration(review.duration_ms)}</span>` : '',
    (review.prompt_tokens || review.completion_tokens)
      ? `<span class="detail-chip">${num((review.prompt_tokens || 0) + (review.completion_tokens || 0))} tokens</span>`
      : '',
    review.context_chars
      ? `<span class="detail-chip">${num(review.context_chars)} context chars</span>`
      : '',
    review.merged
      ? '<span class="detail-chip">Merged automatically</span>'
      : review.state === 'merged'
        ? '<span class="detail-chip">Merged</span>'
        : review.state === 'closed'
          ? '<span class="detail-chip">Closed</span>'
          : '',
  ].filter(Boolean).join('');

  const list = findings.length
    ? findings.map((f) => {
      const sources = f.evidence && Array.isArray(f.evidence.sources) && f.evidence.sources.length
        ? `<div class="finding-sources">
            <span>Sources</span>
            ${f.evidence.sources.map((s) => `<a href="${esc(safeUrl(s.url))}" target="_blank" rel="noopener noreferrer">${esc(s.title || s.url)}</a>`).join('')}
          </div>`
        : '';
      const verified = f.evidence_note
        ? `<p class="verified-note">${esc(f.evidence_note)}</p>`
        : '';
      return `<article class="finding">
        <div class="finding-top">
          <span class="sev sev-${esc(f.severity || 'low')}"></span>
          <span class="finding-title">${esc(f.title)}</span>
          <span class="finding-loc">${esc(f.file)}:${f.line}</span>
        </div>
        <p class="finding-detail">${esc(f.detail)}</p>
        ${f.fix ? `<p class="finding-fix"><strong>Fix:</strong> ${esc(f.fix)}</p>` : ''}
        ${verified}
        ${sources}
      </article>`;
    }).join('')
    : `<div class="drawer-empty">
        <strong>No findings</strong>
        <span>The review approved this change without issues.</span>
      </div>`;

  const link = review.url
    ? `<a class="detail-link" href="${esc(safeUrl(review.url))}" target="_blank" rel="noopener noreferrer">Open on GitHub <svg><use href="#i-external"/></svg></a>`
    : '';

  $('#drawer-body').innerHTML = `${link}<div class="detail-meta">${chips}</div>${list}`;
}

function closeDrawer() {
  $('#drawer').classList.remove('is-open');
  $('#drawer').setAttribute('aria-hidden', 'true');
  $('#scrim').hidden = true;
  if (state.lastFocus && typeof state.lastFocus.focus === 'function') state.lastFocus.focus();
}

/* ── gate ──────────────────────────────────────────────────────────────── */

function openGate() {
  $('#gate').hidden = false;
  setTimeout(() => $('#gate-token').focus(), 80);
}

async function submitGate(event) {
  event.preventDefault();
  const button = $('#gate-submit');
  const error = $('#gate-error');
  button.disabled = true;
  error.hidden = true;

  try {
    const res = await fetch('/api/session', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ token: $('#gate-token').value }),
    });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      throw new Error(body.error || 'Invalid token');
    }
    window.location.reload();
  } catch (err) {
    error.textContent = err.message;
    error.hidden = false;
    button.disabled = false;
  }
}

/* ── routing & controls ────────────────────────────────────────────────── */

function setView(view) {
  state.view = view;
  document.body.dataset.view = view;
  $$('.nav-item').forEach((el) => el.classList.toggle('is-active', el.dataset.view === view));
  $$('.view').forEach((el) => { el.hidden = el.id !== `view-${view}`; });
  $('#page-title').textContent = TITLES[view] || 'Overview';
  $('#range').hidden = view !== 'overview';
  $('#live').hidden = view === 'settings';

  if (view === 'reviews') {
    if (!state.reviewsLoaded) {
      state.reviewsLoaded = true;
      loadReviews(true);
    }
    requestAnimationFrame(() => movePill('#review-sort'));
  }
  if (view === 'repos' && !state.reposLoaded) {
    state.reposLoaded = true;
    loadRepos();
  }
  if (view === 'learnings') loadLearningsView();
  if (view === 'settings') loadRuntime();
}

function route() {
  const name = window.location.hash.replace(/^#\/?/, '').trim();
  setView(TITLES[name] ? name : 'overview');
}

function selectDays(days) {
  state.days = days;
  $('#range-label').textContent = `${days} days`;
  $$('#range-pop button').forEach((el) => {
    el.setAttribute('aria-selected', String(Number(el.dataset.days) === days));
  });
  loadOverview(false).catch((error) => {
    if (error.message !== 'Unauthorized') toast(error.message);
  });
}

let searchTimer;
function bindSearch() {
  const input = $('#search');
  const clear = $('#search-clear');

  input.addEventListener('input', () => {
    clear.hidden = !input.value;
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => {
      state.q = input.value.trim();
      loadReviews(true);
    }, 260);
  });

  clear.addEventListener('click', () => {
    input.value = '';
    clear.hidden = true;
    state.q = '';
    input.focus();
    loadReviews(true);
  });
}

function bindMenu() {
  const menu = $('#range');
  const trigger = $('#range-trigger');
  const pop = $('#range-pop');

  const setOpen = (open) => {
    menu.classList.toggle('is-open', open);
    trigger.setAttribute('aria-expanded', String(open));
    if (open) {
      const selected = pop.querySelector('[aria-selected="true"]') || pop.querySelector('button');
      if (selected) selected.focus();
    }
  };

  trigger.addEventListener('click', () => setOpen(!menu.classList.contains('is-open')));
  trigger.addEventListener('keydown', (event) => {
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      setOpen(true);
    }
  });

  pop.addEventListener('click', (event) => {
    const button = event.target.closest('button[data-days]');
    if (!button) return;
    selectDays(Number(button.dataset.days));
    setOpen(false);
    trigger.focus();
  });

  pop.addEventListener('keydown', (event) => {
    const items = $$('button', pop);
    const index = items.indexOf(document.activeElement);
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      items[(index + 1) % items.length].focus();
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      items[(index - 1 + items.length) % items.length].focus();
    } else if (event.key === 'Escape') {
      setOpen(false);
      trigger.focus();
    }
  });

  document.addEventListener('click', (event) => {
    if (!menu.contains(event.target)) setOpen(false);
  });
}

function bindAtmosphere() {
  const spot = $('#spot');
  if (!spot) return;
  const fine = window.matchMedia('(hover: hover) and (pointer: fine)').matches;
  if (!fine || reducedMotion()) {
    spot.style.display = 'none';
    return;
  }

  let targetX = window.innerWidth * .5;
  let targetY = window.innerHeight * .3;
  let x = targetX;
  let y = targetY;
  let frame = 0;

  const loop = () => {
    x += (targetX - x) * .07;
    y += (targetY - y) * .07;
    spot.style.transform = `translate3d(${x - 310}px, ${y - 310}px, 0)`;
    if (Math.abs(targetX - x) > .4 || Math.abs(targetY - y) > .4) {
      frame = requestAnimationFrame(loop);
    } else {
      frame = 0;
    }
  };

  window.addEventListener('pointermove', (event) => {
    targetX = event.clientX;
    targetY = event.clientY;
    if (!frame) frame = requestAnimationFrame(loop);
  }, { passive: true });
}

function bindGlare() {
  if (reducedMotion()) return;
  let frame = 0;
  let last = null;

  document.addEventListener('pointermove', (event) => {
    last = event;
    if (frame) return;
    frame = requestAnimationFrame(() => {
      frame = 0;
      const el = last.target.closest ? last.target.closest('.card, .repo-card') : null;
      if (!el) return;
      const rect = el.getBoundingClientRect();
      el.style.setProperty('--cx', `${last.clientX - rect.left}px`);
      el.style.setProperty('--cy', `${last.clientY - rect.top}px`);
    });
  }, { passive: true });
}

function bind() {
  bindAtmosphere();
  bindGlare();

  $$('.nav-item').forEach((el) => {
    el.addEventListener('click', () => {
      window.location.hash = `#/${el.dataset.view}`;
    });
  });

  window.addEventListener('hashchange', route);

  $('#review-board').addEventListener('click', (event) => {
    const card = event.target.closest('.queue-card');
    if (card) openReview(card.dataset.id);
  });

  bindSearch();
  bindMenu();
  $$('#review-sort .seg-btn').forEach((el) => {
    el.addEventListener('click', () => {
      if (state.reviewSort === el.dataset.reviewSort) return;
      state.reviewSort = el.dataset.reviewSort || 'newest';
      $$('#review-sort .seg-btn').forEach((btn) => {
        btn.classList.toggle('is-active', btn === el);
        btn.setAttribute('aria-selected', String(btn === el));
      });
      movePill('#review-sort');
      loadReviews(true);
    });
  });
  window.addEventListener('resize', moveActivePill);

  $('#see-all').addEventListener('click', () => { window.location.hash = '#/reviews'; });

  $('#learn-form').addEventListener('submit', submitLearning);
  $('#learn-cancel').addEventListener('click', resetLearningForm);
  $('#learn-refresh').addEventListener('click', () => loadLearnings());
  $('#learn-repo').addEventListener('change', (event) => {
    learnings.repo = event.target.value;
    resetLearningForm();
    loadLearnings();
  });

  $('#refresh').addEventListener('click', async () => {
    const button = $('#refresh');
    button.classList.add('is-spinning');
    try {
      if (state.view === 'overview') await loadOverview(false);
      else if (state.view === 'reviews') await loadReviews(true);
      else if (state.view === 'repos') await loadRepos();
      else if (state.view === 'learnings') await loadLearnings();
      else await loadRuntime();
      toast('Refreshed');
    } catch (error) {
      if (error.message !== 'Unauthorized') toast(error.message);
    } finally {
      setTimeout(() => button.classList.remove('is-spinning'), 700);
    }
  });

  $('#load-more').addEventListener('click', () => loadReviews(false));

  $('#drawer-close').addEventListener('click', closeDrawer);
  $('#scrim').addEventListener('click', closeDrawer);

  $('#gate-form').addEventListener('submit', submitGate);

  $('#watch-all').addEventListener('click', () => saveWatch({ all: !state.watch.all }));

  const repoSearch = $('#repo-search');
  repoSearch.addEventListener('input', () => {
    state.repoQuery = repoSearch.value;
    $('#repo-search-clear').hidden = !repoSearch.value;
    renderRepos();
  });
  $('#repo-search-clear').addEventListener('click', () => {
    repoSearch.value = '';
    state.repoQuery = '';
    $('#repo-search-clear').hidden = true;
    renderRepos();
    repoSearch.focus();
  });
  $$('#repo-filter .seg-btn').forEach((el) => {
    el.addEventListener('click', () => {
      state.repoFilter = el.dataset.repoFilter || 'all';
      $$('#repo-filter .seg-btn').forEach((b) => {
        b.classList.toggle('is-active', b === el);
        b.setAttribute('aria-selected', String(b === el));
      });
      renderRepos();
      movePill('#repo-filter');
    });
  });
  $('#repo-grid').addEventListener('click', (event) => {
    const toggle = event.target.closest('.repo-toggle');
    if (!toggle || toggle.disabled) return;
    const repo = toggle.dataset.repo;
    const watched = state.watch.repos.some((entry) => entry.toLowerCase() === repo.toLowerCase());
    const repos = watched
      ? state.watch.repos.filter((entry) => entry.toLowerCase() !== repo.toLowerCase())
      : state.watch.repos.concat(repo);
    saveWatch({ repos });
  });

  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && $('#drawer').classList.contains('is-open')) closeDrawer();
    if (event.key === '/' && document.activeElement.tagName !== 'INPUT' && document.activeElement.tagName !== 'TEXTAREA') {
      if (state.view === 'reviews') {
        event.preventDefault();
        $('#search').focus();
      } else if (state.view === 'repos') {
        event.preventDefault();
        $('#repo-search').focus();
      }
    }
  });

  let resizeTimer;
  window.addEventListener('resize', () => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(moveActivePill, 120);
  });
}

/* ── boot ──────────────────────────────────────────────────────────────── */

document.addEventListener('DOMContentLoaded', () => {
  bind();
  route();
  loadOverview(false).catch((error) => {
    if (error.message !== 'Unauthorized') {
      toast(error.message);
      $('#app').setAttribute('aria-busy', 'false');
    }
  });

  setInterval(() => {
    if (document.visibilityState === 'visible' && state.view === 'overview') {
      loadOverview(true).catch(() => {});
    }
  }, 60000);
});
