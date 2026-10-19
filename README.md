# Kanban App — Go core in a Hexagonal (Ports & Adapters) architecture

A small kanban app whose business logic is completely independent of HTMX,
`net/http`, SQLite and Google Sheets. The core is driven by tests without a
server, and the storage backend is a swappable adapter. The app holds
**several boards**, each with its own columns and cards.

```
cmd/server            Composition root (manual constructor injection)
internal/domain       Entities + invariants (Board, Card, Column, Priority, BoardView)
internal/ports        input.go (BoardService, CardService, ColumnService,
                      ImportExportService) / output.go (BoardRepository)
internal/usecase      Four services (boards, cards, columns, import/export) over
                      shared collaborators: a board reader, an ID generator and
                      the move/demote/pull cascade
internal/events       In-process observer bus, event kinds and request identity
internal/architecture Structural guardrails: the 400-line / 40-function test
internal/adapters
  primary/http        HTMX handler, templates, WebSocket hub, patch renderer,
                      gzip/timing middleware and static browser modules
                      (driving adapter)
  secondary/sqlite    SQLite repository          (driven adapter, default)
  secondary/memory    In-memory repository       (driven adapter, tests/demo)
  secondary/gsheets   Google Sheets repository   (driven adapter, optional, one board)
harness               Tests driving the services, the HTTP adapter and the
                      repository contract shared by every storage adapter
```

Dependency rule: `domain` imports nothing, `usecase` imports `domain`, `ports`
and the small `events` publisher interface, adapters import the core. Nothing in
the core imports an adapter. The services receive their repository and event
publisher through the constructor, never build one, and the composition root
(`cmd/server`) is the only place that knows a concrete storage; it passes the
four services and one event bus to the HTTP handler.

## Structure and guardrails

Every Go file stays at **400 lines or fewer** and every function at **40 lines
or fewer**. `go test ./internal/architecture` parses the tree and fails on a
violation; the served stylesheet
(`internal/adapters/primary/http/static/style.css`) is the single documented
exception, since a stylesheet has no function-level unit. Test files follow the
same caps.

Use case code is split by responsibility: `board_service.go`, `card_service.go`,
`column_service.go` and `transfer_service.go`, with the shared reader, the ID
generator and the cascade in their own files. The HTTP adapter mirrors that
split (one file per surface plus `view.go` for the render models), and the
storage adapters are split by table (boards, cards, columns, schema, snapshot).

The browser layer has no build step. Plain ES modules live in
`internal/adapters/primary/http/static/`: `app.js` boots the page, `identity.js`
owns the display name and tab ID, `actions.js` applies optimistic changes,
`patches.js` reconciles live fragments, `ws.js` owns reconnect/resync,
`markdown.js` runs the sanitizer, and `ui.js` renders toasts and dialogs.
`marked` and `DOMPurify` are vendored under `static/vendor/`; HTMX is still
loaded from unpkg.

## Run it

```bash
go run ./cmd/server          # http://localhost:8080, cards in ./kanban.db
```

Environment:

| Variable | Default | Purpose |
| --- | --- | --- |
| `STORAGE` | `sqlite` | Storage adapter: `sqlite`, `memory` or `sheets` |
| `KANBAN_DB` | `kanban.db` | SQLite file (created on first run) |
| `PORT` | `8080` | Listen port |
| `TEMPLATE_GLOB` | `internal/adapters/primary/http/templates/*.html` | Template location (run from the module root) |
| `STATIC_DIR` | `internal/adapters/primary/http/static` | Stylesheet directory served under `/static/` |
| `SHEET_URL` | *empty* | Spreadsheet link; selecting it switches to `sheets` (ID and `gid` are parsed out) |
| `SHEET_ID` | *empty* | Bare spreadsheet ID (alternative to `SHEET_URL`) |
| `SHEET_NAME` | *first tab* | Pin a tab by title |
| `SHEET_GID` | *first tab* | Pin a tab by the `gid` in the sheet URL |
| `GOOGLE_APPLICATION_CREDENTIALS` | *ADC* | Service-account JSON for the Sheets API |

### SQLite storage (default)

`sqlite` writes to `KANBAN_DB` (default `kanban.db`):

