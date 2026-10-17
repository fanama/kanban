# Responsive Trello-Inspired UI — Specification

| Field | Value |
|---|---|
| Status | Ready for implementation |
| Date | 2026-10-17 |
| Repository | `kanban-app` (hexagonal Go kanban) |
| Request | "I wanna improve the design to be responsive and inspire by trello" |
| Scope | Primary HTTP adapter: templates, CSS, browser JS, plus the small application-port change needed by the per-column composer |
| Behavior | Existing board rules, routes, storage and JSON contracts unchanged |
| Delivery | One PR: spec + implementation |

---

## 1. Purpose

The kanban board works but looks like a single dark panel: all cards are added
from one toolbar form, columns are fixed-width boxes in a horizontally scrolling
strip, the sidebar never adapts to small screens, and there is no card detail
view. The request is a **Trello-inspired, responsive redesign**.

This spec records the interview decisions and is the implementation blueprint.
It covers:

1. a new visual system (Trello-like board canvas, lists, cards, top bar);
2. responsive behavior from phone to wide desktop;
3. OS-driven light/dark themes with a custom brand palette;
4. native drag and drop between adjacent, legal columns;
5. a read-only card modal that keeps inline editing;
6. a per-column "Add a card" composer;
7. a separate, cacheable stylesheet served by the HTTP adapter;
8. updated tests and documentation.

---

## 2. Current state (baseline)

- One template file, `internal/adapters/primary/http/templates/index.html`
  (300 lines), with all CSS inline in a `<style>` block.
- Layout: static header, toolbar (single add-card form, import, export),
  `<main id="board" class="shell">` containing a 190 px sidebar and a
  horizontally scrolling `.cards-area`.
- Columns are `flex: 1 0 260px; max-width: 340px`; cards sit in a plain
  vertical flex list. No media queries, no touch affordances, no dark/light
  switch (always dark).
- HTMX 2.0.4 is loaded from unpkg. Every mutation re-renders the whole
  `#board` fragment (`hx-target="#board" hx-swap="outerHTML"`).
- The read-only card fragment (`GET /cards/{id}`) already exists and is used to
  leave edit mode; the edit form fragment is `GET /cards/{id}/edit`.
- `POST /cards` always adds the card to the board's first column:
  `BoardUseCase.AddCard(ctx, boardID, title, priority)`.
- Templates are loaded from disk (`TEMPLATE_GLOB`); there is **no static asset
  route** and no `static/` directory.
- Harness tests assert a few markup details: `id="board"`, column names as
  `>Name</span>`, `action="/columns/{id}/delete"` forms, rename input
  attributes, and the locked-advance `title` text. Those contracts must keep
  working (assertions may be updated only where the redesign necessarily
  changes markup).

---

## 3. Interview decisions (authoritative)

| Area | Decision |
|---|---|
| Scope | Visual + responsive redesign **plus** drag and drop **plus** a read-only card detail, with inline editing retained |
| Theme | Follow the OS (`prefers-color-scheme`), no manual toggle |
| Palette | Trello layout/spacing with a **custom brand palette** (indigo accent), in both themes |
| Mobile columns | Horizontal scrolling with scroll snapping |
| CSS delivery | Separate static CSS file served by the HTTP adapter |
| Drag technology | Native HTML5 drag events; desktop only |
| Drag targets | Only **adjacent** columns: previous always, next only when the existing stage rule allows it |
| Blocked drop | Not a drop target at all (no highlight); server remains the final authority |
| Card detail | Modal (native `<dialog>`), **read-only**, opened from the existing `GET /cards/{id}` fragment; Edit still swaps in the existing inline form |
| Navigation | Collapsible sidebar: persistent on desktop, off-canvas drawer on mobile |
| Card composer | Per-column "+ Add a card" (requires `AddCard` to accept a column) |
| No-JS | Everything that works today still works: forms post classically, composers use `<details>`, sidebar stays visible without JS; drag, modal and drawer are enhancements |
| Tests | Update only the assertions the redesign breaks; add coverage for the new API, modal hooks, drag hooks, and static CSS |

---

## 4. Target experience

### 4.1 Shell

```
┌──────────────────────────────────────────────────────────────┐
│ top bar: [☰] brand        │ board tools (import/export)      │
├───────────────┬──────────────────────────────────────────────┤
│ sidebar       │ board heading: My board · 12 cards           │
│  Boards       │ ┌─────────┐ ┌─────────┐ ┌─────────┐          │
│  · My board   │ │ To Do 3 │ │ Doing 2 │ │ Done 7  │  → swipe │
│  + New board  │ │ [card]  │ │ [card]  │ │ [card]  │          │
│  Delete board │ │ ...     │ │ ...     │ │ ...     │          │
│  Columns      │ │ + Add   │ │ + Add   │ │ + Add   │          │
│  + New column │ └─────────┘ └─────────┘ └─────────┘          │
└───────────────┴──────────────────────────────────────────────┘
```

