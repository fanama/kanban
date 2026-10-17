package harness

import (
	"context"
	"errors"
	memoryRepo "kanban-app/internal/adapters/secondary/memory"
	"kanban-app/internal/domain"
	"kanban-app/internal/usecase"
	"testing"
)

func TestExportImportRoundTrip(t *testing.T) {
	testTransferExportBoardSnapshotsTheColumnsInOrderAndEveryCard(t)
	testTransferImportBoardRebuildsTheBoardFromASnapshot(t)
	testTransferImportBoardAppliesTheBoardRulesToTheFile(t)
	testTransferACardWhoseColumnIsUnknownFallsBackToTheFirstColumn(t)
	testTransferDeletingACardKeepsTheDependentsAndTheirOtherConstraints(t)
	testTransferBoardsAreCreatedNamedAndListed(t)
	testTransferBoardsDoNotLeakContentIntoEachOther(t)
	testTransferADeletedBoardTakesItsColumnsAndCardsAndTheLastOneStays(t)
}

func testTransferExportBoardSnapshotsTheColumnsInOrderAndEveryCard(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t, "To Do", "Doing", "Done")
	early := addCard(t, uc, "Early")
	late := addCard(t, uc, "Late")
	setDependencies(t, uc, late, early.ID)
	if _, err := uc.MoveCard(ctx, early.ID, "Doing"); err != nil {
		t.Fatalf("MoveCard failed: %v", err)
	}

	snapshot, err := uc.ExportBoard(ctx)
	if err != nil {
		t.Fatalf("ExportBoard failed: %v", err)
	}
	if len(snapshot.Columns) != 3 || snapshot.Columns[0].Name != "To Do" {
		t.Errorf("Expected the columns in order, got %+v", snapshot.Columns)
	}
	if len(snapshot.Cards) != 2 {
		t.Errorf("Expected 2 cards, got %+v", snapshot.Cards)
	}
	for _, c := range snapshot.Cards {
		if c.ID == late.ID && (len(c.DependsOn) != 1 || c.DependsOn[0] != early.ID) {
			t.Errorf("Expected the constraint in the snapshot, got %+v", c)
		}
	}
}

func testTransferImportBoardRebuildsTheBoardFromASnapshot(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t, "To Do", "Doing", "Done")
	eraseMe := addCard(t, uc, "Erase me")
	early := addCard(t, uc, "Early")
	setDependencies(t, uc, early, eraseMe.ID) // a dependency across the board

	snapshot, err := uc.ExportBoard(ctx)
	if err != nil {
		t.Fatalf("ExportBoard failed: %v", err)
	}

	// Load it into another board — as another person would, maybe backed
	// by a different storage adapter.
	fresh := usecase.NewServices(memoryRepo.NewMemoryRepo())
	workspace, err := fresh.Board.CreateBoard(ctx, "Fresh")
	if err != nil {
		t.Fatalf("CreateBoard failed: %v", err)
	}
	board, err := fresh.Transfer.ImportBoard(ctx, workspace.ID, snapshot)
	if err != nil {
		t.Fatalf("ImportBoard failed: %v", err)
	}
	assertSnapshotImport(t, board, eraseMe, early)

	// The same file imports cleanly again: a snapshot does not decay.
	if _, err := fresh.Transfer.ImportBoard(ctx, workspace.ID, snapshot); err != nil {
		t.Errorf("Re-importing the same snapshot failed: %v", err)
	}
}

func assertSnapshotImport(t *testing.T, board domain.BoardView, erased, early domain.Card) {
	t.Helper()
	if len(board.Columns) != 3 {
		t.Errorf("Expected the 3 columns to come back, got %+v", board.OrderedColumns())
	}
	if _, ok := board.Index()[erased.ID]; !ok {
		t.Error("Expected the imported cards, not the previous content")
	}
	stale, ok := board.Index()[early.ID]
	if !ok {
		t.Fatal("Expected Early to be imported")
	}
	if len(stale.DependsOn) != 1 || stale.DependsOn[0] != erased.ID {
		t.Errorf("Expected the constraints to survive the trip, got %v", stale.DependsOn)
	}
	if got := board.Position(early.ID); got != 0 {
		t.Errorf("Expected Early back in the first column (position 0), got %d", got)
	}
}

func testTransferImportBoardAppliesTheBoardRulesToTheFile(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t)
	for _, tc := range importRuleCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := uc.ImportBoard(ctx, tc.snap)
			if err == nil {
				t.Fatalf("Expected %v, got no error", tc.isErr)
			}
			if !errors.Is(err, tc.isErr) {
				t.Fatalf("Expected %v, got %v", tc.isErr, err)
			}
		})
	}
}

