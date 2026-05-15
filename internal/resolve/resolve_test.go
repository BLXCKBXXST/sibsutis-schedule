package resolve

import (
	"errors"
	"testing"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

func TestParseResultsFlexibleID(t *testing.T) {
	// id приходит то числом, то строкой — оба варианта должны разобраться.
	body := []byte(`{"results":[
		{"id":42,"text":"  ИБ-211 "},
		{"id":"ИВ-021","text":"ИВ-021"}
	]}`)
	matches, err := parseResults(body)
	if err != nil {
		t.Fatalf("parseResults: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("совпадений = %d, want 2", len(matches))
	}
	if matches[0].ID != "42" || matches[0].Text != "ИБ-211" {
		t.Errorf("matches[0] = %+v", matches[0])
	}
	if matches[1].ID != "ИВ-021" {
		t.Errorf("matches[1].ID = %q", matches[1].ID)
	}
}

func TestPickSingle(t *testing.T) {
	m, err := pick([]Match{{ID: "1", Text: "ИБ-211"}},
		model.Target{Type: model.TypeStudent, Query: "ИБ-211"}, mustMeta(t, model.TypeStudent))
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if m.ID != "1" {
		t.Errorf("ID = %q", m.ID)
	}
}

func TestPickNotFound(t *testing.T) {
	_, err := pick(nil,
		model.Target{Type: model.TypeStudent, Query: "ничего"}, mustMeta(t, model.TypeStudent))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("ожидалась ErrNotFound, получено %v", err)
	}
}

func TestPickAmbiguous(t *testing.T) {
	matches := []Match{
		{ID: "1", Text: "Иванов И.И."},
		{ID: "2", Text: "Иванов П.С."},
	}
	_, err := pick(matches,
		model.Target{Type: model.TypeTeacher, Query: "Иванов"}, mustMeta(t, model.TypeTeacher))
	if !errors.Is(err, ErrAmbiguous) {
		t.Errorf("ожидалась ErrAmbiguous, получено %v", err)
	}
}

func TestPickExactAmongMany(t *testing.T) {
	// Несколько совпадений, но одно точно совпадает по тексту с запросом.
	matches := []Match{
		{ID: "1", Text: "Иванов И.И."},
		{ID: "2", Text: "Иванов И.И. (доцент)"},
	}
	m, err := pick(matches,
		model.Target{Type: model.TypeTeacher, Query: "иванов и.и."}, mustMeta(t, model.TypeTeacher))
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if m.ID != "1" {
		t.Errorf("ID = %q, want 1 (точное совпадение по тексту)", m.ID)
	}
}

func TestBuildAjaxURL(t *testing.T) {
	meta := mustMeta(t, model.TypeStudent)
	got, err := buildAjaxURL("https://my.sibsutis.ru/students/schedule/", meta, "ИБ-211")
	if err != nil {
		t.Fatalf("buildAjaxURL: %v", err)
	}
	want := "https://my.sibsutis.ru/ajax/get_groups_soap.php?search_group=%D0%98%D0%91-211"
	if got != want {
		t.Errorf("buildAjaxURL = %q, want %q", got, want)
	}
}

func mustMeta(t *testing.T, typ model.TargetType) model.TypeMeta {
	t.Helper()
	m, ok := model.Target{Type: typ}.Meta()
	if !ok {
		t.Fatalf("нет метаданных для типа %q", typ)
	}
	return m
}