```sql
boards(id TEXT PRIMARY KEY, name TEXT NOT NULL)

cards(id TEXT PRIMARY KEY, title TEXT NOT NULL, description TEXT NOT NULL,
      objective TEXT NOT NULL, status TEXT NOT NULL, priority TEXT NOT NULL,
      version INTEGER NOT NULL DEFAULT 1, created_at TIMESTAMP NOT NULL,
      board_id TEXT NOT NULL)

card_dependencies(card_id TEXT REFERENCES cards(id) ON DELETE CASCADE,
                  depends_on_id TEXT REFERENCES cards(id) ON DELETE CASCADE,
                  position INTEGER NOT NULL,
                  PRIMARY KEY (card_id, depends_on_id))

columns(id TEXT NOT NULL, name TEXT NOT NULL, position INTEGER NOT NULL,
        board_id TEXT NOT NULL, PRIMARY KEY (board_id, id))
```

The `status` column of `cards` holds the ID of the card's column; the name is
historical (the default column IDs are the status names an earlier version
used), so an existing file needs no data rewrite. `version` starts at `1` and
advances on every edit and move; `Update` checks the version the caller started
from and returns `ErrCardConflict` instead of overwriting a newer edit.

Constraints live in their own table, so a card can never depend on a card that
does not exist. The old free-text `constraints` column of earlier versions is no
longer read or written; it is simply left in place if your file still has it.

Cards and columns survive restarts: the tables are created on first run (an
empty database also gets the default columns `To Do | In Progress | Done` and
two demo cards) and `GetAll` returns cards in insertion order. A `kanban.db`
created by an older version is upgraded in place on startup: the missing columns
of `cards` are added (empty by default), the columns table is created, and the
existing cards keep their stage. The file is in `.gitignore` — it is runtime
data, not source. Set `KANBAN_DB` (e.g. to a path outside the repo) or
`STORAGE=memory` if you want nothing on disk.

The driver is `github.com/mattn/go-sqlite3` (cgo), matching the other Go service
in this repository. The connection is capped at one writer, so the app never
hits `database is locked`.

Cards and columns belong to a board (a `board_id` column), and the `boards`
table gives the workspaces their names. A database written before boards
existed is migrated in place: its content becomes the board `My board`, with
nothing renamed.

### Google Sheets storage (optional)

```bash
export SHEET_URL="https://docs.google.com/spreadsheets/d/<spreadsheet-id>/edit#gid=0"
# and one of:
export GOOGLE_APPLICATION_CREDENTIALS=/path/to/service-account.json   # service account
gcloud auth application-default login \
  --scopes=https://www.googleapis.com/auth/spreadsheets,https://www.googleapis.com/auth/cloud-platform   # or ADC
go run ./cmd/server
```

Startup prints the storage it picked, e.g.
`Using Google Sheets storage: spreadsheet 1Jll86…, tab "Feuille 1"`. No tab name
is needed: the adapter asks the API for the spreadsheet's tabs and uses the one
matching the URL's `gid` (or the first tab).

The adapter writes the `ID | Title | Status | Priority | Description |
Objective | BlockedBy | Version` header on first use (range `A1:H1`), reads
cards from `A2:H`, and moving a card writes its column cell and the next
version. The `Status` column holds the card's column ID — the name is the one a
spreadsheet written by an earlier version already has. `BlockedBy` holds the
constraint card IDs separated by commas (semicolons and extra spaces are read
too). Rows whose ID column is empty fall back to `row_<n>`, an unknown priority
degrades to `Low`, and a missing `Version` cell reads as `1`.

The board's columns live in their own `Columns` tab (`ID | Name | Position`),
created on first use; deleting a column blanks its row and renumbers the rest,
because the spreadsheet is what a human reads.

