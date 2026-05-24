package store

import (
	"testing"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

var testTarget = model.Target{Type: model.TypeStudent, Query: "ИА-321"}

func sampleSchedule(subject string, at time.Time) model.Schedule {
	return model.Schedule{
		Target:    testTarget,
		Title:     "ИА-321",
		FetchedAt: at,
		Weeks: []model.Week{
			{Name: "числитель", Days: []model.Day{
				{Weekday: "Понедельник", Lessons: []model.Lesson{
					{Number: 1, TimeFrom: "08:00", TimeTo: "09:35", Subject: subject},
				}},
			}},
		},
	}
}

func firstSubject(s model.Schedule) string {
	for _, w := range s.Weeks {
		for _, d := range w.Days {
			if len(d.Lessons) > 0 {
				return d.Lessons[0].Subject
			}
		}
	}
	return ""
}

func TestSaveLoadRoundTrip(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := testTarget.Key()

	want := sampleSchedule("Матан", time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC))
	saved, id, err := st.Save(key, want)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !saved {
		t.Fatal("первая версия должна быть сохранена")
	}

	got, err := st.Load(key, id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Title != want.Title || firstSubject(got) != "Матан" {
		t.Errorf("загруженная версия не совпадает: %+v", got)
	}
	if got.Target != testTarget {
		t.Errorf("Target не сохранился: %+v", got.Target)
	}
}

func TestSaveDedup(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := testTarget.Key()

	// Та же по содержанию версия, но выгружена позже — не должна создавать файл.
	first := sampleSchedule("Матан", time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC))
	second := sampleSchedule("Матан", time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC))

	if saved, _, err := st.Save(key, first); err != nil || !saved {
		t.Fatalf("Save first: saved=%v err=%v", saved, err)
	}
	saved, _, err := st.Save(key, second)
	if err != nil {
		t.Fatalf("Save second: %v", err)
	}
	if saved {
		t.Error("идентичное расписание не должно создавать новую версию")
	}

	infos, err := st.List(key)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 1 {
		t.Errorf("ожидалась 1 версия после дедупа, получено %d", len(infos))
	}
}

func TestSaveNewVersionOnChange(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := testTarget.Key()

	if _, _, err := st.Save(key, sampleSchedule("Матан", time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
	saved, _, err := st.Save(key, sampleSchedule("Физика", time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	if !saved {
		t.Error("изменившееся расписание должно создавать новую версию")
	}

	latest, _, err := st.Latest(key)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if firstSubject(latest) != "Физика" {
		t.Errorf("Latest вернул не самую свежую версию: %q", firstSubject(latest))
	}

	infos, err := st.List(key)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Errorf("ожидалось 2 версии, получено %d", len(infos))
	}
}

func TestListLatest(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := testTarget.Key()
	// Сохраняем 3 различные версии (даты разные → разный hash → dedup пропустит).
	for i, subj := range []string{"Матан", "Физика", "Программирование"} {
		ts := time.Date(2026, 5, 14, 10+i, 0, 0, 0, time.UTC)
		if _, _, err := st.Save(key, sampleSchedule(subj, ts)); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}

	all, err := st.ListLatest(key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("ListLatest(0) = %d, want 3", len(all))
	}
	// Новейшая — первой; FetchedAt идёт по убыванию.
	if !all[0].FetchedAt.After(all[1].FetchedAt) || !all[1].FetchedAt.After(all[2].FetchedAt) {
		t.Errorf("порядок FetchedAt от новых к старым нарушен: %v / %v / %v",
			all[0].FetchedAt, all[1].FetchedAt, all[2].FetchedAt)
	}

	top2, err := st.ListLatest(key, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(top2) != 2 {
		t.Errorf("ListLatest(2) = %d, want 2", len(top2))
	}
	if !top2[0].FetchedAt.Equal(all[0].FetchedAt) {
		t.Errorf("ListLatest(2)[0] != ListLatest(0)[0]")
	}
}

func TestHistoryPerTargetIsolated(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	groupKey := testTarget.Key()
	roomKey := model.Target{Type: model.TypeRoom, Query: "1-101"}.Key()

	if _, _, err := st.Save(groupKey, sampleSchedule("Матан", time.Now())); err != nil {
		t.Fatal(err)
	}
	// У другого target'а истории быть не должно.
	if _, _, err := st.Latest(roomKey); err != ErrNoHistory {
		t.Errorf("история другого target'а должна быть пуста, получено %v", err)
	}

	targets, err := st.Targets()
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(targets) != 1 || targets[0].Key != groupKey {
		t.Errorf("Targets = %+v, ожидался один %q", targets, groupKey)
	}
	if targets[0].Versions != 1 || targets[0].Target != testTarget {
		t.Errorf("сводка target'а неверна: %+v", targets[0])
	}
}

func TestLatestEmpty(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Latest("student-несуществует"); err != ErrNoHistory {
		t.Errorf("ожидалась ErrNoHistory, получено %v", err)
	}
}

func TestMeta(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := testTarget.Key()

	m, err := st.ReadMeta(key)
	if err != nil {
		t.Fatalf("ReadMeta пустой: %v", err)
	}
	if !m.LastCheck.IsZero() {
		t.Error("пустая meta должна иметь нулевой LastCheck")
	}

	if err := st.RecordCheck(key, false, "сайт недоступен"); err != nil {
		t.Fatalf("RecordCheck fail: %v", err)
	}
	if err := st.RecordCheck(key, true, ""); err != nil {
		t.Fatalf("RecordCheck success: %v", err)
	}
	m, err = st.ReadMeta(key)
	if err != nil {
		t.Fatal(err)
	}
	if m.LastCheck.IsZero() || m.LastSuccess.IsZero() {
		t.Error("после успешной проверки LastCheck и LastSuccess не должны быть нулевыми")
	}
	if m.LastError != "" {
		t.Errorf("после успеха LastError должен быть пуст, получено %q", m.LastError)
	}
}
