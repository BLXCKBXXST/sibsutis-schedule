package model

import (
	"testing"
	"time"
)

// sample расписания для тестов: две недели, числитель с парой во вторник
// (Программирование, 09:50–11:25, две даты), знаменатель с парой в среду
// (Физика, 11:40–13:15, одна дата), плюс «свободный» вторник.
func sampleSchedule() Schedule {
	d := func(y, m, day int) time.Time { return time.Date(y, time.Month(m), day, 0, 0, 0, 0, time.UTC) }
	return Schedule{
		Weeks: []Week{
			{
				Name: "числитель",
				Days: []Day{
					{Weekday: "Понедельник"},
					{Weekday: "Вторник", Lessons: []Lesson{
						{
							Number:   2,
							TimeFrom: "09:50",
							TimeTo:   "11:25",
							Subject:  "Программирование",
							Dates:    []time.Time{d(2026, 6, 2), d(2026, 6, 16)},
						},
						{
							Number:   1,
							TimeFrom: "08:00",
							TimeTo:   "09:35",
							Subject:  "Физкультура",
							Dates:    []time.Time{d(2026, 6, 2)},
						},
					}},
					{Weekday: "Среда"},
					{Weekday: "Четверг"}, {Weekday: "Пятница"},
					{Weekday: "Суббота"}, {Weekday: "Воскресенье"},
				},
			},
			{
				Name: "знаменатель",
				Days: []Day{
					{Weekday: "Понедельник"},
					{Weekday: "Вторник"},
					{Weekday: "Среда", Lessons: []Lesson{
						{
							Number:   3,
							TimeFrom: "11:40",
							TimeTo:   "13:15",
							Subject:  "Физика",
							Dates:    []time.Time{d(2026, 6, 10)},
						},
					}},
					{Weekday: "Четверг"}, {Weekday: "Пятница"},
					{Weekday: "Суббота"}, {Weekday: "Воскресенье"},
				},
			},
		},
	}
}

func TestLessonsOn(t *testing.T) {
	s := sampleSchedule()
	loc := time.UTC
	got := s.LessonsOn(time.Date(2026, 6, 2, 0, 0, 0, 0, loc))
	if len(got) != 2 {
		t.Fatalf("вторник 02-06-2026: пар = %d, want 2", len(got))
	}
	// Сортировка по TimeFrom: 08:00 перед 09:50.
	if got[0].TimeFrom != "08:00" || got[1].TimeFrom != "09:50" {
		t.Errorf("неверный порядок: %s, %s", got[0].TimeFrom, got[1].TimeFrom)
	}
	// 16 июня — только Программирование.
	got = s.LessonsOn(time.Date(2026, 6, 16, 0, 0, 0, 0, loc))
	if len(got) != 1 || got[0].Subject != "Программирование" {
		t.Errorf("16-06: %+v", got)
	}
	// День без пар.
	if got := s.LessonsOn(time.Date(2026, 6, 3, 0, 0, 0, 0, loc)); got != nil {
		t.Errorf("03-06: ожидался nil, получено %v", got)
	}
	// Время суток должно игнорироваться.
	noon := time.Date(2026, 6, 10, 14, 30, 0, 0, loc)
	if got := s.LessonsOn(noon); len(got) != 1 || got[0].Subject != "Физика" {
		t.Errorf("10-06 в 14:30: %+v", got)
	}
}

func TestWeekIndexFor(t *testing.T) {
	s := sampleSchedule()
	loc := time.UTC
	if idx, ok := s.WeekIndexFor(time.Date(2026, 6, 2, 0, 0, 0, 0, loc)); !ok || idx != 0 {
		t.Errorf("02-06 числитель: idx=%d, ok=%v", idx, ok)
	}
	if idx, ok := s.WeekIndexFor(time.Date(2026, 6, 10, 0, 0, 0, 0, loc)); !ok || idx != 1 {
		t.Errorf("10-06 знаменатель: idx=%d, ok=%v", idx, ok)
	}
	if _, ok := s.WeekIndexFor(time.Date(2026, 6, 3, 0, 0, 0, 0, loc)); ok {
		t.Errorf("03-06: ожидался ok=false")
	}
}

func TestNextLessonAfter(t *testing.T) {
	s := sampleSchedule()
	loc := time.UTC

	// Утром 2 июня → следующая пара 08:00 в этот же день.
	lesson, when, ok := s.NextLessonAfter(time.Date(2026, 6, 2, 7, 0, 0, 0, loc))
	if !ok || lesson.Subject != "Физкультура" {
		t.Fatalf("07:00: %+v, when=%v, ok=%v", lesson, when, ok)
	}
	want := time.Date(2026, 6, 2, 8, 0, 0, 0, loc)
	if !when.Equal(want) {
		t.Errorf("when = %v, want %v", when, want)
	}

	// В 12:00 того же дня — все пары вторника уже прошли,
	// ближайшая будущая — Физика 10 июня 11:40 (а не Программирование 16-го).
	lesson, when, ok = s.NextLessonAfter(time.Date(2026, 6, 2, 12, 0, 0, 0, loc))
	if !ok || lesson.Subject != "Физика" {
		t.Errorf("12:00: %+v", lesson)
	}
	want = time.Date(2026, 6, 10, 11, 40, 0, 0, loc)
	if !when.Equal(want) {
		t.Errorf("when = %v, want %v", when, want)
	}

	// После всех — ok=false.
	if _, _, ok := s.NextLessonAfter(time.Date(2030, 1, 1, 0, 0, 0, 0, loc)); ok {
		t.Errorf("после семестра: ожидался ok=false")
	}
}

func TestStudyDates(t *testing.T) {
	s := sampleSchedule()
	dates := s.StudyDates()
	want := []time.Time{
		time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC),
	}
	if len(dates) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(dates), len(want), dates)
	}
	for i, d := range dates {
		if !d.Equal(want[i]) {
			t.Errorf("dates[%d] = %v, want %v", i, d, want[i])
		}
	}
}
