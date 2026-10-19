# Real-Time, Front-First Board Sync + Markdown Cards — Specification

| Field | Value |
|---|---|
| Status | Ready for implementation |
| Date | 2026-10-19 |
| Repository | `kanban-app` (hexagonal Go kanban) |
| Request | “Lorsque l'app est déployée l'app a des lags et des délais, ce qui n'arrive pas en local. Il faut privilégier les actions front. Voir pour utiliser le pattern observer pour que tout le monde voie en temps réel ce qui se passe sur les boards. Le texte rendu doit être en Markdown (dans les tickets).” |
| Scope | Front-first optimistic UI, observer pattern + WebSocket fan-out, targeted HTML patches, card versioning for edit conflicts, client-side Markdown rendering, performance instrumentation |
| Behavior | Domain rules, existing routes, storage semantics and no-JS fallbacks stay intact; the only data-contract addition is a card `version` |
| Delivery | This spec first; implementation in a follow-up PR |

> This document is the implementation blueprint. No application code has been
> changed in this step.

---

## 1. Purpose

The board is fast on a developer laptop and slow once deployed: every click
goes to the server, the server re-renders the **whole board**, and the browser
waits for that HTML before showing the result. There is no live update between
clients, so two people looking at the same board see different states, and card
descriptions are displayed as raw text.

The request has three outcomes:

1. **Perceived latency disappears in production**: a user sees the result of
   add / move / edit / delete / column / board actions immediately, before the
   network answers, with a safe rollback when the server refuses.
2. **Everyone sees board activity in real time**: an observer/event bus feeds a
   WebSocket hub, so a change made by one person is pushed to every other
   viewer of that board (and to the sidebar for board-level changes).
3. **Card text is rendered as Markdown**: description and objective are shown
   as formatted text in the card and in the card dialog, without allowing
   script injection.

The spec also fixes two concrete sources of production slowness that are
visible in the current code:

- the full-board HTML response on every mutation;
- the repeated full-board scans performed while decorating every card
  (`domain.Unfinished`, `PullPlan`, `AheadOfDependents`) — effectively an
  O(n²) render path on boards with many cards.

The fix is not “make the server faster and keep waiting”. It is
**front-first + targeted patches + push**, with server-side render
optimizations and measurements so the deployed behaviour is verifiable.

---

## 2. Current state (verified in the codebase)

- `internal/adapters/primary/http/handler.go` registers every mutation route;
  every mutation handler ends with `renderBoard`, which returns the full page
  for a classic POST and the whole `<main id="board">` fragment when
  `HX-Request: true`. HTMX swaps `#board` with `hx-swap="outerHTML"`.
- `index.html` contains the entire browser layer inline: sidebar drawer, card
  dialog, native drag and drop, and an `htmx.ajax` POST for drops. HTMX 2.0.4
  is loaded from unpkg. There is no WebSocket, no static application JS, and no
  Markdown.
- `card.html` renders `{{.Description}}` and `{{.Objective}}` as escaped plain
  text. Titles, column names and board names are plain text.
- `view.go` decorates **every card** on every render. `decorateCard` calls
  `domain.Unfinished`, which builds `BoardView.Index()` (a full map) for each
  card, then `domain.PullPlan`, which builds positions and calls
  `domain.Dependents` (another full scan) for each card. A board render is
  therefore much more expensive than a single pass over the cards.
- `renderBoard` also calls `ListBoards` on every mutation, and each use case
  reads the board before writing it, then reads it again for the response.
- SQLite is opened with WAL, a 5-second busy timeout and `MaxOpenConns(1)`:
  one connection serializes reads and writes. `GetAll` loads **all**
  dependencies across every board (`loadDependencies` has no board filter).
- The domain has no card version/updated-at field, so two editors cannot detect
  that they are overwriting each other.
- There is no authentication. The selected board lives in the `kanban_board`
  cookie; direct use-case calls carry the board ID explicitly.
- `internal/architecture` enforces ≤ 400 lines per Go file and ≤ 40 lines per
  function. The served stylesheet is the documented exception.
- The existing `responsive-trello-ui-spec.md` and README describe the current
  responsive UI, per-column composer, dialog, drag-and-drop and static
  stylesheet. Those behaviours must keep working.

---

## 3. Interview decisions (authoritative)

| Area | Decision |
|---|---|
| Deployment | Freebuff service “D1”. The runtime is a **long-running container/VM**; storage persistence and the exact meaning of “D1” were not confirmed. |
| Dominant lag | Noticeable **after every click/action**, not only on first load. |
| Concurrent users | **1–5** people viewing the same board at once. |
| Real-time transport | **WebSocket** |
| Live update style | **Targeted DOM update + brief highlight**, plus a **notification banner/toast** for other people’s actions |
| Optimistic actions | **All**: add card, move/drag card, edit card, delete card, column add/rename/delete, board create/delete/switch |
| Failure handling | **Rollback to the previous DOM state + show the server’s error message** |
| Concurrent edit | **Warn and let the user choose** (reload the server version or overwrite deliberately) |
| Conflict data | Add a persisted **card version / updated-at field**; stale saves are rejected with `409` |
| Markdown fields | **Card description and card objective only** (not title, column names or board names) |
| Markdown dialect | **Basic only**: paragraphs, line breaks, emphasis/strong, headings h1–h3, inline code, fenced code, blockquotes, ordered/unordered lists, links. No GFM tables, task lists, strikethrough, footnotes, autolinks or images. |
| Markdown renderer | **Client-side in the browser** (`marked` + `DOMPurify`), vendored locally. The server sends raw Markdown inside server-rendered HTML fragments; the browser transforms it after initial load and after every swap/patch. |
| Raw HTML in Markdown | **Allow a safe subset**, sanitized by an allowlist. No scripts, styles, iframes, images, event handlers or `javascript:` URLs. |
| Editor UX | **Plain textarea only**; no toolbar and no live preview. The card/modal shows the rendered result after saving. |
| Socket payload | **Server-rendered HTML fragments** (inside a JSON envelope), not full board HTML and not a bare “changed” signal. |
| Own events | **Ignore own echo** in the originating tab; the HTTP response is its confirmation. |
| Reconnect | **Automatic full resync** of the current board; show a subtle connection status while disconnected. |
| Dependencies | Vendor `marked` and `DOMPurify` under `/static/vendor/`; add the maintained Go WebSocket library `github.com/coder/websocket`. |
| Identity | **Prompt once for a display name**, store it locally, and attribute live banners to it. This is a label, not authentication. |
| Event scope | **Current board + the sidebar board list**. Card/column events stay board-scoped; board create/delete events update every sidebar. |
| Patch granularity | **Only the affected pieces**: card fragment for add/edit/delete, column fragments for moves/renames, plus counts and board summary. Structural column changes may replace the columns region; imports trigger a full reload. |

---

## 4. Root causes and strategy

