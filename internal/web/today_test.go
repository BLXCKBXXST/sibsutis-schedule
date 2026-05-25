package web

import (
	"testing"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

// todayTestSchedule — фикстура: числитель содержит пары в Вторник
// (Программирование 09:50–11:25, Физкультура 08:00–09:35), знаменатель —
// в Среду (Физика 11:40–13:15). Lesson.Dates у этих фикстур не заполнен:
// текущая логика не должна их использовать — всё считается по WeekParity.
func todayTestSchedule() model.Schedule {
	return model.Schedule{
		Weeks: []model.Week{
			{Name: "числитель", Days: []model.Day{
				{Weekday: "Понедельник"},
				{Weekday: "Вторник", Lessons: []model.Lesson{
					{Number: 1, TimeFrom: "08:00", TimeTo: "09:35", Subject: "Физкультура"},
					{Number: 2, TimeFrom: "09:50", TimeTo: "11:25", Subject: "Программирование"},
				}},
				{Weekday: "Среда"}, {Weekday: "Четверг"}, {Weekday: "Пятница"},
				{Weekday: "Суббота"}, {Weekday: "Воскресенье"},
			}},
			{Name: "знаменатель", Days: []model.Day{
				{Weekday: "Понедельник"},
				{Weekday: "Вторник"},
				{Weekday: "Среда", Lessons: []model.Lesson{
					{Number: 3, TimeFrom: "11:40", TimeTo: "13:15", Subject: "Физика"},
				}},
				{Weekday: "Четверг"}, {Weekday: "Пятница"},
				{Weekday: "Суббота"}, {Weekday: "Воскресенье"},
			}},
		},
	}
}

func TestComputeTodayHint(t *testing.T) {
	s := todayTestSchedule()
	loc := krskLocation

	// Вт 26.05.2026 (числитель) — есть пары, IsExactDay=true.
	got := computeTodayHint(s, time.Date(2026, 5, 26, 10, 0, 0, 0, loc))
	if !got.Found || got.WeekIdx != 0 || got.DayIdx != 1 || !got.IsExactDay {
		t.Errorf("вторник числителя: %+v", got)
	}

	// Ср 03.06.2026 (знаменатель) — есть пары, IsExactDay=true.
	got = computeTodayHint(s, time.Date(2026, 6, 3, 13, 0, 0, 0, loc))
	if !got.Found || got.WeekIdx != 1 || got.DayIdx != 2 || !got.IsExactDay {
		t.Errorf("среда знаменателя: %+v", got)
	}

	// Ср 27.05.2026 (числитель) — в числителе среда без пар.
	// Перебор 14 дней даст ближайший рабочий день: 03.06.2026 (ср знаменателя).
	got = computeTodayHint(s, time.Date(2026, 5, 27, 9, 0, 0, 0, loc))
	if !got.Found || got.IsExactDay {
		t.Errorf("27.05 без пар: ожидался upcoming Found=true, %+v", got)
	}
	if got.WeekIdx != 1 || got.DayIdx != 2 {
		t.Errorf("upcoming = %+v, ожидалось WeekIdx=1, DayIdx=2", got)
	}

	// Расписание без пар вообще → Found=false.
	empty := model.Schedule{Weeks: []model.Week{
		{Days: make([]model.Day, 7)},
		{Days: make([]model.Day, 7)},
	}}
	if got := computeTodayHint(empty, time.Date(2026, 5, 26, 0, 0, 0, 0, loc)); got.Found {
		t.Errorf("пустое расписание: ожидался Found=false, %+v", got)
	}
}

func TestHighlights(t *testing.T) {
	s := todayTestSchedule()
	loc := krskLocation

	// Вт 26.05.2026 10:00 (числитель) — идёт Программирование (09:50–11:25),
	// следующая по циклу — Физика в ср знаменателя 03.06 11:40.
	hl := highlights(s, time.Date(2026, 5, 26, 10, 0, 0, 0, loc))
	if hl.Now == nil || hl.Now.WeekIdx != 0 || hl.Now.DayIdx != 1 || hl.Now.TimeFrom != "09:50" {
		t.Errorf("Now (Программирование) = %+v", hl.Now)
	}
	if hl.Next == nil || hl.Next.WeekIdx != 1 || hl.Next.DayIdx != 2 || hl.Next.TimeFrom != "11:40" {
		t.Errorf("Next (Физика 03.06) = %+v", hl.Next)
	}
	want := time.Date(2026, 6, 3, 11, 40, 0, 0, loc)
	if !hl.NextAt.Equal(want) {
		t.Errorf("NextAt = %v, want %v", hl.NextAt, want)
	}

	// Тот же вторник в 12:00 — обе сегодняшние пары прошли, Now=nil,
	// ближайшая — Физика 03.06.
	hl = highlights(s, time.Date(2026, 5, 26, 12, 0, 0, 0, loc))
	if hl.Now != nil {
		t.Errorf("Now после пары: %+v", hl.Now)
	}
	if hl.Next == nil || hl.Next.WeekIdx != 1 || hl.Next.DayIdx != 2 {
		t.Errorf("Next = %+v", hl.Next)
	}

	// Воскресенье 24.05.2026 (предыдущая неделя — знаменатель) — Now=nil,
	// ближайшая в шаблоне = пн числителя 25.05 в 08:00 Физкультура? Нет, в
	// фикстуре пн пуст. Тогда 26.05 (вт числителя) 08:00 Физкультура.
	hl = highlights(s, time.Date(2026, 5, 24, 23, 0, 0, 0, loc))
	if hl.Next == nil {
		t.Fatalf("Next в воскресенье = nil")
	}
	wantAt := time.Date(2026, 5, 26, 8, 0, 0, 0, loc)
	if !hl.NextAt.Equal(wantAt) {
		t.Errorf("NextAt в вс = %v, want %v", hl.NextAt, wantAt)
	}

	// Пустое расписание → Next=nil.
	empty := model.Schedule{Weeks: []model.Week{
		{Days: make([]model.Day, 7)},
		{Days: make([]model.Day, 7)},
	}}
	if hl := highlights(empty, time.Date(2026, 5, 26, 0, 0, 0, 0, loc)); hl.Now != nil || hl.Next != nil {
		t.Errorf("пустое расписание: %+v", hl)
	}
}

// TestHighlightsCoversSubgroupsInSameSlot — регрессия на баг, где из двух
// параллельных подгрупп в одном слоте подсвечивалась только первая.
// Теперь highlights возвращает slotRef (не индекс пары), и сравнение в
// шаблоне идёт по TimeFrom — обе пары слота получают одинаковый класс.
func TestHighlightsCoversSubgroupsInSameSlot(t *testing.T) {
	loc := krskLocation
	s := model.Schedule{
		Weeks: []model.Week{
			{Name: "числитель", Days: []model.Day{
				{Weekday: "Понедельник"},
				{Weekday: "Вторник", Lessons: []model.Lesson{
					{Number: 2, TimeFrom: "09:50", TimeTo: "11:25",
						Subject: "Физика", Subgroup: "1"},
					{Number: 2, TimeFrom: "09:50", TimeTo: "11:25",
						Subject: "Математика", Subgroup: "2"},
				}},
				{Weekday: "Среда"}, {Weekday: "Четверг"}, {Weekday: "Пятница"},
				{Weekday: "Суббота"}, {Weekday: "Воскресенье"},
			}},
			{Name: "знаменатель", Days: []model.Day{
				{Weekday: "Понедельник"}, {Weekday: "Вторник"}, {Weekday: "Среда"},
				{Weekday: "Четверг"}, {Weekday: "Пятница"}, {Weekday: "Суббота"},
				{Weekday: "Воскресенье"},
			}},
		},
	}

	// is-now: в 10:00 идёт слот 09:50–11:25; в нём обе подгруппы.
	hl := highlights(s, time.Date(2026, 5, 26, 10, 0, 0, 0, loc))
	if hl.Now == nil || hl.Now.WeekIdx != 0 || hl.Now.DayIdx != 1 || hl.Now.TimeFrom != "09:50" {
		t.Fatalf("Now slot = %+v, ожидался (0,1,09:50)", hl.Now)
	}

	// Проверяем, что обе строки реально получат класс is-now: имитируем
	// то, что делает lessonClass в шаблоне — сравнение по (wi, di, TimeFrom).
	for li, l := range s.Weeks[0].Days[1].Lessons {
		match := hl.Now.WeekIdx == 0 && hl.Now.DayIdx == 1 && hl.Now.TimeFrom == l.TimeFrom
		if !match {
			t.Errorf("подгруппа Lesson[%d] %q не попала под подсветку слота", li, l.Subject)
		}
	}
}

func TestWeekdayIndex(t *testing.T) {
	monday := time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)
	if got := weekdayIndex(monday); got != 0 {
		t.Errorf("понедельник = %d, want 0", got)
	}
	sunday := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	if got := weekdayIndex(sunday); got != 6 {
		t.Errorf("воскресенье = %d, want 6", got)
	}
}
