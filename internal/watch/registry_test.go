package watch

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

func TestRegistryTouchAndList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.json")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	t1 := model.Target{Type: model.TypeStudent, Query: "ИКС-531"}
	t2 := model.Target{Type: model.TypeTeacher, Query: "Иванов И.И."}
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)

	if isNew, _ := r.Touch(t1, now); !isNew {
		t.Error("первый Touch должен вернуть isNew=true")
	}
	if isNew, _ := r.Touch(t1, now.Add(time.Hour)); isNew {
		t.Error("повторный Touch должен вернуть isNew=false")
	}
	if _, err := r.Touch(t2, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	list := r.List()
	if len(list) != 2 {
		t.Fatalf("len=%d, want 2", len(list))
	}
	// Самый свежий (t2) идёт первым.
	if list[0].Query != "Иванов И.И." {
		t.Errorf("первый = %s, want Иванов И.И.", list[0].Query)
	}
}

func TestRegistryPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.json")
	r, _ := Open(path)
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	_, _ = r.Touch(model.Target{Type: model.TypeStudent, Query: "A"}, now)

	r2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	list := r2.List()
	if len(list) != 1 || list[0].Query != "A" {
		t.Errorf("после reopen: %+v", list)
	}
}

func TestRegistryPrune(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.json")
	r, _ := Open(path)
	old := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	fresh := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)
	_, _ = r.Touch(model.Target{Type: model.TypeStudent, Query: "OLD"}, old)
	_, _ = r.Touch(model.Target{Type: model.TypeStudent, Query: "FRESH"}, fresh)

	cutoff := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	n, err := r.Prune(cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("Prune вернул %d, want 1", n)
	}
	if list := r.List(); len(list) != 1 || list[0].Query != "FRESH" {
		t.Errorf("после Prune: %+v", list)
	}
}

func TestRegistryConcurrentSafe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.json")
	r, _ := Open(path)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tgt := model.Target{Type: model.TypeStudent, Query: "G" + string(rune('A'+i%5))}
			_, _ = r.Touch(tgt, time.Now())
			_ = r.List()
		}(i)
	}
	wg.Wait()
	if got := len(r.List()); got > 5 || got == 0 {
		t.Errorf("ожидалось 1..5 уникальных target, получено %d", got)
	}
}
