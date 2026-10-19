import { identityHeaders } from './identity.js';
import { applyPatch, resyncIfNeeded } from './patches.js';
import { renderMarkdown } from './markdown.js';
import { clearError, highlight, showConflict, showError } from './ui.js';

const MUTATION_PATHS = [
  /^\/cards(\/|$)/,
  /^\/columns(\/|$)/,
  /^\/boards(\/|$)/
];

export function initForms() {
  document.addEventListener('submit', (event) => {
    const form = event.target.closest('form');
    if (!form || !isMutation(form)) return;
    if (form.dataset.skipOptimistic === 'true') return;
    event.preventDefault();
    event.stopImmediatePropagation();
    handleForm(form);
  }, true);
}

function isMutation(form) {
  const path = new URL(form.action, location.href).pathname;
  return MUTATION_PATHS.some((pattern) => pattern.test(path));
}

async function handleForm(form, overwrite = false) {
  const path = new URL(form.action, location.href).pathname;
  if (path.endsWith('/delete') && form.dataset.confirmed !== 'true') {
    const message = form.getAttribute('hx-confirm') || 'Delete this item?';
    if (!window.confirm(message)) return;
  }
  const rollback = overwrite ? null : optimistic(form, path);
  try {
    const envelope = await postForm(form);
    if (envelope.type === 'redirect') {
      location.href = envelope.url;
      return;
    }
    applyPatch(envelope, { fromSelf: true });
    document.querySelectorAll('[data-pending="true"]').forEach((node) => node.remove());
    clearError();
    refreshCounts();
    resyncIfNeeded();
  } catch (error) {
    rollback?.();
    showError(error.message);
    if (error.status === 409) await handleConflict(form);
    else if (overwrite) form.dataset.skipOptimistic = 'false';
  }
}

async function postForm(form) {
  const response = await fetch(form.action, {
    method: 'POST',
    body: new FormData(form),
    headers: { ...identityHeaders(), 'X-Kanban-Patch': 'true' }
  });
  const text = await response.text();
  if (!response.ok) {
    const error = new Error(text || response.statusText);
    error.status = response.status;
    throw error;
  }
  return JSON.parse(text);
}

function optimistic(form, path) {
  if (path === '/cards') return optimisticAdd(form);
  if (path === '/cards/move') return optimisticMoveForm(form);
  if (/^\/cards\/[^/]+\/delete$/.test(path)) return optimisticDelete(form, path);
  if (/^\/cards\/[^/]+$/.test(path)) return optimisticEdit(form, path);
  if (path === '/columns') return optimisticColumnAdd(form);
  if (/^\/columns\/[^/]+\/rename$/.test(path)) return optimisticColumnRename(form, path);
  if (/^\/columns\/[^/]+\/delete$/.test(path)) return optimisticColumnDelete(form, path);
  if (path === '/boards') return optimisticBoardCreate(form);
  if (/^\/boards\/[^/]+\/delete$/.test(path)) return optimisticBoardDelete(path);
  return null;
}

function optimisticAdd(form) {
  const columnId = form.querySelector('[name="column_id"]')?.value || '';
  const title = form.querySelector('[name="title"]')?.value?.trim();
  if (!columnId || !title) return null;
  const column = document.querySelector(`[data-column-id="${CSS.escape(columnId)}"]`);
  const cards = column?.querySelector('[data-column-cards]');
  if (!cards) return null;
  const card = buildCard(title, form.querySelector('[name="priority"]')?.value || 'Low', columnId);
  cards.querySelector('.empty')?.remove();
  cards.append(card);
  renderMarkdown(card);
  refreshCounts();
  return () => {
    card.remove();
    refreshCounts();
    if (!cards.querySelector('.card')) cards.append(emptyState());
  };
}

function optimisticMoveForm(form) {
  const cardId = form.querySelector('[name="card_id"]')?.value;
  const columnId = form.querySelector('[name="column_id"]')?.value;
  const card = document.querySelector(`[data-card-id="${CSS.escape(cardId)}"]`);
  if (!card || !columnId) return null;
  return moveNode(card, columnId);
}

function optimisticDelete(form, path) {
  const cardId = path.split('/')[2];
  const card = document.querySelector(`[data-card-id="${CSS.escape(cardId)}"]`);
  if (!card) return null;
  const parent = card.parentElement;
  const next = card.nextElementSibling;
  card.remove();
  refreshCounts();
  return () => {
    if (next) parent.insertBefore(card, next);
    else parent.append(card);
    refreshCounts();
  };
}