### 4.1 Perceived latency

| Cause | Effect | Fix |
|---|---|---|
| Every action waits for a POST + full board HTML | The user sees nothing until the round trip and server render finish | Optimistic DOM change in the same frame; reconcile with a targeted patch |
| Full `#board` swap on every mutation | Large payload, loss of scroll/focus, expensive browser layout | Server returns only changed card/column fragments |
| `renderBoard` runs on every action | Full board render, `ListBoards`, repeated reads | Patch renderer; single board read; optional timing instrumentation |
| O(n²) card decoration | Server time grows quickly with card count | Precompute index, positions and dependents once per render |
| No push | Other viewers stay stale until they act or reload | Observer bus + WebSocket fan-out |
| Deployment environment | Unknown disk/network latency, cold starts, proxy timeouts | Measure with `Server-Timing`; keep the socket alive; auto-resync |

### 4.2 Strategy

1. **Front-first**: mutate the DOM immediately, snapshot it for rollback, then
   send the request.
2. **Targeted reconciliation**: the server answers with HTML fragments for the
   affected pieces, not the whole board.
3. **Observer + push**: application services publish board events; an
   in-process bus notifies WebSocket clients; each client applies the same
   patch format.
4. **Server render reduction**: compute board facts once, render only affected
   fragments, and avoid full-board scans per card.
5. **Markdown at the edge**: keep raw Markdown in the payload and render it in
   the browser with a sanitizer, so the server and the optimistic UI share one
   renderer.
6. **Measure**: server timings, browser marks and payload sizes are part of the
   acceptance criteria, because the original problem only appears in
   production.

### 4.3 Non-goals of the strategy

- No CRDT/OT engine, no offline editing queue, no multi-instance broker in this
  iteration.
- No rewrite away from HTMX or the server-rendered templates.
- No server-side Markdown converter.

---

## 5. Observer architecture

### 5.1 Roles

The observer pattern is implemented explicitly:

```
HTTP mutation ──► use case ──► repository
                     │
                     │ publish(Event)
                     ▼
              events.Bus  (Subject / publisher)
                     │  board topic + global board-list topic
          ┌──────────┴──────────┐
          ▼                     ▼
    wsClient A             wsClient B      (Observers / subscribers)
    (browser tab)          (browser tab)
```

- **Subject**: `internal/events.Bus`, an in-process pub/sub registry.
- **Observer**: every WebSocket connection registered through a subscription.
- **Concrete observer**: `wsClient` in the HTTP adapter; it owns a buffered
  send channel and a write loop.
- **Event**: an immutable value describing what changed, for whom, and which
  entities are affected.
- **Notification**: the observer receives the event, renders the affected HTML
  fragments from the current board, and writes a JSON patch to the socket.

Loose coupling rules:

- The use case publishes events through a small `Publisher` interface; it does
  not know about WebSockets, HTTP or templates.
- The HTTP adapter subscribes to the bus; it does not mutate domain state to
  notify anyone.
- The domain package remains dependency-free. The event package imports only
  the standard library (and, if needed, small value types from `ports`).

### 5.2 Event package

New package `internal/events`:

```go
type Kind string

const (
    BoardCreated  Kind = "board.created"
    BoardDeleted  Kind = "board.deleted"
    BoardImported Kind = "board.imported"

    ColumnAdded   Kind = "column.added"
    ColumnRenamed Kind = "column.renamed"
    ColumnDeleted Kind = "column.deleted"

    CardAdded     Kind = "card.added"
    CardUpdated   Kind = "card.updated"
    CardMoved     Kind = "card.moved"
    CardDeleted   Kind = "card.deleted"
)

type Event struct {
    Seq        uint64    // per-board monotonic sequence, 0 for board-list events
    Kind       Kind
    BoardID    string
    Actor      string    // self-declared display name, sanitized
    ClientID   string    // originating tab, for own-echo suppression
    CardIDs    []string  // affected cards (including cascade victims)
    ColumnIDs  []string  // affected columns
    Reload     bool      // too many pieces changed: fetch the whole board
    At         time.Time
}
```

`Bus` API:

```go
func NewBus() *Bus
func (b *Bus) SubscribeBoard(boardID string) (<-chan Event, func())
func (b *Bus) SubscribeBoards() (<-chan Event, func())
func (b *Bus) Publish(event Event)
```

The use case depends on a narrow interface, not on the concrete bus:

```go
// internal/events
type Publisher interface {
    Publish(Event)
}
```

`NewServices` takes the publisher explicitly:

```go
func NewServices(repo ports.BoardRepository, publisher events.Publisher) Services
```

Tests can pass a fake publisher; a nil publisher is treated as a no-op so
read-only use-case tests do not need the bus.

Semantics:

- Topics are isolated: a subscription to board A never receives board A's
  card/column events from board B.
- Board create/delete events are published on the global board-list topic only.
- `Publish` never blocks a mutation. Each subscriber has a buffered channel
  (capacity 32). If it is full, the bus closes/drops that subscriber; the
  browser reconnects and resyncs.
- `Publish` is called **after** the repository operation commits. A publish
  failure must never turn a successful mutation into an HTTP error; it is
  logged.
- `Seq` is allocated per board by the bus under a mutex. Clients use it to
  discard stale/duplicate patches after reconnects.
- Subscriptions are cheap and cancellable; `cancel` is idempotent.
- No persistence: an event while nobody is listening is lost, which is fine
  because every client resyncs on (re)connect.

### 5.3 Request identity in the context

Add helpers in `internal/events`:

```go
func WithActor(ctx context.Context, actor string) context.Context
func WithClientID(ctx context.Context, clientID string) context.Context
func Actor(ctx context.Context) string    // fallback "Someone"
func ClientID(ctx context.Context) string
```

The HTTP adapter fills these values from, in order:

1. `X-Kanban-Actor` / `X-Kanban-Client` headers (JS `fetch`);
2. `actor` / `client_id` form fields (classic and HTMX fallback);
3. actor fallback `"Someone"`, client ID empty.

Sanitization: trim, strip control characters, collapse whitespace, cap at 40
runes; escape on output. A display name is **not** an authorization identity
and must not be treated as one.

### 5.4 Board change result

To render a targeted patch and publish an event without extra full-board
reads, mutating card/column service methods return a change value in addition
to the board:

```go
// internal/ports
type BoardChange struct {
    Board          domain.BoardView
    CardIDs        []string // cards added, edited, moved or cascaded
    ColumnIDs      []string // columns whose head/cards changed
    RemovedCards   []string
    RemovedColumns []string
    Reload         bool     // import or too many changes
}
```

The concrete signature becomes, for example:

```go
UpdateCard(ctx, boardID string, card domain.Card) (ports.BoardChange, error)
```

