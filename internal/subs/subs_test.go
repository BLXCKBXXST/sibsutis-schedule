package subs

import (
	"testing"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

var (
	tGroup   = model.Target{Type: model.TypeStudent, Query: "ИКС-531"}
	tTeacher = model.Target{Type: model.TypeTeacher, Query: "Иванов И.И."}
	tRoom    = model.Target{Type: model.TypeRoom, Query: "1-101"}
)

func TestAddListRemove(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	added, err := st.Add(42, tGroup)
	if err != nil || !added {
		t.Fatalf("Add: added=%v err=%v", added, err)
	}
	// повторная подписка — added=false
	added, _ = st.Add(42, tGroup)
	if added {
		t.Error("повторная подписка не должна добавлять")
	}

	st.Add(42, tTeacher)

	list := st.List(42)
	if len(list) != 2 {
		t.Errorf("List(42) = %d, want 2", len(list))
	}

	removed, _ := st.Remove(42, tGroup)
	if !removed {
		t.Error("Remove существующей подписки должен вернуть true")
	}
	removed, _ = st.Remove(42, tGroup)
	if removed {
		t.Error("повторное Remove должно вернуть false")
	}

	if got := len(st.List(42)); got != 1 {
		t.Errorf("после Remove List = %d, want 1", got)
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	st, _ := New(dir)
	st.Add(42, tGroup)
	st.Add(99, tRoom)

	// перечитываем с диска новым Store — содержимое должно сохраниться
	again, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.List(42)) != 1 || again.List(42)[0].Key() != tGroup.Key() {
		t.Errorf("после перезапуска подписка 42 потерялась")
	}
	if len(again.List(99)) != 1 {
		t.Errorf("после перезапуска подписка 99 потерялась")
	}
}

func TestSubscribers(t *testing.T) {
	st, _ := New(t.TempDir())
	st.Add(1, tGroup)
	st.Add(2, tGroup)
	st.Add(3, tTeacher)

	subs := st.Subscribers(tGroup)
	if len(subs) != 2 || subs[0] != 1 || subs[1] != 2 {
		t.Errorf("Subscribers(tGroup) = %v", subs)
	}
	if len(st.Subscribers(tRoom)) != 0 {
		t.Error("у tRoom подписчиков быть не должно")
	}
}

func TestUniqueTargets(t *testing.T) {
	st, _ := New(t.TempDir())
	st.Add(1, tGroup)
	st.Add(2, tGroup)
	st.Add(2, tTeacher)
	st.Add(3, tRoom)

	uniq := st.UniqueTargets()
	if len(uniq) != 3 {
		t.Errorf("UniqueTargets = %d, want 3 (%+v)", len(uniq), uniq)
	}
}

func TestRemoveEmptiesChat(t *testing.T) {
	st, _ := New(t.TempDir())
	st.Add(42, tGroup)
	st.Remove(42, tGroup)

	// после удаления единственной подписки — пустой список
	if got := st.List(42); len(got) != 0 {
		t.Errorf("List(42) после Remove = %v, want []", got)
	}
}
