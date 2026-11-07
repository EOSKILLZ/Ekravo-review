package store

import (
	"context"
	"strings"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestLearningsCRUDAndIsolation(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	id, err := s.AddLearning(ctx, "o/r", "internal/**", "Prefer early returns.\nUse errors.Is.", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("zero id")
	}

	list, err := s.ListLearnings(ctx, "o/r", true)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if strings.Contains(list[0].Text, "\n") {
		t.Fatalf("learning text was not normalised: %q", list[0].Text)
	}
	if list[0].PathGlob != "internal/**" || !list[0].Enabled {
		t.Fatalf("learning = %+v", list[0])
	}

	texts, err := s.LearningTexts(ctx, "o/r", []string{"internal/app/app.go"})
	if err != nil || len(texts) != 1 {
		t.Fatalf("texts = %v, %v", texts, err)
	}
	if texts, _ := s.LearningTexts(ctx, "o/r", []string{"docs/readme.md"}); len(texts) != 0 {
		t.Fatalf("glob should not match: %v", texts)
	}
	if err := s.SetLearningEnabled(ctx, "o/r", id, false); err != nil {
		t.Fatal(err)
	}
	texts, _ = s.LearningTexts(ctx, "o/r", []string{"internal/app/app.go"})
	if len(texts) != 0 {
		t.Fatalf("disabled learning injected: %v", texts)
	}

	other, err := s.AddLearning(ctx, "other/r", "", "Different repo", "chat")
	if err != nil || other == id {
		t.Fatalf("other id = %d, %v", other, err)
	}
	if list, _ := s.ListLearnings(ctx, "o/r", true); len(list) != 1 {
		t.Fatalf("repo isolation failed: %+v", list)
	}

	ok, err := s.DeleteLearning(ctx, "o/r", id)
	if err != nil || !ok {
		t.Fatalf("delete = %v, %v", ok, err)
	}
	if ok, _ := s.DeleteLearning(ctx, "o/r", other); ok {
		t.Fatal("deleted a learning from another repo")
	}
}

func TestUpdateLearningEditsFields(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	id, err := s.AddLearning(ctx, "o/r", "internal/**", "First rule.", "chat")
	if err != nil {
		t.Fatal(err)
	}

	text, glob := "Second rule.", "cmd/**"
	if ok, err := s.UpdateLearning(ctx, "o/r", id, &text, &glob, nil); err != nil || !ok {
		t.Fatalf("update = %v, %v", ok, err)
	}
	list, err := s.ListLearnings(ctx, "o/r", true)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if list[0].Text != "Second rule." || list[0].PathGlob != "cmd/**" || !list[0].Enabled {
		t.Fatalf("learning = %+v", list[0])
	}

	disabled := false
	if ok, err := s.UpdateLearning(ctx, "o/r", id, nil, nil, &disabled); err != nil || !ok {
		t.Fatalf("disable = %v, %v", ok, err)
	}
	if list, _ := s.ListLearnings(ctx, "o/r", true); list[0].Enabled {
		t.Fatal("learning was not disabled")
	}

	if ok, _ := s.UpdateLearning(ctx, "other/r", id, &text, nil, nil); ok {
		t.Fatal("updated a learning from another repo")
	}
	blank := "   "
	if _, err := s.UpdateLearning(ctx, "o/r", id, &blank, nil, nil); err == nil {
		t.Fatal("blank text accepted")
	}
	if _, err := s.UpdateLearning(ctx, "o/r", id, nil, nil, nil); err == nil {
		t.Fatal("empty update accepted")
	}
}

func TestAddLearningRejectsEmpty(t *testing.T) {
	if _, err := openTestStore(t).AddLearning(context.Background(), "o/r", "", "  ", "chat"); err == nil {
		t.Fatal("empty learning accepted")
	}
}