// importRuleCases lists the files the importer must refuse, with the error the
// live board rules attribute to each one.
var importRuleCases = []struct {
	name  string
	snap  domain.BoardSnapshot
	isErr error
}{
	{
		name:  "a file with no column",
		snap:  domain.BoardSnapshot{},
		isErr: domain.ErrInvalidImport,
	},
	{
		name: "an empty title",
		snap: domain.BoardSnapshot{
			Columns: []domain.Column{{ID: "A", Name: "A", Position: 0}},
			Cards:   []domain.Card{{ID: "c1", Title: "  ", ColumnID: "A"}},
		},
		isErr: domain.ErrEmptyTitle,
	},
	{
		name: "a card without an ID",
		snap: domain.BoardSnapshot{
			Columns: []domain.Column{{ID: "A", Name: "A", Position: 0}},
			Cards:   []domain.Card{{Title: "Ghost", ColumnID: "A"}},
		},
		isErr: domain.ErrInvalidImport,
	},
	{
		name: "a duplicate card ID",
		snap: domain.BoardSnapshot{
			Columns: []domain.Column{{ID: "A", Name: "A", Position: 0}},
			Cards: []domain.Card{
				{ID: "c1", Title: "First", ColumnID: "A"},
				{ID: "c1", Title: "Second", ColumnID: "A"},
			},
		},
		isErr: domain.ErrInvalidImport,
	},
	{
		name: "a duplicated column ID",
		snap: domain.BoardSnapshot{
			Columns: []domain.Column{
				{ID: "A", Name: "A", Position: 0},
				{ID: "A", Name: "B", Position: 1},
			},
		},
		isErr: domain.ErrInvalidImport,
	},
	{
		name: "an empty column name",
		snap: domain.BoardSnapshot{
			Columns: []domain.Column{
				{ID: "A", Name: "  ", Position: 0},
			},
		},
		isErr: domain.ErrEmptyColumnName,
	},
	{
		name: "an unknown constraint",
		snap: domain.BoardSnapshot{
			Columns: []domain.Column{{ID: "A", Name: "A", Position: 0}},
			Cards:   []domain.Card{{ID: "c1", Title: "Alone", ColumnID: "A", DependsOn: []string{"ghost"}}},
		},
		isErr: domain.ErrUnknownDependency,
	},
	{
		name: "a self constraint",
		snap: domain.BoardSnapshot{
			Columns: []domain.Column{{ID: "A", Name: "A", Position: 0}},
			Cards:   []domain.Card{{ID: "c1", Title: "Snake", ColumnID: "A", DependsOn: []string{"c1"}}},
		},
		isErr: domain.ErrSelfDependency,
	},
	{
		name: "a constraint cycle",
		snap: domain.BoardSnapshot{
			Columns: []domain.Column{{ID: "A", Name: "A", Position: 0}},
			Cards: []domain.Card{
				{ID: "c1", Title: "One", ColumnID: "A", DependsOn: []string{"c2"}},
				{ID: "c2", Title: "Two", ColumnID: "A", DependsOn: []string{"c1"}},
			},
		},
		isErr: domain.ErrDependencyCycle,
	},
}

func testTransferACardWhoseColumnIsUnknownFallsBackToTheFirstColumn(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t)
	board, err := uc.ImportBoard(ctx, domain.BoardSnapshot{
		Columns: []domain.Column{
			{ID: "A", Name: "A", Position: 0},
			{ID: "B", Name: "B", Position: 1},
		},
		Cards: []domain.Card{{ID: "c1", Title: "Stray", ColumnID: "ghosted"}},
	})
	if err != nil {
		t.Fatalf("ImportBoard failed: %v", err)
	}
	if got, ok := board.Index()["c1"]; !ok || got.ColumnID != "A" {
		t.Errorf("Expected the stray card in the first column, got %+v", got)
	}
}

func testTransferDeletingACardKeepsTheDependentsAndTheirOtherConstraints(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t)
	victim := addCard(t, uc, "Victim")
	keeper := addCard(t, uc, "Keeper")
	holder := addCard(t, uc, "Holder")
	setDependencies(t, uc, holder, victim.ID, keeper.ID)

	if _, err := uc.DeleteCard(ctx, victim.ID); err != nil {
		t.Fatalf("DeleteCard failed: %v", err)
	}

	board, err := uc.GetBoard(ctx)
	if err != nil {
		t.Fatalf("GetBoard failed: %v", err)
	}
	if _, ok := board.Index()[victim.ID]; ok {
		t.Error("Expected the deleted card to be gone")
	}
	if _, ok := board.Index()[keeper.ID]; !ok {
		t.Error("Expected the other cards to stay")
	}
	after, ok := board.Index()[holder.ID]
	if !ok {
		t.Fatal("Expected the dependent to stay")
	}
	if len(after.DependsOn) != 1 || after.DependsOn[0] != keeper.ID {
		t.Errorf("Expected the constraint dropped and the other kept, got %v", after.DependsOn)
	}

	if _, err := uc.DeleteCard(ctx, "ghost"); !errors.Is(err, domain.ErrCardNotFound) {
		t.Errorf("Expected ErrCardNotFound for a deleted card, got %v", err)
	}
}

