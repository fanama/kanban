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
- [x] Legacy migration: an old `kanban.db` without boards/columns is upgraded
      in place (content becomes the board *My board*).
- [x] Tests: use-case tests plus real-HTTP harness tests, a repository
      contract shared by memory and SQLite, and a fake-Sheets server so the
      gsheets adapter needs no Google credentials. `go vet ./...` clean.

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
- [ ] Deployment notes in the README (how to run behind a reverse proxy,
      a Dockerfile for the cgo/sqlite binary).
- [ ] Onboarding the Google Sheets adapter into the shared repository
      contract is impossible while it holds a single board — either document
      that limit clearly (README does) or work out a single-board contract
      variant.
