import { clientId } from './identity.js';
import { applyPatch, boardId, resyncBoard } from './patches.js';
import { setLiveStatus } from './ui.js';

let socket = null;
let attempts = 0;
let everConnected = false;
let pollTimer = null;
let hiddenAt = 0;

function wsURL() {
  const id = boardId();
  if (!id) return '';
  const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${protocol}//${location.host}/ws?board=${encodeURIComponent(id)}&client=${encodeURIComponent(clientId())}`;
}

function stopPolling() {
  if (pollTimer) clearInterval(pollTimer);
  pollTimer = null;
}

function startPolling() {
  if (pollTimer) return;
  setLiveStatus('Polling', 'polling');
  pollTimer = setInterval(() => {
    if (!document.hidden && (!socket || socket.readyState !== WebSocket.OPEN)) resyncBoard();
  }, 5000);
}

function scheduleReconnect() {
  attempts += 1;
  const delay = Math.min(15000, 500 * 2 ** Math.min(attempts, 5));
  if (attempts >= 5) startPolling();
  setTimeout(connect, delay + Math.random() * 300);
}

function connect() {
  const url = wsURL();
  if (!url) return;
  setLiveStatus(attempts ? 'Reconnecting…' : 'Connecting…', attempts ? 'reconnecting' : 'connecting');
  socket = new WebSocket(url);
  socket.addEventListener('open', () => {
    const reconnect = everConnected;
    everConnected = true;
    attempts = 0;
    stopPolling();
    setLiveStatus('Live', 'live');
    if (reconnect) resyncBoard();
  });
  socket.addEventListener('message', (event) => {
    let message;
    try { message = JSON.parse(event.data); } catch { return; }
    if (message.type === 'ready') {
      setLiveStatus('Live', 'live');
      return;
    }
    applyPatch(message, { fromSelf: false });
  });
  socket.addEventListener('close', () => {
    if (!everConnected) setLiveStatus('Reconnecting…', 'reconnecting');
    else setLiveStatus('Reconnecting…', 'reconnecting');
    scheduleReconnect();
  });
  socket.addEventListener('error', () => {
    socket?.close();
  });
}

export function startWS() {
  document.addEventListener('visibilitychange', () => {
    if (document.hidden) {
      hiddenAt = Date.now();
      return;
    }
    if (hiddenAt && Date.now() - hiddenAt > 30000) resyncBoard();
    if (!socket || socket.readyState !== WebSocket.OPEN) resyncBoard();
  });
  window.addEventListener('online', () => resyncBoard());
  connect();
}