- The sidebar stays **inside** `#board` so every HTMX mutation keeps the board
  list and column form fresh.
- The top bar lives outside `#board` and holds the app brand, the sidebar
  toggle, and the import/export tools (both address the selected board via the
  cookie, so they need no re-render).
- The board name and card count are rendered inside the fragment.
- Board switching keeps the current full-page `GET /?board=…` behavior.

### 4.2 Columns

- Fixed 280 px width on desktop (`min(85vw, 300px)` on phones), full-height
  lists with an internally scrolling card area and a sticky header.
- Header: name, card count, rename (✎) and delete (✕) actions.
- Footer: `<details>` composer with a hidden `column_id`, title input, priority
  select and submit. Works with JS disabled.
- Horizontal track: `display:flex; overflow-x:auto; scroll-snap-type:x proximity`
  with `scroll-snap-align:start` on columns; keyboard/trackpad scrolling still
  works. No snap on very wide screens where all columns fit.

### 4.3 Cards

- White/raised cards in light mode, elevated slate in dark mode, with the
  priority shown as a colored chip instead of only a left border.
- Title is a button (keyboard focusable) that opens the detail modal.
- Description, objective, blocker and dependent messages stay on the card.
- Existing move controls remain (`◀ Prev`, `Next ▶`, locked state, Edit,
  Delete). No action is removed.
- New data attributes for drag: `data-card-id`, `data-prev-column`,
  `data-next-column`, `data-can-advance`.

### 4.4 Drag and drop

- `draggable="true"` on cards; edit forms are not draggable.
- Drag starts anywhere on the card. A `dragging` class fades the source.
- `dragover` on a column highlights it **only** when the dragged card may move
  there:
  - previous adjacent column: always legal (the domain always allows a
    backward move, and the cascade demotes dependents);
  - next adjacent column: legal only when the card has no blockers and no
    waiting dependents — exactly the condition that enables the existing
    advance button.
- Drop sends `POST /cards/move` with `card_id` + `column_id` through
  `htmx.ajax`, targeting `#board` with `outerHTML`, so all existing rules,
  cascades and error rendering apply unchanged.
- Touch devices have no drag; they use the existing move buttons and the modal.
- Keyboard users have the same move buttons.

### 4.5 Card detail modal

- Native `<dialog id="card-dialog">` in `index.html`, closed by default.
- Clicking a card title fetches `GET /cards/{id}` (the read-only fragment),
  inserts it into the dialog body, calls `htmx.process` on it, and opens the
  dialog with `showModal()`.
- The fragment's Edit button keeps working inside the dialog
  (`hx-target="closest article"`), and so do move/delete.
- Any successful non-GET HTMX request while the dialog is open closes it, so
  board re-renders never leave stale content.
- Close via the ✕ button, `Esc`, or a backdrop click. Focus returns to the
  board naturally.
- No URL change: the modal is an enhancement; the board itself remains the
  canonical view (decision: native dialog over the existing fragment).

### 4.6 Responsive behavior

| Width | Behavior |
|---|---|
| ≥ 1100 px | Sidebar persistent, columns 280 px, board canvas fills the rest |
| 700–1099 px | Sidebar persistent but narrower; columns still horizontally scrollable |
| < 900 px | Sidebar becomes an off-canvas drawer with a backdrop; toggle in the top bar. Without JS the sidebar is displayed above the columns |
| < 700 px | Top bar wraps its tools into a menu; columns are `min(85vw, 300px)` wide with snap; card actions use larger touch targets |
| < 420 px | Single column nearly full width; import/export collapse into the Board tools menu |

- `100dvh` shell so mobile browser chrome does not clip the board.
- `prefers-reduced-motion` disables drawer/snap animations.
- Visible `:focus-visible` rings on every control.
- Dialog is `max-width: 640px; width: calc(100vw - 2rem); max-height: 85dvh`
  with an internally scrolling body.

### 4.7 Themes

- Tokens defined once on `:root` (light), overridden in
  `@media (prefers-color-scheme: dark)`.
- Custom brand palette, indigo accent (`#5b5bd6` light, `#8b8bf5` dark),
  lavender-tinted board canvas, white cards in light mode.
- Semantic tokens: canvas, surface, surface-raised, border, text, muted,
  accent, accent-contrast, danger, warning, success, shadow, focus.
- Priority colors: high rose, medium amber, low emerald; chips plus a subtle
  card edge.