All mutating card/column methods (`AddCard`, `UpdateCard`, `MoveCard`,
`DeleteCard`, `AddColumn`, `RenameColumn`, `DeleteColumn`) adopt it.
`ImportBoard` also returns `BoardChange` with `Reload: true`. `CreateBoard` and
`DeleteBoard` keep their current signatures and publish board-list events.

The affected IDs are computed by diffing the before/after `BoardView` inside
the use case:

- added card → new card ID + target column;
- edited card → card ID + any cascade victims;
- moved card → every card whose `ColumnID` changed + source/destination
  columns;
- deleted card → removed card ID + dependents whose constraint list changed;
- column rename → column ID;
- column add/delete → column IDs + cards that fell back;
- import → `Reload: true`.

This diff is O(n) and replaces ad-hoc ID collection in handlers. A helper
`diffBoard(before, after BoardChange) BoardChange` lives in the use case layer
and is unit-tested.

### 5.5 WebSocket hub

New HTTP-adapter file `ws.go` / `hub.go`:

- Endpoint: `GET /ws?board=<boardID>&client=<clientID>`.
- Uses `github.com/coder/websocket`.
- The server checks the `Origin` header against the request host (same-origin
  only); rejects cross-origin upgrades.
- The connection subscribes to:
  - `board:<current board>` — card/column changes;
  - the global board-list topic — board create/delete.
- The hub keeps `map[*wsClient]struct{}` plus `map[boardID]map[*wsClient]struct{}`
  under a mutex, or an equivalent registry.
- One writer goroutine per client; no concurrent writes to a connection.
- Buffered send channel (32). If full, close the client; the browser reconnects
  and resyncs.
- Server ping every 30 seconds; read deadline 60 seconds; browser WebSocket
  automatically answers pings. A closed read loop unregisters the client.
- The hub skips delivering an event to the connection whose `client` matches
  `event.ClientID` (own-echo suppression at the server), and the browser also
  checks `client_id` defensively.
- Incoming client messages are ignored (the protocol is server → browser);
  they are read only to detect close and to apply a small read limit.
- On server shutdown or board deletion, the hub closes the affected clients;
  clients reconnect/resync.

### 5.6 Rendering and sending patches

The HTTP adapter is the only place that knows templates. On an event it:

1. fetches the board once (`BoardService.GetBoard`) — for board deletion,
   fetches the board list instead;
2. decorates only the affected cards/columns;
3. executes the existing `card`, `column-head`/`card` templates into buffers;
4. builds a `patchEnvelope` (section 7.2);
5. writes it as one WebSocket text message.

For HTTP mutation responses, the handler already has `BoardChange.Board` and
uses it directly; it does not fetch the board again.

Event bursts are not coalesced on the server in this iteration (1–5 users).
The browser coalesces DOM application with `requestAnimationFrame`.

### 5.7 Single-instance limitation (explicit)

The bus is in-process. It works when one Go process serves every user. It does
**not** work with multiple app instances behind a load balancer: an event
published by instance A is invisible to clients connected to instance B.

If the deployment is ever scaled horizontally, the `Bus`/publisher boundary is
the seam for a shared adapter (Redis pub/sub, NATS, Postgres `LISTEN/NOTIFY`,
or DB change polling). That work is out of scope for this iteration. The spec
assumes a single long-running container/VM, as confirmed in the interview, and
the deployment checklist in section 12 must verify it.

---

## 6. Front-end behaviour

### 6.1 Modules and load order

No build step. Serve plain ES modules from `/static/`:

```
static/
  app.js          boot: identity, status, markdown, patches, actions, ws
  identity.js     display name prompt/storage, client ID
  markdown.js     marked + DOMPurify pipeline
  patches.js      applyPatch(envelope), DOM reconciliation, highlights
  actions.js      optimistic submit/drag/rollback
  ws.js           WebSocket lifecycle, reconnect, resync, polling fallback
  ui.js           toasts, connection status, conflict dialog, name dialog
  vendor/
    marked.min.js
    purify.min.js
```

- Load `marked` and `DOMPurify` as classic or ES-module scripts from the local
  vendor directory; expose them to `markdown.js` without a global namespace
  collision.
- Keep HTMX for GET fragments (edit form, rename form, card dialog) and as the
  no-JS/JS-error fallback.
- The inline script in `index.html` is split into these modules; no behavioural
  feature is lost.
- Event delegation on `document` means patches do not need to rebind listeners.

### 6.2 Optimistic action pipeline

Every optimistic action follows the same sequence:

1. **Capture** the affected DOM node(s) and their position (a `DocumentFragment`
   clone or references to `parentNode`/`nextSibling`).
2. **Apply locally** — mutate the board DOM immediately.
3. **Render Markdown** on any newly inserted text.
4. **Send** the request (`fetch` with `X-Kanban-Patch: true`).
5. **Reconcile** with the authoritative patch returned by the server.
6. **On failure** — restore the snapshot, remove temporary nodes, restore form
   values if applicable, and show the server message.
7. **On success** — clear the pending marker; the live banner is for *other*
   users only.

Actions and their local effects:

| Action | Optimistic DOM change | Request | Reconciliation | Rollback |
|---|---|---|---|---|
| Add card | Append a temporary card to the target column; render its Markdown; focus it | `POST /cards` | Replace temp card with server `card` fragment; update count/summary | Remove temp card; restore composer text |
| Move / drag | Move the card node to the destination column; update counts | `POST /cards/move` | Replace affected `column-cards` and counts; card fragment if needed | Put the node back at its original parent/index |
| Edit card | Close the edit form; replace the card with the new title/description/objective/priority; render Markdown | `POST /cards/{id}` | Replace with server `card` fragment | Restore the old card and, if possible, the form values |
| Delete card | Remove the card node; update count | `POST /cards/{id}/delete` | Confirm removal; update count/summary | Reinsert the node at its original position |
| Column add | Insert a temporary column at the end | `POST /columns` | Replace with server column fragment / columns region | Remove temp column |
| Column rename | Update the column head name immediately | `POST /columns/{id}/rename` | Replace `column-head` | Restore old name |
| Column delete | Remove the column; move its cards visually to the previous column | `POST /columns/{id}/delete` | Apply server columns/cards patch | Restore column and cards |
| Board create | Add a temporary sidebar entry with a pending style | `POST /boards` | Navigate to the created board | Remove temp entry |
| Board delete | Remove the sidebar entry; mark the current board as leaving | `POST /boards/{id}/delete` | Navigate to the fallback board returned by the server | Restore entry |
| Board switch | Highlight the target tab immediately; show a skeleton in the board area | Full navigation (`GET /?board=…`) | Normal page load | Not applicable (navigation) |

Notes:

- The temporary card uses a client ID such as `tmp_<uuid>` and has
  `data-pending="true"`. It is not draggable until confirmed.
- Add-card submissions keep the title in the composer if the server rejects the
  action, so no typing is lost.
- Edit forms submit the hidden `version` field (section 7.1). The optimistic
  card must not change the version until the server confirms.