A spreadsheet holds **exactly one board** (the tab you pointed the storage at,
bearing the tab's name): `ListBoards` reports it, and creating or deleting a
board is refused (`ErrBoardsUnsupported`). Deleting a card blanks its row in
place, so the order a human reads is never disturbed.

The account needs the Sheets API enabled on its project and access to the
file: a service account must be given edit rights (share the sheet with its
`client_email`); a user account must own or be able to edit it.

## Test the core without a network

```bash
go test ./... -count=1        # harness + adapter tests + size guardrails
go vet ./...
gofmt -l .                    # must print nothing
go test -bench . ./internal/adapters/primary/http
```

`harness/board_test.go` exercises the input port directly (no HTTP) and the
primary adapter end-to-end (real HTTP requests against `httptest`), including
the HTMX fragment contract, the per-column composer and the static stylesheet.
`harness/realtime_test.go` covers patch responses, `409` conflicts and a real
WebSocket client receiving a broadcast. `harness/repository_contract_test.go`
runs the same expectations against **every** `BoardRepository` adapter (memory
and SQLite), so the output port behaves identically whichever storage is wired
in. `internal/adapters/secondary/gsheets` is tested against a fake Sheets API
server, so it needs no Google credentials.

## The board

`Column` is a stage of the board: an `ID`, a `Name` and a `Position`. The
columns are edited at runtime — `POST /columns` appends one,
`POST /columns/{id}/rename` replaces its name (the column keeps its ID,
position and cards; a blank name is refused with `ErrEmptyColumnName`, a
sibling's name with `ErrColumnExists`) and `POST /columns/{id}/delete` removes
one — and a board always keeps at least one (`ErrLastColumn`). Deleting a
column does not lose its cards: they fall back to *the previous column* (or to
the one that becomes first when the deleted column was the first), so a card
never moves forward by accident.

The sidebar is where the board is built: a **Lists** section holds the
`+` form (always visible, whatever the width of the lists area), and every
list header carries a ✎ pencil (inline rename form) and a ✕ button. Each list
also has its own **+ Add a card** composer at the bottom, so a card can be
created directly where it belongs; `POST /cards` without a `column_id` still
targets the first list.

## Look and interaction

The board is a responsive, Trello-inspired layout: lists are fixed-width
columns that scroll sideways with scroll snapping on a phone, and each list
scrolls its own cards. The sidebar is persistent on desktop and becomes an
off-canvas drawer (with a backdrop) below 900 px. Without JavaScript the
sidebar simply stays visible above the board.

The palette follows the operating system (`prefers-color-scheme`): a light
board canvas with white cards, and a dark variant, both using the same indigo
brand tokens. The stylesheet lives in
`internal/adapters/primary/http/static/style.css` and is served at
`/static/style.css` (`STATIC_DIR` overrides the directory). There is no CSS
build step and no external font or icon dependency.

Desktop cards can be dragged between **adjacent** lists: backwards is always
allowed, forwards only when the stage rule permits the advance (the same
condition that enables the move button). A blocked list never accepts a drop;
the server still enforces every rule. Touch devices use the move buttons.

Clicking a card title opens its read-only detail in a native `<dialog>`. The
**Edit** button inside it swaps in the existing inline edit form; a successful
save, move or delete re-renders the board and closes the dialog. `Esc`, the
backdrop and the ✕ button close it. Drag and drop, the dialog and the mobile
drawer are progressive enhancements: the classic forms keep working without
JavaScript.

## Live board and optimistic actions

With JavaScript enabled, every add / move / edit / delete / column / board
action is applied to the DOM immediately, before the request finishes. The
request runs in the background with `X-Kanban-Patch: true`; the server answers
with a small JSON patch, and the optimistic node is replaced by the
authoritative fragment. If the server refuses the action, the previous DOM is
restored and the server message stays in `#flash`.

A WebSocket at `/ws?board=<id>&client=<tab-id>` carries the same patches to
every other viewer of that board. The application services publish an event
through `internal/events` after the repository commits; the HTTP adapter turns
the event into card/column fragments, counts and a board summary, and the
browser applies only those pieces, highlights them and shows a toast naming
the actor. The originating tab ignores its own echo. Board create/delete events
update every sidebar; card/column events never cross boards.

If the socket closes, the topbar shows **Reconnecting…** and reconnects with
exponential backoff; after five failures it falls back to polling every five
seconds. A reconnect (or a tab returning after more than 30 seconds hidden)
fetches the current board fragment and replaces `#board`, so missed events are
not left stale. A dirty edit form blocks the replacement: live updates pause
until the form closes.

A first visit asks for a display name, stored in `localStorage` and sent as
`X-Kanban-Actor` (with the per-tab `X-Kanban-Client`). This is a courtesy label
in toasts, **not authentication** — anyone can pick any name.

Card versions make concurrent edits explicit. The edit form submits the version
it started from; a stale save returns `409` and the browser offers **Reload**
(the fresh version) or **Overwrite** (copy the fresh version and deliberately
resubmit the local text). A live patch during an open edit does not overwrite
the form; it shows the same warning.

### Markdown in cards

`Description` and `Objective` are rendered as Markdown in the card and in the
read-only dialog; titles, column names and board names stay plain text. The
dialect is intentionally basic: paragraphs, line breaks, emphasis, `h1`–`h3`,
inline code, fenced code, blockquotes, lists and links. Tables, task lists,
strikethrough, footnotes, autolinks and images are not supported. Raw HTML is
sanitized by DOMPurify with an allowlist: scripts, styles, iframes, images,
forms, event handlers and `javascript:` URLs are removed. Rendering happens in
the browser after initial load, after HTMX swaps, after live patches and for
optimistic cards. Without JavaScript the raw Markdown is still readable as
text.

The **no-JS** path is intact: classic posts return the whole page, HTMX
requests still receive the `#board` fragment, and the sidebar stays visible.
Drag, the dialog, live updates and formatted Markdown are the enhancements that
need JavaScript.

## The card

`Card` carries the work item: `Title` (the only required field), `Description`,
`Objective`, `DependsOn`, `ColumnID`, `Priority` and `Version`. `Description`
and `Objective` are Markdown source (multi-line); an empty value means "not
filled in yet". `UpdateCard` overlays the edited fields on the stored card and
refuses an empty title, an unknown column, or an edit whose `Version` is not
the stored one (`ErrCardConflict`).

Deleting a card (`DeleteCard`, the ✕ button) removes it and *drops the
constraint* from the cards that pointed at it: a dependent keeps its other
constraints and nothing lingers as a ghost. Nothing else moves: deleting a
constraint only ever frees the dependent.

### The stage rule

A card may only sit in column *n* when its constraints are at stage *n-1* or
further: to reach column *n+1*, a constraint must be at stage *n* at least. The
rule is enforced by the application core on every move, whether it comes from a
move button or from an edit form:

- blocked move → `ErrCardBlocked`, and the message names the cards that block
  it *and* the stage they must reach first; the board shows them and locks the
  button instead of offering it;
- the first column is always reachable, so a card can always be brought back;
- the rule is about constraints: a card that has none is placed wherever the
  caller asks, and the board's move buttons only ever offer one stage anyway;
- a constraint must exist (`ErrUnknownDependency`), cannot be the card itself
  (`ErrSelfDependency`) and cannot create a cycle (`ErrDependencyCycle`).

### Two directions, one rule

A card that moves **backwards** never leaves a dependent stranded: every card
that depends on it is demoted to the stage just after it — *the smallest move
that restores the rule*, never further back than necessary — and that card drags
its own dependents in turn. The same cascade runs after the cards of a deleted
column fall back to the previous stage.

A card that moves **forwards** never passes the cards that depend on it: they
are pulled along, transitively — carrying one card carries its own dependents.
A pull that would break a pulled card's own stage rule is refused
(`ErrWouldLeaveDependents`): the constraint stays put rather than jump over
someone it cannot bring. Moving one card therefore moves the whole chain it
connects, in both directions — nothing can deadlock.

On the `UpdateCard` input port, `DependsOn: nil` means "leave the constraints
as they are" so a partial edit cannot drop them, while an empty non-nil list
explicitly removes every constraint. The edit form always submits the field
(including an empty hidden one), so unticking every box clears them.

## HTTP surface

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/` | Full board page (the selected board — `?board=` switches) |
| `POST` | `/boards` | Create a board (`name`); the browser is switched to it |
| `POST` | `/boards/{id}/delete` | Delete a board and everything on it (the last board stays) |
| `POST` | `/cards` | Add a card (`title`, `priority`, optional `column_id`; defaults to the first list) |
| `POST` | `/cards/move` | Move a card one stage (`card_id`, `column_id`) |
| `GET` | `/cards/{id}` | Read-only card fragment (leaves edit mode) |
| `GET` | `/cards/{id}/edit` | Card edit form fragment |
| `POST` | `/cards/{id}` | Save an edit (`title`, `description`, `objective`, `depends_on` (repeated), `column_id`, `priority`, `version`) |
| `POST` | `/cards/{id}/delete` | Delete a card (its dependents lose only this constraint) |
| `POST` | `/columns` | Append a column (`name`) |
| `GET` | `/columns/{id}` | Column header fragment (cancels rename mode) |
| `GET` | `/columns/{id}/rename` | Inline rename form fragment |
| `POST` | `/columns/{id}/rename` | Replace a column's name (`name`) |
| `POST` | `/columns/{id}/delete` | Delete a column (its cards fall back one stage) |
| `GET` | `/export.json` | Download the selected board as a JSON file (`kanban-<name>.json`) |
| `POST` | `/import.json` | Replace the selected board with a JSON file (`file` upload or `application/json` body) |
| `GET` | `/ws?board=<id>&client=<tab-id>` | Same-origin WebSocket carrying board and sidebar patches |

Requests carrying `HX-Request: true` receive only the `<main id="board">`
fragment, which HTMX swaps in place. Plain form posts return the whole page, so
the board also works with JavaScript disabled — including the edit form, whose
`POST /cards/{id}` also works as a classic form submission.

JavaScript mutations send `X-Kanban-Patch: true` (plus `X-Kanban-Actor` and
`X-Kanban-Client`) and receive a JSON patch envelope instead of HTML:

```json
{
  "type": "patch", "board_id": "bd_1", "seq": 42, "event": "card.moved",
  "actor": "Alice", "client_id": "tab-a",
  "fragments": [{"target": "card", "id": "card_1", "html": "<article></article>"}],
  "remove": [], "reload": false,
  "notice": {"kind": "card.moved", "actor": "Alice", "card_title": "Design it",
             "to": "Doing"}
}
```

Fragments target `card`, `column-head`, `column-cards`, `columns`,
`board-summary` or `sidebar-boards`; `reload: true` makes the browser fetch the
full `#board` fragment. Board create/delete in patch mode answer with
`{"type":"redirect","url":"/?board=…"}` so the optimistic sidebar can finish
with a normal navigation.

Errors: `404` for an unknown card, column or board, `409` for a stale card
version, `400` for an empty title, a blocked move, an empty or duplicate column
name, deleting the last column, an unknown constraint or a constraint cycle, an
invalid board file, an empty or duplicated board name, deleting the last board,
or a board operation a single-board storage cannot do; `500` otherwise. The
page shows the message in the `#flash` element and keeps the current board.

## Boards

The page opens on a sidebar: the list of boards (the active one highlighted,
the others links that switch, saved in the `kanban_board` cookie), a `New
board…` form and a `Delete this board` button that appears once there is more
than one board. A new board is created with the default columns `To Do |
In Progress | Done`, ready to use. Deleting a board takes its columns and its
cards with it; the last board stays (`ErrLastBoard`), an empty state is a file
import (below) or a fresh storage away.

A board switch goes through `?board=` (the handler saves the choice in the
`kanban_board` cookie, so the following links and form posts keep it). Export,
import and every card/column operation address the selected board — one file
loads into one board, it does not touch the others.


## JSON export / import

The toolbar has **Export JSON** (a plain `GET /export.json` link that downloads
`kanban-<board-name>.json`) and **Load a board…** (a file upload posted to
`/import.json`). No JavaScript is needed for either: the import form also posts
as a classic form, and a script may instead `POST /import.json` an
`application/json` body directly. The exported file is the selected board; the
import replaces the content of the selected board.

A board file is the `columns` in order plus every `card`: fields as in the
SQLite or Sheets rows, but with real types. An example file:

```json
{
  "columns": [
    {"id": "To Do", "name": "To Do", "position": 0},
    {"id": "In Progress", "name": "In Progress", "position": 1},
    {"id": "Done", "name": "Done", "position": 2}
  ],
  "cards": [
    {"id": "card_1", "title": "Design it", "description": "**Draft** it",
     "objective": "", "depends_on": [], "column_id": "To Do",
     "priority": "High", "version": 1}
  ]
}
```

Loading is done by the application core (the import use case), so it behaves
the same whatever the storage adapter is: the whole board — columns and cards
alike — is replaced in one operation. The file is checked with the same rules
the live board obeys: a title is mandatory, a constraint must exist and can
neither be the card itself nor form a cycle, card IDs must be unique and non
empty; a card whose column is not in the file falls back to the first one and
card positions are renumbered densely. A card's `depends_on` may be an array
of card IDs or a single string of IDs separated by commas, semicolons or
newlines (the same form the Sheets `BlockedBy` cell uses). Imported cards keep
a positive `version` (a missing or zero version becomes `1`).

## Deployment notes

Live updates use an **in-process** bus. That is enough for the intended
single long-running process (1–5 viewers per board), but it does **not** work
behind a load balancer with several app instances: an event published by
instance A is invisible to clients connected to instance B. Keep one instance,
or replace the bus with a shared broker (Redis pub/sub, NATS, Postgres
`LISTEN/NOTIFY`) before scaling horizontally.

The reverse proxy must forward the `Upgrade` and `Connection` headers for
`/ws`, must not buffer or gzip that stream, and should keep idle connections
open longer than the 30-second heartbeat (60+ seconds recommended). The app
sends `Server-Timing: app;dur=…` on normal responses and logs requests slower
than 250 ms with status and response size; HTML, CSS, JS and JSON responses are
gzipped when the client accepts it.

`STATIC_DIR` must exist in the deployed image, including `static/vendor/`
(`marked` and `DOMPurify` are served locally; HTMX still comes from unpkg). The
static directory is copied with the binary — the app does not use `go:embed`.
`KANBAN_DB` must point at a persistent volume for SQLite; a request-scoped or
serverless runtime would lose the database and cannot hold WebSocket
connections. If the app sleeps after inactivity, expect one reconnect and a
full board resync after wake; the topbar status makes that visible.