function optimisticEdit(form, path) {
  const cardId = path.split('/')[2];
  const card = document.querySelector(`[data-card-id="${CSS.escape(cardId)}"]`);
  if (!card) return null;
  const snapshot = card.cloneNode(true);
  const parent = card.parentElement;
  const next = card.nextElementSibling;
  const title = form.querySelector('[name="title"]')?.value?.trim();
  if (title) card.querySelector('.card-title-button').textContent = title;
  setMarkdown(card, '.desc', form.querySelector('[name="description"]')?.value || '');
  setMarkdown(card, '.field .md', form.querySelector('[name="objective"]')?.value || '');
  const priority = form.querySelector('[name="priority"]')?.value;
  if (priority) setPriority(card, priority);
  const columnId = form.querySelector('[name="column_id"]')?.value;
  if (columnId && card.dataset.columnId !== columnId) moveNode(card, columnId);
  refreshCounts();
  return () => {
    if (next) parent.insertBefore(snapshot, next);
    else parent.append(snapshot);
    refreshCounts();
  };
}

function optimisticColumnAdd(form) {
  const name = form.querySelector('[name="name"]')?.value?.trim();
  if (!name) return null;
  const columns = document.querySelector('.columns');
  if (!columns) return null;
  const section = document.createElement('section');
  section.className = 'column column--pending';
  section.dataset.pending = 'true';
  section.dataset.columnId = `tmp_${Date.now()}`;
  section.innerHTML = `<h2 class="column-head" data-column-head><span class="name"></span><span class="count">0</span></h2>
    <div class="cards" data-column-cards><p class="empty">No cards yet</p></div>`;
  section.querySelector('.name').textContent = name;
  columns.append(section);
  return () => section.remove();
}

function optimisticColumnRename(form, path) {
  const columnId = path.split('/')[2];
  const name = form.querySelector('[name="name"]')?.value?.trim();
  const column = document.querySelector(`[data-column-id="${CSS.escape(columnId)}"]`);
  const label = column?.querySelector('[data-column-head] .name');
  if (!label || !name) return null;
  const previous = label.textContent;
  label.textContent = name;
  return () => { label.textContent = previous; };
}

function optimisticBoardCreate(form) {
  const name = form.querySelector('[name="name"]')?.value?.trim();
  const boards = document.querySelector('[data-sidebar-boards]');
  if (!name || !boards) return null;
  const entry = document.createElement('a');
  entry.className = 'board-tab board-tab--pending';
  entry.href = '#';
  entry.dataset.pending = 'true';
  entry.setAttribute('aria-disabled', 'true');
  const dot = document.createElement('span');
  dot.className = 'board-dot';
  dot.setAttribute('aria-hidden', 'true');
  const label = document.createElement('span');
  label.className = 'board-tab-name';
  label.textContent = name;
  entry.append(dot, label);
  boards.append(entry);
  return () => entry.remove();
}

function optimisticBoardDelete(path) {
  const id = path.split('/')[2];
  const boards = document.querySelector('[data-sidebar-boards]');
  const entry = boards && [...boards.querySelectorAll('a.board-tab')]
    .find((tab) => tab.getAttribute('href') === `/?board=${id}`);
  if (!entry) return null;
  const parent = entry.parentElement;
  const next = entry.nextElementSibling;
  entry.remove();
  return () => {
    if (next) parent.insertBefore(entry, next);
    else parent.append(entry);
  };
}

function optimisticColumnDelete(form, path) {
  const columnId = path.split('/')[2];
  const column = document.querySelector(`[data-column-id="${CSS.escape(columnId)}"]`);
  if (!column) return null;
  const parent = column.parentElement;
  const next = column.nextElementSibling;
  column.remove();
  refreshCounts();
  return () => {
    if (next) parent.insertBefore(column, next);
    else parent.append(column);
    refreshCounts();
  };
}

export function moveCard(card, columnId) {
  const rollback = moveNode(card, columnId);
  const body = new FormData();
  body.set('card_id', card.dataset.cardId);
  body.set('column_id', columnId);
  fetch('/cards/move', {
    method: 'POST',
    body,
    headers: { ...identityHeaders(), 'X-Kanban-Patch': 'true' }
  }).then(async (response) => {
    const text = await response.text();
    if (!response.ok) throw new Error(text);
    applyPatch(JSON.parse(text), { fromSelf: true });
    clearError();
  }).catch((error) => {
    rollback?.();
    showError(error.message);
  });
}

function moveNode(card, columnId) {
  const target = document.querySelector(`[data-column-id="${CSS.escape(columnId)}"] [data-column-cards]`);
  if (!target) return null;
  const parent = card.parentElement;
  const next = card.nextElementSibling;
  target.querySelector('.empty')?.remove();
  target.append(card);
  card.dataset.columnId = columnId;
  refreshCounts();
  return () => {
    if (next) parent.insertBefore(card, next);
    else parent.append(card);
    card.dataset.columnId = parent.closest('[data-column-id]')?.dataset.columnId || card.dataset.columnId;
    refreshCounts();
  };
}

function buildCard(title, priority, columnId) {
  const card = document.createElement('article');
  card.className = `card prio-${priority} card--pending`;
  card.dataset.cardId = `tmp_${Date.now()}`;
  card.dataset.columnId = columnId;
  card.dataset.pending = 'true';
  const head = document.createElement('h3');
  head.className = 'card-title';
  const button = document.createElement('button');
  button.type = 'button';
  button.className = 'card-title-button';
  button.textContent = title;
  head.append(button);
  const desc = document.createElement('div');
  desc.className = 'desc md';
  desc.dataset.markdown = '';
  desc.textContent = '';
  const foot = document.createElement('div');
  foot.className = 'card-foot';
  const badge = document.createElement('span');
  badge.className = `badge prio-${priority}`;
  badge.textContent = priority;
  foot.append(badge);
  card.append(head, desc, foot);
  return card;
}

