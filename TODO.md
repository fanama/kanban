# TODO

Checklist for the kanban app. Done items document what is already verified;
open items point at the next useful changes.

## Done

- [x] Hexagonal core: `domain` depends on nothing, `usecase` talks to storage
      through the `ports.BoardRepository` contract, HTTP is a driving adapter.
- [x] Cards: add, edit (title, description, objective, priority, column),
      move with the stage rule, delete (dependents keep their other
      constraints, this one is dropped).
- [x] Stage rule in both directions: backwards moves demote dependents by the
      smallest necessary amount; forward moves pull the dependents along
      transitively or are refused.
- [x] **Columns are fully editable at runtime:**
  - add (`POST /columns`, the `+` form in the sidebar),
  - rename (`POST /columns/{id}/rename`, ✎ pencil in each column header;
        the column keeps its ID, position and cards),
  - delete (`POST /columns/{id}/delete`, ✕; the cards fall back to the
        previous column; the last column is protected).
- [x] **Multiple boards:** sidebar tabs, `kanban_board` cookie, content fully
      isolated per board, last board protected (`ErrLastBoard`); SQLite keeps
      one `boards` table with `board_id` scoping; Google Sheets stays a single
      board and refuses board operations (`ErrBoardsUnsupported`).
- [x] Export/import a board as JSON (validated against the live board rules).
- [x] Responsive Trello-inspired UI: OS light/dark tokens, snap-scrolling
      columns, off-canvas sidebar, per-column card composer, native desktop
      drag and drop between legal adjacent lists, and a read-only card dialog
      that keeps inline editing. Styles live in a served static stylesheet.
- [x] Legacy migration: an old `kanban.db` without boards/columns is upgraded
      in place (content becomes the board *My board*).
- [x] Tests: use-case tests plus real-HTTP harness tests, a repository
      contract shared by memory and SQLite, a real WebSocket/patch harness, and
      a fake-Sheets server so the gsheets adapter needs no Google credentials.
      `go vet ./...` clean.
- [x] Clean architecture split: four small input ports (boards, cards,
      columns, import/export) with shared reader/ID/cascade collaborators,
      manual constructor injection, storage and HTTP adapters split by
      responsibility, and an automated `internal/architecture` test enforcing
      the 400-line file / 40-line function caps.
- [x] Removed stale files: the abandoned `clean-code-architecture-spec.md`
      (describing another project) and the unused `opencode.json` tooling
      config.
- [x] SQLite keeps a column ID **per board** (`PRIMARY KEY (board_id, id)`),
      matching the in-memory adapter: a second board can start with its own
      `To Do / In Progress / Done`, and the repository contract pins it. Files
      written by the previous release are rebuilt in place on open.
- [x] **Live sync:** use cases publish through the `internal/events` observer
      bus; a same-origin WebSocket fans server-rendered card/column/sidebar
      patches out per board. Reconnects back off and resync the board; slow
      clients are dropped; own echoes are suppressed on both sides.
- [x] **Front-first actions:** add, move, edit, delete, column and board
      actions update the DOM before the request, reconcile with the JSON patch,
      and roll back to the previous DOM with the server error on refusal.
- [x] **Card Markdown:** description and objective render a basic dialect with
      vendored `marked` + `DOMPurify`; images, scripts, styles, forms, event
      handlers and `javascript:` URLs are stripped. Titles and board/list names
      stay plain text.
- [x] **Card versioning:** `version` starts at 1, advances on edit/move in
      SQLite, memory and Sheets, and a stale edit returns `409` with a
      reload/overwrite conflict dialog.
- [x] **Render and transport cost:** `boardFacts` removes repeated full-board
      scans per card, a 200-card benchmark guards the path, and gzip,
      `Server-Timing` and slow-request logs make deployed latency visible.
- [x] **Card order:** dropping on the upper or lower half of another card in
      the same list inserts above or below it. SQLite stores a position column,
      memory keeps an order slice, Sheets rewrites the visible rows, and the
      live patch refreshes the whole column for every viewer.

## Next steps / ideas

- [ ] **Reorder columns** without deleting them: a use case that swaps two
      neighbours (and renumbers) would let the default `To Do | In Progress |
      Done` become any workflow at runtime.
- [ ] **Board renaming** is still missing: a board is created with a name and
      never changed afterwards.
- [ ] **Due dates / tags on cards** — the card form has free-text fields
      (`Description`, `Objective`) but no date or label support.
- [ ] Search / filter on the board (by title or priority) for boards with many
      cards.
- [ ] Keyboard-friendly flow: HTMX focus handling after a swap so the add-card
      form is ready for typing without a click.
- [ ] Add a Dockerfile for the cgo/SQLite binary (the proxy and WebSocket
      deployment requirements are now documented in the README).
- [ ] Replace the in-process event bus with a shared broker (Redis pub/sub,
      NATS or Postgres `LISTEN/NOTIFY`) before running more than one app
      instance; the `events.Publisher` seam is already in place.
- [ ] Vendor HTMX under `static/vendor/` too, so the browser layer does not
      depend on unpkg being reachable in production.
- [ ] Onboarding the Google Sheets adapter into the shared repository
      contract is impossible while it holds a single board — either document
      that limit clearly (README does) or work out a single-board contract
      variant.
- [ ] **Known limitation:** SQLite still keys card IDs globally, so importing
      a board's export into a *different* board fails with `card already
      exists` (HTTP 500). Give `cards` a `(board_id, id)` key, carry
      `board_id` into `card_dependencies` for the cascades, and migrate
      existing files; the in-memory adapter already behaves correctly.
