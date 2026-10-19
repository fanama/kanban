const ACTOR_KEY = 'kanban.actor';
const CLIENT_KEY = 'kanban.client';

export function actor() {
  return localStorage.getItem(ACTOR_KEY) || '';
}

export function actorLabel() {
  return actor() || 'Someone';
}

export function setActor(name) {
  const clean = (name || '').trim().slice(0, 40);
  if (clean) localStorage.setItem(ACTOR_KEY, clean);
  else localStorage.removeItem(ACTOR_KEY);
  renderActorChip();
}

export function clientId() {
  let id = sessionStorage.getItem(CLIENT_KEY);
  if (!id) {
    id = (crypto.randomUUID && crypto.randomUUID()) || `tab-${Date.now()}-${Math.random().toString(16).slice(2)}`;
    sessionStorage.setItem(CLIENT_KEY, id);
  }
  return id;
}

export function identityHeaders() {
  return {
    'X-Kanban-Actor': actorLabel(),
    'X-Kanban-Client': clientId()
  };
}

export function renderActorChip() {
  const chip = document.getElementById('actor-chip');
  if (chip) chip.textContent = actorLabel();
}

export function promptForActor() {
  const dialog = document.getElementById('name-dialog');
  const input = document.getElementById('name-input');
  if (!dialog || !input) return;
  input.value = actor();
  if (!actor() && typeof dialog.showModal === 'function') dialog.showModal();
  document.getElementById('name-save')?.addEventListener('click', () => {
    setActor(input.value);
    dialog.close();
  });
  document.getElementById('name-skip')?.addEventListener('click', () => {
    setActor(input.value || 'Someone');
    dialog.close();
  });
  document.getElementById('actor-chip')?.addEventListener('click', () => {
    input.value = actor();
    if (typeof dialog.showModal === 'function') dialog.showModal();
  });
}
