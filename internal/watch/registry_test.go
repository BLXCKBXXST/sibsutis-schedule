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

func TestRegistrySubscribeUnsubscribe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.json")
	r, _ := Open(path)
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	target := model.Target{Type: model.TypeStudent, Query: "X"}

	added, _ := r.Subscribe(target, 111, now)
	if !added {
		t.Error("первая подписка должна вернуть added=true")
	}
	added2, _ := r.Subscribe(target, 111, now)
	if added2 {
		t.Error("повторная подписка должна вернуть added=false")
	}
	_, _ = r.Subscribe(target, 222, now)

	subs := r.SubscribersOf(target)
	if len(subs) != 2 {
		t.Errorf("SubscribersOf = %v, want [111 222]", subs)
	}

	if list := r.TargetsForChat(111); len(list) != 1 || list[0].Query != "X" {
		t.Errorf("TargetsForChat(111) = %+v", list)
	}

	removed, _ := r.Unsubscribe(target, 111)
	if !removed {
		t.Error("Unsubscribe должен был убрать 111")
	}
	if subs := r.SubscribersOf(target); len(subs) != 1 || subs[0] != 222 {
		t.Errorf("после Unsubscribe: %v", subs)
	}
}

func TestRegistryPruneKeepsSubscribers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.json")
	r, _ := Open(path)
	old := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	withSubs := model.Target{Type: model.TypeStudent, Query: "WITH_SUBS"}
	_, _ = r.Subscribe(withSubs, 42, old)

	withoutSubs := model.Target{Type: model.TypeStudent, Query: "PLAIN"}
	_, _ = r.Touch(withoutSubs, old)

	cutoff := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)
	n, _ := r.Prune(cutoff)
	if n != 1 {
		t.Errorf("Prune убрал %d, want 1", n)
	}
	if list := r.List(); len(list) != 1 || list[0].Query != "WITH_SUBS" {
		t.Errorf("после Prune остался не тот target: %+v", list)
	}
}

func TestRegistryMarkNotifiedRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.json")
	r, _ := Open(path)
	target := model.Target{Type: model.TypeStudent, Query: "X"}
	_, _ = r.Touch(target, time.Now())
	_ = r.MarkNotified(target, "v1")

	r2, _ := Open(path)
	for _, e := range r2.List() {
		if e.LastNotifiedVersion != "v1" {
			t.Errorf("LastNotifiedVersion после reopen = %q", e.LastNotifiedVersion)
		}
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
