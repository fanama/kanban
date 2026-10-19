const MAX_TOASTS = 3;

function toastText(notice) {
  const actor = notice.actor || 'Someone';
  const title = notice.card_title || 'a card';
  switch (notice.kind) {
    case 'card.added': return `${actor} added “${title}” to ${notice.to || 'a list'}`;
    case 'card.moved': return `${actor} moved “${title}” to ${notice.to || 'a list'}`;
    case 'card.updated': return `${actor} updated “${title}”`;
    case 'card.deleted': return `${actor} deleted “${title}”`;
    case 'column.added': return `${actor} added list “${notice.column_name || 'a list'}”`;
    case 'column.renamed': return `${actor} renamed a list to “${notice.column_name || 'a list'}”`;
    case 'column.deleted': return `${actor} deleted list “${notice.column_name || 'a list'}”`;
    case 'board.created': return `${actor} created board “${notice.board_name || 'a board'}”`;
    case 'board.deleted': return `${actor} deleted board “${notice.board_name || 'a board'}”`;
    case 'board.imported': return `${actor} loaded a board`;
    default: return `${actor} changed the board`;
  }
}

export function showToast(notice) {
  const region = document.getElementById('toast-region');
  if (!region || !notice) return;
  while (region.children.length >= MAX_TOASTS) region.firstElementChild.remove();
  const toast = document.createElement('div');
  toast.className = 'toast';
  toast.setAttribute('role', 'status');
  const text = document.createElement('span');
  text.textContent = toastText(notice);
  const close = document.createElement('button');
  close.type = 'button';
  close.className = 'toast-close';
  close.setAttribute('aria-label', 'Dismiss');
  close.textContent = '✕';
  close.addEventListener('click', () => toast.remove());
  toast.append(text, close);
  if (notice.card_id) {
    toast.classList.add('toast-clickable');
    toast.addEventListener('click', (event) => {
      if (event.target === close) return;
      const card = document.querySelector(`[data-card-id="${CSS.escape(notice.card_id)}"]`);
      if (card) {
        card.scrollIntoView({ block: 'center', behavior: 'smooth' });
        card.classList.add('is-live-updated');
        setTimeout(() => card.classList.remove('is-live-updated'), 1600);
      }
    });
  }
  region.append(toast);
  let timer = setTimeout(() => toast.remove(), 5000);
  const pause = () => clearTimeout(timer);
  const resume = () => { timer = setTimeout(() => toast.remove(), 5000); };
  toast.addEventListener('mouseenter', pause);
  toast.addEventListener('mouseleave', resume);
  toast.addEventListener('focusin', pause);
  toast.addEventListener('focusout', resume);
}

export function setLiveStatus(label, state) {
  const status = document.getElementById('live-status');
  if (!status) return;
  status.dataset.state = state;
  status.querySelector('span:last-child').textContent = label;
  status.title = label;
}

export function showError(message) {
  const flash = document.getElementById('flash');
  if (!flash) return;
  flash.textContent = (message || '').trim();
  flash.hidden = false;
}

export function clearError() {
  const flash = document.getElementById('flash');
  if (!flash) return;
  flash.textContent = '';
  flash.hidden = true;
}

export function showConflict({ title, allowOverwrite, onReload, onOverwrite, onKeep }) {
  const dialog = document.getElementById('conflict-dialog');
  const text = document.getElementById('conflict-text');
  if (!dialog || !text) return;
  text.textContent = title;
  const reload = document.getElementById('conflict-reload');
  const overwrite = document.getElementById('conflict-overwrite');
  const keep = document.getElementById('conflict-keep');
  overwrite.hidden = !allowOverwrite;
  reload.onclick = () => { dialog.close(); onReload?.(); };
  overwrite.onclick = () => { dialog.close(); onOverwrite?.(); };
  keep.onclick = () => { dialog.close(); onKeep?.(); };
  if (typeof dialog.showModal === 'function') dialog.showModal();
}

export function highlight(node) {
  if (!node) return;
  node.classList.add('is-live-updated');
  setTimeout(() => node.classList.remove('is-live-updated'), 1600);
}