func testTransferBoardsAreCreatedNamedAndListed(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t)

	created, err := uc.CreateBoard(ctx, "Side quest")
	if err != nil {
		t.Fatalf("CreateBoard failed: %v", err)
	}
	if created.ID == "" || created.Name != "Side quest" {
		t.Errorf("Unexpected board: %+v", created)
	}

	boards, err := uc.ListBoards(ctx)
	if err != nil {
		t.Fatalf("ListBoards failed: %v", err)
	}
	if len(boards) != 2 {
		t.Fatalf("Expected 2 boards, got %+v", boards)
	}

	if _, err := uc.CreateBoard(ctx, "   "); !errors.Is(err, domain.ErrEmptyBoardName) {
		t.Errorf("Expected ErrEmptyBoardName, got %v", err)
	}
	if _, err := uc.CreateBoard(ctx, "side QUEST"); !errors.Is(err, domain.ErrBoardExists) {
		t.Errorf("Expected ErrBoardExists for a duplicated name, got %v", err)
	}
}

func testTransferBoardsDoNotLeakContentIntoEachOther(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t)
	cut, err := uc.CreateBoard(ctx, "Cut")
	if err != nil {
		t.Fatalf("CreateBoard failed: %v", err)
	}

	// The test board gets a card through the wrapper (its own workspace).
	if _, err := uc.AddCard(ctx, "Only on the test board", domain.PriorityLow); err != nil {
		t.Fatalf("AddCard failed: %v", err)
	}

	fresh, err := uc.BoardService.GetBoard(ctx, cut.ID)
	if err != nil {
		t.Fatalf("GetBoard failed: %v", err)
	}
	if fresh.Count() != 0 {
		t.Errorf("Expected the fresh board empty, got %+v", fresh.All())
	}

	// The fresh board gets its own card: the test board's card stays there.
	if _, err := uc.CardService.AddCard(ctx, cut.ID, "", "Cut card", domain.PriorityLow); err != nil {
		t.Fatalf("AddCard on the fresh board failed: %v", err)
	}
	if got := columnOf(t, uc, "Only on the test board"); got != "To Do" {
		t.Errorf("Expected the other board's card untouched, got %q", got)
	}
}

func testTransferADeletedBoardTakesItsColumnsAndCardsAndTheLastOneStays(t *testing.T) {
	ctx := context.Background()
	uc := newBoard(t)
	added, err := uc.CreateBoard(ctx, "Doomed")
	if err != nil {
		t.Fatalf("CreateBoard failed: %v", err)
	}
	if _, err := uc.ColumnService.AddColumn(ctx, added.ID, "Only column"); err != nil {
		t.Fatalf("AddColumn failed: %v", err)
	}
	if _, err := uc.CardService.AddCard(ctx, added.ID, "", "Doom card", domain.PriorityLow); err != nil {
		t.Fatalf("AddCard failed: %v", err)
	}

	if err := uc.DeleteBoard(ctx, added.ID); err != nil {
		t.Fatalf("DeleteBoard failed: %v", err)
	}
	if _, err := uc.BoardService.GetBoard(ctx, added.ID); !errors.Is(err, domain.ErrBoardNotFound) {
		t.Errorf("Expected ErrBoardNotFound after deletion, got %v", err)
	}

	boards, err := uc.ListBoards(ctx)
	if err != nil {
		t.Fatalf("ListBoards failed: %v", err)
	}
	if len(boards) != 1 {
		t.Fatalf("Expected 1 board left, got %+v", boards)
	}
	if err := uc.DeleteBoard(ctx, boards[0].ID); !errors.Is(err, domain.ErrLastBoard) {
		t.Errorf("Expected ErrLastBoard for the last board, got %v", err)
	}
	if err := uc.DeleteBoard(ctx, "ghost"); !errors.Is(err, domain.ErrLastBoard) {
		t.Errorf("Expected ErrLastBoard even with nowhere to fall back, got %v", err)
	}

	// The surviving board still works.
	if _, err := uc.AddCard(ctx, "Still alive", domain.PriorityLow); err != nil {
		t.Errorf("AddCard on the surviving board failed: %v", err)
	}
}