function setMarkdown(card, selector, value) {
  const node = card.querySelector(selector);
  if (!value.trim()) {
    node?.remove();
    return;
  }
  if (node) {
    node.textContent = value;
    delete node.dataset.mdRendered;
    renderMarkdown(node);
    return;
  }
  const created = document.createElement('div');
  created.className = selector.includes('field') ? 'field' : 'desc md';
  if (selector.includes('field')) {
    const label = document.createElement('span');
    label.textContent = 'Objective';
    created.append(label);
  }
  const body = document.createElement('div');
  body.className = 'md';
  body.dataset.markdown = '';
  body.textContent = value;
  created.append(body);
  card.querySelector('.card-foot')?.before(created);
  renderMarkdown(created);
}

function setPriority(card, priority) {
  card.className = card.className.replace(/prio-\w+/g, `prio-${priority}`);
  const badge = card.querySelector('.badge');
  if (badge) {
    badge.className = `badge prio-${priority}`;
    badge.textContent = priority;
  }
}

function refreshCounts() {
  const columns = [...document.querySelectorAll('.column')];
  let total = 0;
  for (const column of columns) {
    const count = column.querySelectorAll('[data-column-cards] .card').length;
    total += count;
    const label = column.querySelector('.count');
    if (label) label.textContent = String(count);
  }
  const summary = document.querySelector('[data-board-summary]');
  if (summary) summary.textContent = `${total} card${total === 1 ? '' : 's'} · ${columns.length} list${columns.length === 1 ? '' : 's'}`;
}

function emptyState() {
  const p = document.createElement('p');
  p.className = 'empty';
  p.textContent = 'No cards yet';
  return p;
}

async function handleConflict(form) {
  const cardId = new URL(form.action, location.href).pathname.split('/')[2];
  const response = await fetch(`/cards/${encodeURIComponent(cardId)}/edit`, {
    headers: { 'HX-Request': 'true' }
  });
  if (!response.ok) {
    form.closest('article')?.remove();
    showError('This card no longer exists.');
    return;
  }
  const html = await response.text();
  const doc = new DOMParser().parseFromString(html, 'text/html');
  const version = doc.querySelector('input[name="version"]')?.value || '';
  showConflict({
    title: 'This card changed while you were editing. Reload the latest version or overwrite it with your text.',
    allowOverwrite: true,
    onReload: () => location.reload(),
    onOverwrite: () => {
      const input = form.querySelector('[name="version"]');
      if (input) input.value = version;
      form.dataset.confirmed = 'true';
      handleForm(form, true);
    }
  });
}

export function initDrag() {
  let dragged = null;
  document.addEventListener('dragstart', (event) => {
    const card = event.target.closest('article.card[data-card-id]');
    if (!card || card.classList.contains('card-form') || card.dataset.pending === 'true') {
      event.preventDefault();
      return;
    }
    dragged = card;
    card.classList.add('dragging');
    event.dataTransfer.effectAllowed = 'move';
    event.dataTransfer.setData('text/plain', card.dataset.cardId);
  });
  document.addEventListener('dragend', () => {
    document.querySelectorAll('.column.drop-ok').forEach((node) => node.classList.remove('drop-ok'));
    dragged?.classList.remove('dragging');
    dragged = null;
  });
  document.addEventListener('dragover', (event) => {
    const column = event.target.closest('.column[data-column-id]');
    if (!column || !dragged) return;
    if (accepts(column.dataset.columnId)) {
      event.preventDefault();
      event.dataTransfer.dropEffect = 'move';
      document.querySelectorAll('.column.drop-ok').forEach((node) => {
        if (node !== column) node.classList.remove('drop-ok');
      });
      column.classList.add('drop-ok');
    }
  });
  document.addEventListener('dragleave', (event) => {
    const column = event.target.closest('.column[data-column-id]');
    if (column && !column.contains(event.relatedTarget)) column.classList.remove('drop-ok');
  });
  document.addEventListener('drop', (event) => {
    const column = event.target.closest('.column[data-column-id]');
    if (!column || !dragged) return;
    event.preventDefault();
    const card = dragged;
    const allowed = accepts(column.dataset.columnId);
    document.querySelectorAll('.column.drop-ok').forEach((node) => node.classList.remove('drop-ok'));
    card.classList.remove('dragging');
    dragged = null;
    if (allowed) moveCard(card, column.dataset.columnId);
  });
  function accepts(columnId) {
    if (!dragged) return false;
    if (columnId === dragged.dataset.prevColumn) return true;
    return columnId === dragged.dataset.nextColumn && dragged.dataset.canAdvance === 'true';
  }
}