- Drag-and-drop remains desktop-only native HTML5 DnD; touch users keep the
  move buttons.

### 6.3 Live patch application

`applyPatch(envelope)` in `patches.js`:

- Ignores an envelope whose `client_id` equals this tab's client ID.
- Ignores an envelope whose `seq` is not newer than the last applied sequence
  for that board (except after a resync, when the sequence is reset).
- Applies fragments in order:
  - `card`: replace `[data-card-id="<id>"]`; if absent, append to
    `[data-column-id="<column_id>"] .cards` (a new card is last in the server
    order).
  - `column-head`: replace the head inside the matching column.
  - `column-cards`: replace the inner HTML of the `.cards` container inside
    the matching column.
  - `columns`: replace the whole `.columns` container (structural changes).
  - `board-summary`: replace the summary line.
  - `sidebar-boards`: replace the boards navigation.
  - `remove`: remove the matching card/column node.
  - `reload: true`: fetch the full `#board` fragment and replace it.
- After applying, run `window.htmx.process(root)` for any HTMX attributes and
  `renderMarkdown(root)` for raw Markdown.
- Add `.is-live-updated` to replaced/inserted nodes for a 1.5-second highlight
  animation; respect `prefers-reduced-motion` (no animation, just a border).
- Preserve scroll positions and the open card dialog. If the dialog's card was
  changed, refresh its body; if it was deleted, close it.
- Do not replace an open edit form's fields from a live patch. Mark the card as
  conflicted instead (section 6.6).
- `reload`/resync replaces the board; if a dirty edit form is open, defer the
  replacement and show “Reconnect to refresh” until the form closes.

### 6.4 Notification banner

- A fixed toast region (`#toast-region`, `aria-live="polite"`, `role="status"`)
  in the topbar/page shell. Maximum 3 visible toasts; auto-dismiss after
  5 seconds; pause on hover/focus; close button.
- Live events from other users produce one toast per action, not per cascade
  victim:
  - “Alice added “Design it” to To Do”
  - “Bob moved “Design it” to Doing”
  - “Alice updated “Design it””
  - “Bob deleted “Design it””
  - “Alice renamed list “To Do” to “Backlog””
  - “Alice created board “Sprint 12””
  - “Alice loaded a board”
- The toast is built from the structured `notice` in the patch envelope, not
  from injected server HTML. Text is inserted with `textContent` (no XSS).
- Clicking a toast scrolls to the card and flashes it if it still exists.
- Own actions do not produce a toast (own-echo suppression). A subtle
  “Saved” state on the composer/status is enough.
- Errors keep using the existing `#flash` element plus an error toast; the
  `#flash` remains the canonical server-error surface for tests.

### 6.5 Display name and own-echo suppression

- On first visit, if `localStorage["kanban.actor"]` is missing, show a small
  `<dialog>`: “What should teammates call you?” with a name input, **Save** and
  **Skip** (skip means “Someone”).
- Store the name in `localStorage["kanban.actor"]`; show it in a topbar chip
  with an edit affordance.
- Generate a per-tab `client_id` with `crypto.randomUUID()` in
  `sessionStorage["kanban.client"]` (fallback: timestamp + random).
- JS requests send `X-Kanban-Actor` and `X-Kanban-Client`; hidden `actor` and
  `client_id` fields cover classic/HTMX posts.
- The server sanitizes and echoes `client_id` in the event/patch; the hub skips
  the origin connection, and the browser ignores any matching echo defensively.
- A display name is a courtesy label, not authentication; anyone can choose any
  name. Document this in the README.

### 6.6 Edit conflicts

Add a card `version` (section 7.1). The edit form includes
`<input type="hidden" name="version" value="{{.Version}}">`.

- If a live event touches a card with an open edit form or dialog:
  - **Deleted**: close the form/dialog, remove the card, toast “Alice deleted
    this card”.
  - **Updated or moved**: do not overwrite the form; set a `conflict` state and
    show a dialog: “This card changed while you were editing. Reload the latest
    version or keep your text?” Buttons: **Reload** and **Keep editing**.
- If the user saves despite the conflict, the server returns `409` with
  `ErrCardConflict`.
- On `409`, fetch the current edit form (`GET /cards/{id}/edit`) and show the
  same dialog with **Overwrite** and **Discard**:
  - **Overwrite**: copy the fresh `version` into the local form and resubmit the
    local values. This is the explicit “last write wins” choice.
  - **Discard**: replace the form with the freshly fetched version.
- If the card was deleted server-side, the fetch returns `404`; close the form
  and toast.
- Never auto-merge fields.

### 6.7 Reconnect, resync and fallback

`ws.js` owns a state machine: `connecting`, `live`, `reconnecting`, `polling`,
`offline`.

- Connect to `/ws?board=<current>&client=<clientID>`.
- On `open`: set **Live**, reset backoff, and — only on a **reconnect** — fetch
  the current board fragment and replace `#board`.
- On `close`/`error`: set **Reconnecting…**, schedule an exponential backoff
  with jitter (0.5 s, 1 s, 2 s, 4 s, 8 s, 15 s max), and retry.
- On `visibilitychange` to visible after more than 30 seconds hidden, resync
  (the tab may have missed events while throttled).
- On `online`, resync.
- If 5 consecutive connection attempts fail, switch to **Polling** and fetch
  the board fragment every 5 seconds while the tab is visible; keep trying the
  socket at the maximum backoff. Return to **Live** when it opens.
- If a dirty edit form is open during a resync, do not replace it; set a
  `needsResync` flag, show a banner, and resync when the form is saved or
  cancelled.
- The connection status indicator (`#live-status`) is a small dot + text in the
  topbar, with `aria-live="polite"`, and is compact on mobile.
- A resync fetches `GET /?board=<id>` with `HX-Request: true`, so the server
  returns the existing `#board` fragment; no new endpoint is required.

### 6.8 Board-level actions

- **Create board**: add a pending sidebar entry; POST; on success navigate to
  the new board; on failure remove the entry and restore the form.
- **Delete current board**: keep the existing confirmation; optimistically
  remove the sidebar entry; POST; on success follow the server’s response
  (another board, or the empty state); on failure restore the entry.
- **Switch board**: highlight the tab and show a skeleton immediately; then
  perform the normal `GET /?board=…` navigation. This is perceived-speed work,
  not a server mutation.
- **Board created/deleted by someone else**: the sidebar patch updates the list.
  If the current board was deleted, show a toast and navigate to `/` (the
  server’s cookie/fallback logic chooses a surviving board).
- Board list changes are published on the global topic; card/column changes
  never are.

### 6.9 No-JS / HTMX fallback

- Classic form posts still return the whole page; HTMX requests still receive
  the `#board` fragment.
- Optimistic JS intercepts submit in the capture phase and prevents the HTMX
  request; if the app JS fails to load, HTMX continues to work.
