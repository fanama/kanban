const MARKED_OPTIONS = {
  gfm: false,
  breaks: true,
  headerIds: false,
  mangle: false,
  pedantic: false
};

const PURIFY_OPTIONS = {
  ALLOWED_TAGS: [
    'p', 'br', 'strong', 'em', 'b', 'i', 'u', 's',
    'code', 'pre', 'blockquote', 'ul', 'ol', 'li',
    'a', 'h1', 'h2', 'h3'
  ],
  ALLOWED_ATTR: ['href', 'title', 'target', 'rel'],
  ALLOW_DATA_ATTR: false,
  FORBID_TAGS: [
    'img', 'table', 'thead', 'tbody', 'tr', 'th', 'td',
    'script', 'style', 'iframe', 'object', 'embed', 'form',
    'input', 'button', 'svg', 'math'
  ],
  FORBID_ATTR: ['style', 'class', 'id', 'srcset']
};

let hookInstalled = false;

// marked only honours `breaks` when GFM is enabled. The card dialect is
// deliberately not GFM, so mark paragraph soft breaks with the standard
// two-space hard break instead.
function withSoftBreaks(source) {
  const lines = source.replace(/\r\n?/g, '\n').split('\n');
  let fenced = false;
  return lines.map((line, index) => {
    if (line.trimStart().startsWith('```')) {
      fenced = !fenced;
      return line;
    }
    if (fenced || !line.trim()) return line;
    if (/^ {0,3}(#{1,6}\s|[-+*]\s|\d+[.)]\s|---)/.test(line)) return line;
    const next = lines[index + 1];
    if (next === undefined || !next.trim()) return line;
    return line.replace(/\s+$/, '') + '  ';
  }).join('\n');
}

function installLinkHook() {
  if (hookInstalled || !window.DOMPurify) return;
  window.DOMPurify.addHook('afterSanitizeAttributes', (node) => {
    if (node.tagName !== 'A') return;
    const href = node.getAttribute('href') || '';
    node.setAttribute('rel', 'noopener noreferrer nofollow');
    if (/^https?:/i.test(href)) node.setAttribute('target', '_blank');
  });
  hookInstalled = true;
}

export function renderMarkdown(root = document) {
  if (!window.marked || !window.DOMPurify) return;
  installLinkHook();
  const nodes = [];
  if (root.matches && root.matches('[data-markdown]')) nodes.push(root);
  if (root.querySelectorAll) nodes.push(...root.querySelectorAll('[data-markdown]:not([data-md-rendered])'));
  for (const node of nodes) {
    if (node.dataset.mdRendered) continue;
    const source = node.textContent || '';
    if (!source.trim()) {
      node.dataset.mdRendered = 'true';
      continue;
    }
    // Sanitize the source first so a raw HTML block cannot hide the Markdown
    // around it; DOMPurify runs again on the rendered result for links and
    // anything marked itself produced.
    const safeSource = window.DOMPurify.sanitize(withSoftBreaks(source), PURIFY_OPTIONS);
    const rendered = window.marked.parse(safeSource, MARKED_OPTIONS);
    node.innerHTML = window.DOMPurify.sanitize(rendered, PURIFY_OPTIONS);
    node.dataset.mdRendered = 'true';
  }
}
