import { clientId } from './identity.js';
import { renderMarkdown } from './markdown.js';
import { highlight, showConflict, showError, showToast } from './ui.js';

const lastSeq = new Map();
let needsResync = false;

export function boardId() {
  return document.getElementById('board')?.dataset.boardId || '';
}

export async function resyncBoard() {
  const id = boardId();
  if (!id) return;
  if (document.querySelector('form[data-editing-card]')) {
    needsResync = true;
    showError('Live updates are paused while you edit this card.');
    return;
  }
  const res = await fetch(`/?board=${encodeURIComponent(id)}`, {
    headers: { 'HX-Request': 'true' }
  });
  if (!res.ok) return;
  const html = await res.text();
  const board = document.getElementById('board');
  if (!board) return;
  const template = document.createElement('template');
  template.innerHTML = html.trim();
  const next = template.content.querySelector('#board') || template.content.firstElementChild;
  if (!next) return;
  board.replaceWith(next);
  lastSeq.delete(id); // a full resync resets the sequence baseline
  needsResync = false;
  window.htmx?.process(next);
  renderMarkdown(next);
}

export function resyncIfNeeded() {
  if (needsResync && !document.querySelector('form[data-editing-card]')) resyncBoard();
}

export function applyPatch(envelope, options = {}) {
  if (!envelope || envelope.type !== 'patch') return;
  const own = options.fromSelf === true;
  if (!own && envelope.client_id && envelope.client_id === clientId()) return;
  const id = envelope.board_id || boardId();
  const seq = Number(envelope.seq || 0);
  if (!own && seq > 0) {
    const previous = lastSeq.get(id) || 0;
    if (seq <= previous) return;
    lastSeq.set(id, seq);
  }
  if (envelope.event === 'board.deleted' && envelope.board_id === boardId()) {
    location.href = '/';
    return;
  }
  if (!own && envelope.notice?.card_id && envelope.event.startsWith('card.')) {
    const form = document.querySelector(`form[data-editing-card="${CSS.escape(envelope.notice.card_id)}"]`);
    if (form && envelope.event === 'card.deleted') {
      form.closest('article')?.remove();
      const dialog = document.getElementById('card-dialog');
      if (dialog?.open) dialog.close();
    } else if (form) {
      showConflict({
        title: 'This card changed while you were editing. Reload the latest version to see the change.',
        allowOverwrite: false,
        onReload: () => location.reload(),
        onKeep: () => {}
      });
      return;
    }
  }
  if (envelope.reload) {
    resyncBoard();
  } else {
    applyFragments(envelope);
  }
  if (envelope.notice && !own) showToast(envelope.notice);
  const board = document.getElementById('board');
  window.htmx?.process(board);
  renderMarkdown(board);
}

function applyFragments(envelope) {
  for (const fragment of envelope.fragments || []) {
    applyFragment(fragment);
  }
  for (const removal of envelope.remove || []) {
    const selector = removal.target === 'column'
      ? `[data-column-id="${CSS.escape(removal.id)}"]`
      : `[data-card-id="${CSS.escape(removal.id)}"]`;
    document.querySelectorAll(selector).forEach((node) => node.remove());
  }
}

function applyFragment(fragment) {
  if (!fragment || !fragment.html) return;
  if (fragment.target === 'sidebar-boards') {
    replaceSelector('[data-sidebar-boards]', fragment.html);
    return;
  }
  if (fragment.target === 'board-summary') {
    replaceSelector('[data-board-summary]', fragment.html);
    return;
  }
  if (fragment.target === 'columns') {
    const container = document.querySelector('.columns');
    if (container) container.innerHTML = fragment.html;
    return;
  }
  const column = document.querySelector(`[data-column-id="${CSS.escape(fragment.id)}"]`);
  if (fragment.target === 'column-head') {
    const head = column?.querySelector('[data-column-head]');
    if (head) replaceNode(head, fragment.html);
    return;
  }
  if (fragment.target === 'column-cards') {
    const cards = column?.querySelector('[data-column-cards]');
    if (cards) {
      cards.innerHTML = fragment.html;
      for (const card of cards.querySelectorAll('.card')) highlight(card);
    }
    return;
  }
  if (fragment.target === 'card') {
    const cards = [...document.querySelectorAll(`[data-card-id="${CSS.escape(fragment.id)}"]`)];
    const editing = document.querySelector(`form[data-editing-card="${CSS.escape(fragment.id)}"]`);
    if (editing) {
      // While editing, HTMX has swapped the card for its form article, which
      // has no data-card-id. Replace that article (and any other view of the
      // same card, such as the dialog) instead of appending a second copy.
      const article = editing.closest('article') || editing;
      replaceNode(article, fragment.html, true);
      cards.forEach((card) => { if (card !== article) replaceNode(card, fragment.html, true); });
      return;
    }
    if (cards.length) {
      cards.forEach((card) => replaceNode(card, fragment.html, true));
      return;
    }
    const node = parsedNode(fragment.html);
    const target = node?.dataset.columnId
      ? document.querySelector(`[data-column-id="${CSS.escape(node.dataset.columnId)}"] [data-column-cards]`)
      : null;
    if (node && target) {
      target.querySelector('.empty')?.remove();
      target.append(node);
      highlight(node);
      renderMarkdown(node);
    }
  }
}

function replaceSelector(selector, html) {
  const old = document.querySelector(selector);
  if (old) replaceNode(old, html);
}

function replaceNode(old, html, isCard = false) {
  const next = parsedNode(html);
  if (!next) return;
  old.replaceWith(next);
  if (isCard) {
    highlight(next);
    renderMarkdown(next);
  }
}

function parsedNode(html) {
  const template = document.createElement('template');
  template.innerHTML = html.trim();
  return template.content.firstElementChild;
}