- GET fragments (edit form, rename form, card dialog) remain HTMX-driven.
- Without JavaScript:
  - the board renders fully server-side;
  - forms work classically;
  - Markdown appears as raw text in a `white-space: pre-wrap` container (readable
    but unformatted);
  - the sidebar remains visible above the columns (existing behaviour);
  - drag, dialog, live updates and the display-name prompt are unavailable.
- A `<noscript>` note explains that live updates and formatted card text need
  JavaScript. This is a documented, accepted degradation.

---

## 7. Markdown rendering

### 7.1 Fields and dialect

Only `Card.Description` and `Card.Objective` are rendered as Markdown. Title,
column names and board names stay plain text and are HTML-escaped as today.

Supported syntax (basic, deliberately small):

- paragraphs and hard line breaks (`breaks: true`);
- `**bold**`, `*italic*`, `__bold__`, `_italic_`;
- inline code `` `code` `` and fenced code blocks;
- headings `#`, `##`, `###` (h1–h3 only; deeper headings are flattened by the
  renderer or sanitizer);
- blockquotes;
- ordered and unordered lists;
- inline links.

Not supported:

- tables, task lists, strikethrough, footnotes, autolinks, definition lists;
- images (the `<img>` tag is stripped; no remote loading/tracking);
- raw `<script>`, `<style>`, `<iframe>`, `<object>`, `<embed>`, forms, inputs;
- inline styles, classes, event-handler attributes, `javascript:` URLs.

### 7.2 Rendering pipeline

Templates emit raw, escaped Markdown into a container:

```html
<div class="md" data-markdown>{{.Description}}</div>
```

`markdown.js`:

1. finds `[data-markdown]:not([data-md-rendered])`;
2. reads `element.textContent` as the source;
3. parses with `marked` using
   `{ gfm: false, breaks: true, headerIds: false, mangle: false }`;
4. sanitizes with `DOMPurify` using an allowlist;
5. sets `element.innerHTML` to the sanitized HTML and
   `data-md-rendered="true"`;
6. adds `target="_blank" rel="noopener noreferrer nofollow"` to external
   `http(s)` links through a DOMPurify hook.

DOMPurify allowlist:

```js
ALLOWED_TAGS: [
  'p', 'br', 'strong', 'em', 'b', 'i', 'u', 's',
  'code', 'pre', 'blockquote', 'ul', 'ol', 'li',
  'a', 'h1', 'h2', 'h3'
],
ALLOWED_ATTR: ['href', 'title', 'target', 'rel'],
ALLOW_DATA_ATTR: false,
FORBID_TAGS: ['img', 'table', 'thead', 'tbody', 'tr', 'th', 'td',
              'script', 'style', 'iframe', 'object', 'embed', 'form',
              'input', 'button', 'svg', 'math'],
FORBID_ATTR: ['style', 'class', 'id', 'srcset']
```

The renderer runs:

- after `DOMContentLoaded`;
- after every HTMX `htmx:afterSwap`;
- after every live patch;
- after an optimistic card insertion.

The `data-md-rendered` flag prevents double rendering and repeated sanitizer
work. Replaced nodes from a patch start unrendered and are processed once.

### 7.3 Optimistic cards

When adding or editing optimistically, `actions.js` inserts the raw Markdown
into a new `[data-markdown]` container and calls `renderMarkdown` on it before
the server answers. When the authoritative card fragment arrives, the temp
node is replaced and the fragment's Markdown is rendered again. The
replacement is intentional: the server fragment is the source of truth for
the card shell, version and metadata.

### 7.4 No-JS and accessibility

- Without JavaScript, the raw Markdown is visible as text. Use
  `white-space: pre-wrap` on `[data-markdown]` so newlines remain readable;
  switch to normal wrapping once `data-md-rendered` is set.
- Links are keyboard-focusable after rendering; the card/dialog keeps its
  existing focus management.
- Markdown output is not announced as a live region; only banner text is.

### 7.5 Security

- Templates HTML-escape all user text; the server never injects raw Markdown as
  HTML.
- DOMPurify removes scripts, event handlers, unsafe URLs and disallowed tags.
- Images are forbidden so a card cannot phone home or track a viewer.
- The sanitizer runs on every render path, including patches and optimistic
  nodes.
- Add XSS regression tests for `<script>`, `<img onerror=…>`,
  `[x](javascript:alert(1))`, `<iframe>`, `<style>` and raw HTML events.

---

## 8. Server and API changes

### 8.1 Card version and conflict detection

Add to `domain.Card`:

```go
Version int `json:"version"`
```

- New cards start at `1`.
- Every card mutation increments the version: update and move
  (`UpdateColumn`), including cascade moves. Delete removes the card.
- `UpdateCard` reads the submitted version. If `edit.Version > 0` and it differs
  from the stored version, return `domain.ErrCardConflict`.
- `ErrCardConflict` maps to HTTP `409 Conflict` in `statusFor`.
- The edit form carries the version in a hidden field. The “Overwrite” conflict
  action refetches the current card, copies its version into the form and
  resubmits; no separate force flag is required.
- `version == 0` means “legacy caller, no check” for direct use-case callers
  and old imports. HTTP forms always send a positive version.

Storage:

- SQLite: add `version INTEGER NOT NULL DEFAULT 1` through the existing
  migration mechanism (`addedColumns`). `Update` executes
  `... WHERE id = ? AND board_id = ? AND version = ?`, sets
  `version = version + 1`, and returns `ErrCardConflict` when no row is
  affected. `UpdateColumn` also increments the version.
- Memory: store and check the version in `Update`/`UpdateColumn`.
- Google Sheets: add a `Version` column (H) to the header and rows, extending
  the data range to A2:H. Existing sheets get the header written on first use;
  blank version cells read as `1`. This keeps the repository contract honest.
  If this is too invasive for the optional adapter, the alternative is a
  documented “no conflict detection on Sheets” limitation, but the preferred
  implementation is the version column.
- JSON export/import: `version` is part of the card JSON. Older files without
  it default to `1`; import renumbers/replaces cards and starts imported cards
  at `1` (or preserves a positive version, if provided). The snapshot format
  is otherwise unchanged.

### 8.2 Patch envelope

A new JSON envelope is returned by patch-mode HTTP responses and sent over the
WebSocket:

```json
{
  "type": "patch",
  "board_id": "bd_123",
  "seq": 42,
  "event": "card.moved",
  "actor": "Alice",
  "client_id": "tab-abc",
  "fragments": [
    {
      "target": "column-cards",
      "id": "doing",
      "html": "<article class=\"card ...\" data-card-id=\"card_1\" ...>...</article>"
    },
    {
      "target": "column-head",
      "id": "doing",
      "html": "<h2 class=\"column-head\">...</h2>"
    },
    {
      "target": "board-summary",
      "html": "<p class=\"board-summary\">12 cards · 3 lists</p>"
    }
  ],
  "remove": [],
  "reload": false,
  "notice": {
    "kind": "card.moved",
    "actor": "Alice",
    "card_id": "card_1",
    "card_title": "Design it",
    "from": "To Do",
    "to": "Doing"
  }
}
```