- `color-scheme: light dark` on `:root` so native controls match.
- No external fonts (system stack) and no external icon library (inline
  characters/SVG), keeping the app offline-friendly.

---

## 5. Application / API changes

### 5.1 Per-column composer

`AddCard` gains a target column:

```go
AddCard(ctx context.Context, boardID, columnID, title string, priority domain.Priority)
```

- `columnID == ""` keeps the old behavior: first column (direct callers and
  `POST /cards` without the field).
- Unknown `columnID` → `ErrColumnNotFound` (wrapped, HTTP 404 by the existing
  `statusFor`).
- A new card has no constraints, so any column is legal; no stage check.
- HTTP handler reads `r.FormValue("column_id")`.

### 5.2 Static assets

- New directory `internal/adapters/primary/http/static/` with `style.css`.
- `NewHTTPHandler(useCase, tmplPath, staticDir string)`; `main.go` passes
  `STATIC_DIR` (default `internal/adapters/primary/http/static`).
- Routes: `GET /static/` → `http.StripPrefix("/static/", http.FileServer(...))`,
  plus a `Cache-Control: public, max-age=3600` header on successful responses.
- `index.html` links `<link rel="stylesheet" href="/static/style.css">`.
- Existing `GET /` and HTMX fragment endpoints are unchanged.

### 5.3 Markup contracts kept

- `id="board"` on the swapped root.
- Column names still render inside `<span class="name">` (tests look for
  `>Name</span>`).
- Delete forms keep `action="/columns/{id}/delete"`.
- Rename form keeps `name="name"` and `value="<current>"`.
- Locked advance keeps
  `title="The cards that depend on this one must move first"`.
- Import/export endpoints, HTMX swap target and flash element stay.

---

## 6. Implementation plan

1. **Spec** (this file).
2. **Port + use case**: `AddCard` column parameter and validation.
3. **Handler**: read `column_id`, static dir constructor parameter, static
   route, cache header.
4. **Composition root**: `STATIC_DIR` env var and default.
5. **Template**: new shell, sidebar, per-column composer, card data attributes,
   dialog, progressive-enhancement JS, link to stylesheet.
6. **Stylesheet**: tokens/themes, layout, components, responsive breakpoints,
   reduced motion, focus states.
7. **Tests**:
   - update `NewHTTPHandler` call and `AddCard` call sites;
   - keep existing HTML assertions;
   - add: POST `/cards` with `column_id` lands in that column;
   - add: `GET /static/style.css` returns 200 and `text/css`;
   - add: rendered board carries drag hooks and the dialog.
8. **Docs**: README (UI section, `STATIC_DIR`), TODO item closed/updated.
9. **Verify**: `gofmt`, `go vet ./...`, `go test ./... -count=1`, run the server
   and fetch `/`, `/static/style.css`, and an HTMX board fragment.

---

## 7. Acceptance criteria

- [ ] Light (OS) and dark (OS) themes both render with the custom indigo brand
      palette; no manual toggle needed.
- [ ] At < 900 px the sidebar becomes a drawer with a working toggle; without
      JS the sidebar is still visible.
- [ ] Columns scroll horizontally with snap on phones and keep fixed width on
      desktop; each column scrolls its own cards.
- [ ] Every column has a working "+ Add a card" composer; cards land in that
      column; the old first-column behavior still works when `column_id` is
      absent.
- [ ] Desktop drag highlights only the previous column and a legal next
      column; dropping performs the existing move and cascade; blocked
      columns never accept a drop.
- [ ] Clicking a card title opens a read-only modal from `GET /cards/{id}`;
      Edit swaps in the inline form; a successful save closes the modal;
      `Esc`/backdrop/✕ close it.
- [ ] All existing buttons, forms, keybindings-to-be (Tab/Enter), import,
      export, board switching, column rename/delete/add keep working.
- [ ] With JS disabled: board renders, sidebar visible, composers expand,
      forms post classically, board switching works.
- [ ] `/static/style.css` is served with `text/css` and is the only stylesheet.
- [ ] `gofmt -l .` empty, `go vet ./...` clean, `go test ./... -count=1` green.
- [ ] README documents the new UI, `STATIC_DIR`, and the composer behavior.

---

## 8. Out of scope

- Changing domain rules, cascades, storage adapters, JSON import/export,
  routes for existing operations, or error semantics.
- Drag and drop on touch devices (deliberate: native HTML5 DnD).
- Card labels, due dates, checklists, covers, comments, avatars, search/filter,
  column reordering, board renaming (separate TODO items).
- Deep-linkable card URLs, a manual theme toggle, external fonts/icon packs,
  CSS build tooling or preprocessors.
- Rewriting the HTMX architecture or adding a SPA framework.
