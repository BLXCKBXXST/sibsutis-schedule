package web

import (
	"testing"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

func todayTestSchedule() model.Schedule {
	d := func(y, m, day int) time.Time { return time.Date(y, time.Month(m), day, 0, 0, 0, 0, time.UTC) }
	return model.Schedule{
		Weeks: []model.Week{
			{Name: "числитель", Days: []model.Day{
				{Weekday: "Понедельник"},
				{Weekday: "Вторник", Lessons: []model.Lesson{
					{Number: 2, TimeFrom: "09:50", TimeTo: "11:25",
						Subject: "Программирование",
						Dates:   []time.Time{d(2026, 6, 2), d(2026, 6, 16)}},
				}},
				{Weekday: "Среда"}, {Weekday: "Четверг"}, {Weekday: "Пятница"},
				{Weekday: "Суббота"}, {Weekday: "Воскресенье"},
			}},
			{Name: "знаменатель", Days: []model.Day{
				{Weekday: "Понедельник"},
				{Weekday: "Вторник"},
				{Weekday: "Среда", Lessons: []model.Lesson{
					{Number: 3, TimeFrom: "11:40", TimeTo: "13:15",
						Subject: "Физика",
						Dates:   []time.Time{d(2026, 6, 10)}},
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

	// Сегодня = вторник 02-06-2026, есть пара в числителе.
	got := computeTodayHint(s, time.Date(2026, 6, 2, 10, 0, 0, 0, loc))
	if !got.Found || got.WeekIdx != 0 || got.DayIdx != 1 || !got.IsExactDay {
		t.Errorf("вторник числителя: %+v", got)
	}

	// Сегодня = среда 10-06-2026, есть пара в знаменателе.
	got = computeTodayHint(s, time.Date(2026, 6, 10, 13, 0, 0, 0, loc))
	if !got.Found || got.WeekIdx != 1 || got.DayIdx != 2 {
		t.Errorf("среда знаменателя: %+v", got)
	}

	// Сегодня = 03-06-2026 (среда без пар) — должен взять ближайший
	// следующий учебный день, IsExactDay=false.
	got = computeTodayHint(s, time.Date(2026, 6, 3, 9, 0, 0, 0, loc))
	if !got.Found || got.IsExactDay {
		t.Errorf("03-06 без пар, ожидался upcoming: %+v", got)
	}
	// Ближайший — 10-06 (среда), знаменатель.
	if got.WeekIdx != 1 || got.DayIdx != 2 {
		t.Errorf("upcoming = %+v, ожидался WeekIdx=1, DayIdx=2", got)
	}

	// Расписание без дат вообще — Found=false.
	empty := model.Schedule{Weeks: []model.Week{{Days: make([]model.Day, 7)}}}
	if got := computeTodayHint(empty, time.Date(2026, 6, 2, 0, 0, 0, 0, loc)); got.Found {
		t.Errorf("пустое расписание: ожидался Found=false, получено %+v", got)
	}
}

func TestHighlights(t *testing.T) {
	s := todayTestSchedule()
	loc := krskLocation

	// Вторник 02-06-2026 в 10:00 — идёт «Программирование» (09:50–11:25),
	// следующая по семестру — Физика 10-06.
	hl := highlights(s, time.Date(2026, 6, 2, 10, 0, 0, 0, loc))
	if hl.Now == nil || hl.Now.WeekIdx != 0 || hl.Now.DayIdx != 1 || hl.Now.LessonIdx != 0 {
		t.Errorf("Now = %+v, ожидался (0,1,0)", hl.Now)
	}
	if hl.Next == nil || hl.Next.WeekIdx != 1 || hl.Next.DayIdx != 2 {
		t.Errorf("Next = %+v, ожидалась Физика 10-06 (1,2,0)", hl.Next)
	}
	want := time.Date(2026, 6, 10, 11, 40, 0, 0, loc)
	if !hl.NextAt.Equal(want) {
		t.Errorf("NextAt = %v, want %v", hl.NextAt, want)
	}

	// Тот же день 12:00 — пары уже нет, но есть следующая 10-06.
	hl = highlights(s, time.Date(2026, 6, 2, 12, 0, 0, 0, loc))
	if hl.Now != nil {
		t.Errorf("Now после пары: %+v, ожидался nil", hl.Now)
	}
	if hl.Next == nil || hl.Next.WeekIdx != 1 {
		t.Errorf("Next: %+v", hl.Next)
	}

	// После всего семестра — оба nil.
	hl = highlights(s, time.Date(2030, 1, 1, 0, 0, 0, 0, loc))
	if hl.Now != nil || hl.Next != nil {
		t.Errorf("после семестра: %+v", hl)
	}
}

func TestWeekdayIndex(t *testing.T) {
	monday := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if got := weekdayIndex(monday); got != 0 {
		t.Errorf("понедельник = %d, want 0", got)
	}
	sunday := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	if got := weekdayIndex(sunday); got != 6 {
		t.Errorf("воскресенье = %d, want 6", got)
	}
}