- `fragments[].target`: `card`, `column-head`, `column-cards`, `columns`,
  `board-summary`, `sidebar-boards`.
- `html` is the full replacement outer HTML for the target (for
  `column-cards`, the inner HTML of `.cards`).
- `remove`: `[{ "target": "card", "id": "card_2" }]` entries.
- `reload`: the client fetches the whole board fragment.
- `notice`: structured data for the toast; no HTML.
- `seq` is allocated per board; clients discard stale/duplicate patches.
- `client_id` is the originating tab; the hub skips that connection and the
  browser ignores a matching envelope.
- On connect, the server sends:

```json
{"type":"ready","board_id":"bd_123","seq":42}
```

The initial page render is already complete; `ready` only marks the socket
live.

### 8.3 HTTP response modes

Mutation handlers choose their response by request headers:

| Request | Response |
|---|---|
| `X-Kanban-Patch: true` (app JS `fetch`) | `application/json` patch envelope; status 200 on success, 4xx/5xx plain-text error |
| `HX-Request: true` (HTMX fallback) | existing `#board` fragment, as today |
| classic form post (no JS) | full page, as today |

The app JS intercepts mutating form submits in the capture phase and prevents
the HTMX/classic submission. It sends `fetch` with credentials, the patch
header, and the identity headers.

`POST /cards` continues to accept `column_id`, `title`, `priority`, plus
`actor`/`client_id` and (for edits) `version`. No existing route is removed.

### 8.4 WebSocket endpoint

- `GET /ws?board=<boardID>&client=<clientID>`.
- Upgrade only for `GET`; reject other methods.
- Same-origin check; reject cross-origin.
- Board ID is validated against the board list; an unknown board closes with a
  normal close code and the client resyncs/navigates.
- One board subscription per connection. Board switching is a full navigation
  and therefore a new connection; no subscribe/unsubscribe protocol is needed.
- Heartbeat: server ping every 30 s, read deadline 60 s.
- Send buffer 32; slow clients are closed.
- Do not gzip or wrap the upgraded connection in the compression middleware.

### 8.5 Static assets and dependencies

- Add `github.com/coder/websocket` to `go.mod`.
- Vendor browser assets (do not use a CDN for the new libraries):
  - `static/vendor/marked.min.js` (latest stable at implementation time);
  - `static/vendor/purify.min.js` (DOMPurify 3.x).
- Keep the existing `GET /static/` route and `STATIC_DIR`. Add:
  - `Cache-Control: public, max-age=31536000, immutable` for
    `/static/vendor/*` (versioned filenames or a version query string);
  - a shorter cache for `app.js`, `ws.js`, `markdown.js`, etc. (e.g.
    `max-age=300`) so a deploy is picked up quickly.
- HTMX remains the existing unpkg dependency in this iteration. If the
  deployment cannot reach unpkg, vendoring HTMX is a follow-up (and would be
  consistent with the offline-friendly goal).
- Update `NewHTTPHandler` to receive the bus (or a subscriber factory) in
  addition to the services, templates and static dir. The composition root
  creates one bus and passes it to both `NewServices` and the handler.

### 8.6 Identity, compression and timing middleware

- Identity middleware reads the headers/form fields and stores the actor and
  client ID in the request context.
- Gzip middleware compresses `text/html`, `application/json`, `text/css`,
  `application/javascript` when the client accepts gzip; skip WebSocket
  upgrades and already-compressed files. This reduces deployed payload cost.
- Timing middleware:
  - adds `Server-Timing: app;dur=<ms>` to responses;
  - logs requests slower than 250 ms with method, path, board ID, status and
    response bytes;
  - optionally adds a `Server-Timing: db;dur=<ms>` once repository timing hooks
    exist. DB timing is a nice-to-have; the app total is required.
- No authentication is added. Display names are self-declared.

---

## 9. Server-side render optimization

### 9.1 Precompute board facts once per render

Replace the per-card full-board scans in `view.go` with a single pass:

- Build once:
  - `index map[string]domain.Card`;
  - `positions map[string]int`;
  - `dependents map[string][]domain.Card` (reverse edges);
  - `columnsByID` and ordered columns.
- Add a small `boardFacts` type in the HTTP adapter (or in `domain` if the
  helpers belong there) with methods used by `decorateCard`:
  - `Unfinished(card, targetPosition)`;
  - `AheadOfDependents(card, targetPosition)`;
  - `CanPull(card, targetPosition)` (the `PullPlan` check).
- `decorateCard` must not call `b.Index()`, `b.All()` or `b.Position()` in a
  loop. Use the precomputed maps.
- Keep the existing `domain.Unfinished`, `Dependents`, `AheadOfDependents` and
  `PullPlan` functions for use-case correctness; optimize the HTTP render path
  and, if benchmarks justify it, add shared helpers to the domain.
- Add a benchmark (`go test -bench`) with 10 columns / 200 cards to prove the
  render path is linear and to record the before/after improvement.

### 9.2 Other server-side improvements

- Scope `loadDependencies` to the board:

  ```sql
  SELECT d.card_id, d.depends_on_id
  FROM card_dependencies d
  JOIN cards c ON c.id = d.card_id
  WHERE c.board_id = ?
  ORDER BY d.card_id, d.position
  ```

- Keep SQLite WAL and the single-writer connection. Do not change durability
  settings without a benchmark and an explicit decision.
- Render only the affected fragments for patch responses; never render the
  whole board for a one-card action.
- For imports, column structural changes, or a patch with more than ~50
  fragments, set `reload: true` and let the client fetch the board once.
- Keep `ListBoards` out of the hot patch path where possible: patch rendering
  needs it only for `sidebar-boards`; card/column patches do not.
- Reuse the existing static cache header; add immutable vendor caching.

---

## 10. Performance targets and measurement

These are goals for the deployed environment, not claims to make without
measurement:

| Metric | Target |
|---|---|
| Optimistic visual feedback after click/drop | ≤ 50 ms (next frame) |
| Server mutation p95, local SQLite | ≤ 150 ms |
| Server mutation p95, deployed (same region) | ≤ 400 ms |
| WebSocket delivery p95, same region | ≤ 250 ms |
| Patch payload for a one-card action | ≤ 20 KB and ≤ 25% of the full board HTML on a 50-card board |
| Full board render, 200 cards / 10 columns | ≤ 500 ms server time |

Measurement:

- **Server**: `Server-Timing: app;dur=…` on every response; slow-request logs.
  Compare full-board response bytes vs patch bytes in the logs.
