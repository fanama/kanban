import { initDrag, initForms } from './actions.js';
import { promptForActor } from './identity.js';
import { renderMarkdown } from './markdown.js';
import { resyncIfNeeded } from './patches.js';
import { clearError, setLiveStatus, showError } from './ui.js';
import { startWS } from './ws.js';

function initSidebar() {
  const toggle = document.getElementById('sidebar-toggle');
  const setSidebar = (open) => {
    document.body.classList.toggle('sidebar-open', open);
    toggle?.setAttribute('aria-expanded', String(open));
  };
  toggle?.addEventListener('click', () => setSidebar(!document.body.classList.contains('sidebar-open')));
  document.addEventListener('click', (event) => {
    if (event.target.closest('[data-sidebar-close]')) setSidebar(false);
    const tab = event.target.closest('#sidebar a.board-tab');
    if (tab) {
      document.querySelectorAll('#sidebar a.board-tab').forEach((node) => node.classList.remove('active'));
      tab.classList.add('active');
      setSidebar(false);
    }
  });
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') setSidebar(false);
  });
}

function initDialog() {
  const dialog = document.getElementById('card-dialog');
  const body = document.getElementById('card-dialog-body');
  const close = () => {
    if (dialog?.open) dialog.close();
    if (body) body.innerHTML = '';
  };
  document.addEventListener('click', async (event) => {
    const opener = event.target.closest('[data-card-open]');
    if (!opener) return;
    event.preventDefault();
    if (!dialog || !body) return;
    try {
      const response = await fetch(`/cards/${encodeURIComponent(opener.dataset.cardOpen)}`, {
        headers: { 'HX-Request': 'true' }
      });
      if (!response.ok) {
        showError((await response.text()).trim());
        return;
      }
      clearError();
      body.innerHTML = await response.text();
      window.htmx?.process(body);
      renderMarkdown(body);
      if (typeof dialog.showModal === 'function') dialog.showModal();
      else dialog.setAttribute('open', '');
    } catch {
      showError('Could not open the card. Try again.');
    }
  });
  document.addEventListener('click', (event) => {
    if (event.target.closest('[data-dialog-close]')) close();
  });
  if (dialog) {
    dialog.addEventListener('click', (event) => {
      if (event.target === dialog) close();
    });
    dialog.addEventListener('close', () => {
      if (body) body.innerHTML = '';
      clearError();
    });
  }
}

function initHTMX() {
  document.body.addEventListener('htmx:responseError', (event) => {
    showError(event.detail.xhr.responseText.trim());
  });
  document.body.addEventListener('htmx:afterRequest', (event) => {
    if (!event.detail.successful) return;
    clearError();
    const verb = event.detail.requestConfig?.verb;
    if (verb !== 'get') {
      const dialog = document.getElementById('card-dialog');
      if (dialog?.open) dialog.close();
    }
  });
  document.body.addEventListener('htmx:afterSwap', (event) => {
    renderMarkdown(event.target);
    resyncIfNeeded();
  });
}

window.addEventListener('load', () => {
  renderMarkdown(document);
  promptForActor();
  initSidebar();
  initDialog();
  initHTMX();
  initForms();
  initDrag();
  setLiveStatus('Connecting…', 'connecting');
  startWS();
});