- **Browser**: `performance.mark`/`performance.measure` around the optimistic
  mutation and the first paint after it; expose the last values on
  `window.kanbanMetrics` for manual inspection. Do not send analytics
  anywhere.
- **Load**: a benchmark board with 10 columns / 200 cards; run the Go
  benchmark and a manual two-browser test.
- **Production check**: after deploy, perform one add, one move and one edit
  with DevTools open; record action→paint, request duration, response size and
  socket delivery. Compare with the pre-change baseline.

Rollout/measurement checklist:

1. Baseline the current deployed action latency and payload size.
2. Deploy the render-path optimization alone if possible; measure.
3. Deploy optimistic UI + patches; measure.
4. Deploy WebSocket fan-out; measure two-browser latency.
5. Keep the full-board/HTMX path available; if a regression appears, disable
   the socket client (live updates) without breaking mutations.

---

## 11. Implementation plan

1. **Spec** (this file).
2. **Events core**: `internal/events` with `Event`, `Bus`, context identity
   helpers and tests (board isolation, unsubscribe, slow subscriber, sequence).
3. **Render facts**: optimize `toView`/`decorateCard` with one-pass board
   facts; add the benchmark; scope dependency loading by board.
4. **Versioning**: `domain.Card.Version`, `ErrCardConflict`, SQLite migration
   and conditional updates, memory and Sheets support, JSON snapshot handling.
5. **Use-case change tracking**: `ports.BoardChange`, diff helper, publisher
   injection, event publishing after commits, actor/client from context.
6. **HTTP patch layer**: patch envelope builder, `X-Kanban-Patch` responses,
   identity middleware, 409 mapping, gzip/timing middleware.
7. **WebSocket layer**: `coder/websocket`, hub, `/ws`, origin check, heartbeat,
   subscription scoping, own-echo suppression, tests with a real WS client.
8. **Front-end**: split the inline script into modules; identity prompt;
   optimistic actions and rollback; patch applier; toasts; connection status;
   reconnect/resync; conflict dialog; Markdown pipeline.
9. **Templates/CSS**: `data-markdown` containers, card/column data attributes
   (`data-column-id`, `data-version`), hidden version/actor/client fields, toast
   region, live status, conflict/name dialogs, highlight animation, vendor
   script tags.
10. **Tests and docs**: update harness tests for the new signatures/responses,
    add event/WS/version/markdown-markup tests, README sections (real-time,
    Markdown, display name, deployment/WebSocket requirements, single-instance
    limitation), TODO update.
11. **Verify**: `gofmt -l .` empty, `go vet ./...` clean,
    `go test ./... -count=1`, architecture size test, a two-browser manual test,
    and the measurement checklist.

Suggested file map:

```
internal/events/event.go
internal/events/bus.go
internal/events/bus_test.go

internal/adapters/primary/http/ws.go
internal/adapters/primary/http/hub.go
internal/adapters/primary/http/patch.go
internal/adapters/primary/http/identity.go
internal/adapters/primary/http/timing.go
internal/adapters/primary/http/patch_test.go
internal/adapters/primary/http/ws_test.go

internal/adapters/primary/http/static/app.js
internal/adapters/primary/http/static/identity.js
internal/adapters/primary/http/static/markdown.js
internal/adapters/primary/http/static/patches.js
internal/adapters/primary/http/static/actions.js
internal/adapters/primary/http/static/ws.js
internal/adapters/primary/http/static/ui.js
internal/adapters/primary/http/static/vendor/marked.min.js
internal/adapters/primary/http/static/vendor/purify.min.js
```

Modified: `domain/card.go`, `domain/errors.go`, `ports/input.go`,
`usecase/*.go`, `sqlite/schema.go`, `sqlite/cards.go`, `memory`,
`gsheets`, `http/handler.go`, `http/view.go`, `http/*_handlers.go`,
`templates/*.html`, `static/style.css`, `cmd/server/main.go`, harness tests,
README and TODO.

---

## 12. Deployment checklist

- Confirm the deployed service is a **single long-running process** with a
  persistent SQLite file on a durable volume. If it is serverless/request-scoped,
  or if “D1” means Cloudflare D1 rather than a Freebuff hosting tier, stop and
  revise this design before implementing WebSockets.
- Confirm the reverse proxy forwards `Upgrade` and `Connection` headers for
  `/ws`, and that its idle timeout is longer than the 30-second heartbeat
  (60+ seconds recommended).
- Confirm the proxy does not buffer or gzip the WebSocket stream.
- Confirm `PORT` and `STATIC_DIR` are set correctly; the new vendor files must
  be present in the deployed image. `go:embed` is not used today, so the static
  directory must be copied with the binary.
- If the app runs more than one instance, the in-process bus is insufficient;
  use a single instance or add a shared broker in a follow-up.
- If the app sleeps after inactivity, expect a reconnect + resync after wake;
  the status indicator should make this visible.
- Measure once with a real deployed browser, not only locally; the original
  problem is deployment-specific.

---

## 13. Tests

### 13.1 Go tests

- `internal/events`: publish/subscribe, board isolation, global board-list
  topic, unsubscribe, slow-subscriber drop, per-board sequence.
- Use cases: diff produces the right card/column IDs for add/edit/move/delete
  and cascades; events are published after success and not after errors;
  actor/client are propagated; import sets `Reload`.
- Versioning: new card version 1; update/move increments; stale version returns
  `ErrCardConflict`; SQLite migration adds the column to an old file; memory
  and Sheets behave per contract.
- HTTP:
  - `X-Kanban-Patch: true` returns a valid JSON envelope with the expected
    fragments for add/edit/move/delete/rename;
  - `HX-Request: true` still returns the board fragment;
  - classic posts still return the full page;
  - 409 conflict body/status;
  - `GET /ws` upgrades, sends `ready`, receives a broadcast from a mutation,
    isolates boards, rejects a cross-origin upgrade, and skips the origin
    client;
  - server-rendered card HTML contains `data-markdown` only on description and
    objective, not on the title;
  - static vendor assets are served with the expected cache headers.
- Existing harness tests: update `NewServices`/`BoardChange` call sites and any
  mutation-response assertions; keep the existing board, composer, dialog and
  drag-hook contracts. Do not weaken assertions to make the new code pass.
- Architecture test: keep every Go file ≤ 400 lines and every function ≤ 40
  lines; split the patch/WS code into small files as needed.

### 13.2 Browser/manual tests

No JS test framework exists in the repository; use a two-browser manual matrix
and document the results. At minimum:

- Chrome + Firefox (desktop), Safari (macOS), one mobile viewport.
- Two tabs on the same board:
  - add, move/drag, edit, delete, column rename → the other tab updates without
    a reload, with a highlight and a named toast;
  - own tab does not double-apply or flicker.
- Two boards open in two tabs: card events do not leak across boards; board
  create/delete updates both sidebars.
- Kill the server or the network for 10 seconds, restore it: status changes,
  reconnect happens, the board resyncs, no stale pending cards remain.
- Server rejection (blocked move, empty title, duplicate column): optimistic
  change rolls back and the error is visible.
- Concurrent edit: tab A opens the edit form, tab B edits the same card; A sees
  the warning; Reload and Overwrite both behave as specified.
- Markdown: description with bold/italic/code/list/link/heading; objective with
  line breaks; raw `<script>` and `javascript:` link are neutralized; Markdown
  appears correctly after an optimistic add, after a live patch and in the
  dialog.
- No JavaScript: board renders, forms post, sidebar visible, raw Markdown is
  readable.
- Mobile: toasts do not cover controls; drag is absent; move buttons work.

### 13.3 Commands

```bash
gofmt -l .                    # must print nothing
go vet ./...                  # clean
go test ./... -count=1        # green
go test ./internal/architecture -run TestSize -count=1
go test -bench . ./internal/adapters/primary/http
```

---

## 14. Acceptance criteria

- [ ] A user sees add/move/edit/delete/column results immediately, before the
      server answers; no full-board flash or scroll jump.
- [ ] A denied action restores the previous DOM and shows the server’s error.
- [ ] Every optimistic action in section 6.2 has a tested rollback path.
- [ ] Two browsers on the same board see each other’s changes within 500 ms,
      with the affected piece highlighted and a toast naming the actor.
- [ ] The originating tab does not double-apply its own change (own echo is
      suppressed server-side and client-side).
- [ ] Board create/delete updates the sidebar on every connected client; a
      client on a deleted board is sent to a surviving board.
- [ ] Card/column events never leak to a client subscribed to another board.
- [ ] A disconnect shows a status change, reconnects with backoff, and performs
      a full board resync; missed events are not left stale.
- [ ] The “warn + choose” conflict flow works for edits and moves; a stale save
      returns 409; Reload and Overwrite both behave as specified.
- [ ] Description and objective render basic Markdown in cards and in the card
      dialog, including after optimistic insertion and live patches.
- [ ] Title, column names and board names remain plain text.
- [ ] Raw HTML is sanitized; XSS payloads do not execute; images/scripts/
      iframes/javascript URLs are removed.
- [ ] Without JavaScript the board and all forms still work; raw Markdown is
      readable; the sidebar remains visible.
- [ ] The server returns targeted patches for JS actions, the board fragment
      for HTMX, and the full page for classic posts.
- [ ] The SQLite migration adds card versions; existing data starts at version
      1; update/move increments; stale edits are rejected.
- [ ] `toView` no longer performs repeated full-board scans per card; the
      200-card benchmark shows the improvement.
- [ ] `Server-Timing` and slow-request logging are present; patch payloads are
      measurably smaller than full-board responses for one-card actions.
- [ ] `/ws` rejects cross-origin upgrades and survives heartbeat/idle periods.
- [ ] `gofmt -l .` empty, `go vet ./...` clean, `go test ./... -count=1` green,
      and the architecture size test passes.
- [ ] README documents the real-time behaviour, Markdown, display names, the
      WebSocket/proxy requirements, and the single-instance limitation.

---

## 15. Out of scope

- Authentication, authorization, real user accounts and trusted identity.
- Presence avatars, typing indicators, cursor sharing or “who is online” lists.
- Persistent activity history, notifications outside the browser tab, email or
  push notifications.
- CRDT/OT merging, automatic field-level merge, offline editing and an offline
  mutation queue.
- Multi-instance fan-out and a Redis/NATS/DB-polling broker. The in-process bus
  is single-instance by design; the seam is documented.
- Server-side Markdown rendering (`goldmark`, `bluemonday`) and a Markdown
  preview/toolbar in the edit form.
- GFM tables, task lists, strikethrough, footnotes, autolinks and images.
- Replacing HTMX with a SPA framework; the server-rendered templates and the
  classic/no-JS path remain.
- Board rename, column reordering, search/filter, due dates and labels.
- Migrating from SQLite to Cloudflare D1 or another storage.
- A JS unit-test framework/build pipeline; browser QA is manual for this
  iteration.

---

## 16. Assumptions to verify before implementation

1. The Freebuff “D1” deployment keeps a **single long-running process** and a
   persistent SQLite file. This was selected in the interview but the storage
   mount was marked “not sure”.
2. The reverse proxy supports WebSocket upgrades and does not time out idle
   connections below 60 seconds.
3. The deployment serves the static directory that ships with the binary;
   `marked` and `DOMPurify` must be copied there, not fetched at runtime.
4. The user base is 1–5 concurrent viewers per board, so per-event board reads
   and in-process fan-out are acceptable.
5. Display names are self-declared labels, not security identities; the app
   remains unauthenticated.
6. If the deployment is actually serverless or multi-instance, the WebSocket
   and in-process observer design must be revised before coding; the browser
   polling fallback is not a substitute for a shared broker.
7. HTMX remains available from unpkg. If it is blocked in production, vendor
   HTMX into `/static/vendor/` as a follow-up; the app JS should not depend on
   the CDN for optimistic actions.

---

## Appendix A — Example event flow (move)

```
Browser A (client tab-1)                Server                         Browser B (tab-2)
──────────────────────────              ──────                         ──────────────────
drop card on Doing
  ├─ move DOM immediately
  ├─ snapshot source column
  └─ POST /cards/move
     X-Kanban-Patch: true
     X-Kanban-Actor: Alice
     X-Kanban-Client: tab-1
                                        MoveCard:
                                          read board
                                          check stage
                                          update card + version
                                          cascade dependents
                                          publish Event{
                                            Kind: card.moved,
                                            BoardID: bd_1,
                                            Actor: Alice,
                                            ClientID: tab-1,
                                            CardIDs: [card_1, dep_2],
                                            ColumnIDs: [todo, doing]
                                          }
                                        render patch from current board
     ◄── 200 JSON patch ────────────────
  replace temp columns
  render markdown
                                        hub skips tab-1 (own echo)
                                                                        ◄── WS patch ─────
                                                                     apply fragments
                                                                     highlight cards
                                                                     toast “Alice moved …”
```

## Appendix B — Example Markdown input/output

Input (description):

```markdown
Fix the **login** card.

- [ ] not a task list item (task lists are out of scope)
- `validateToken` returns false
- See [the docs](https://example.com/docs)

<script>alert('xss')</script>
```

Rendered (sanitized):

```html
<p>Fix the <strong>login</strong> card.</p>
<ul>
  <li>[ ] not a task list item (task lists are out of scope)</li>
  <li><code>validateToken</code> returns false</li>
  <li>See <a href="https://example.com/docs" target="_blank"
      rel="noopener noreferrer nofollow">the docs</a></li>
</ul>
```

The `<script>` is removed by DOMPurify. The literal `[ ]` stays text because
task lists are not enabled.
