package parse

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestParseScheduleSample(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "sample.html"))
	if err != nil {
		t.Fatal(err)
	}

	sched, err := ParseSchedule(string(raw))
	if err != nil {
		t.Fatalf("ParseSchedule: %v", err)
	}

	if sched.RawHTML == "" {
		t.Error("RawHTML должен сохраняться всегда")
	}
	if len(sched.Weeks) != 2 {
		t.Fatalf("недель = %d, want 2", len(sched.Weeks))
	}
	if sched.Weeks[0].Name != "числитель" || sched.Weeks[1].Name != "знаменатель" {
		t.Errorf("имена недель: %q, %q", sched.Weeks[0].Name, sched.Weeks[1].Name)
	}
	for i, wk := range sched.Weeks {
		if len(wk.Days) != 7 {
			t.Fatalf("неделя %d: дней = %d, want 7", i, len(wk.Days))
		}
	}
	if got := sched.LessonCount(); got != 3 {
		t.Errorf("LessonCount = %d, want 3", got)
	}

	// Числитель, понедельник: пара во 2-м слоте (09:50).
	mon := sched.Weeks[0].Days[0]
	if mon.Weekday != "Понедельник" {
		t.Errorf("день 0 Weekday = %q", mon.Weekday)
	}
	if len(mon.Lessons) != 1 {
		t.Fatalf("пар в понедельник числителя = %d, want 1", len(mon.Lessons))
	}
	l := mon.Lessons[0]
	if l.Number != 2 {
		t.Errorf("Number = %d, want 2 (2-й слot времени)", l.Number)
	}
	if l.TimeFrom != "09:50" || l.TimeTo != "11:25" {
		t.Errorf("время = %s–%s, want 09:50–11:25", l.TimeFrom, l.TimeTo)
	}
	if l.Subject != "Базы данных" || l.Type != "Лекционные занятия" {
		t.Errorf("дисциплина/тип = %q / %q", l.Subject, l.Type)
	}
	if len(l.Teachers) != 1 || l.Teachers[0] != "Преподаватель А.А." {
		t.Errorf("Teachers = %v", l.Teachers)
	}
	if l.Room != "а.210 (К.1)" {
		t.Errorf("Room = %q", l.Room)
	}
	if len(l.Groups) != 1 || l.Groups[0] != "ТЕСТ-11" {
		t.Errorf("Groups = %v", l.Groups)
	}

	// Вторник числителя: TEACHER пришёл строкой, не массивом — должен стать срезом.
	tue := sched.Weeks[0].Days[1]
	if len(tue.Lessons) != 1 || len(tue.Lessons[0].Teachers) != 1 ||
		tue.Lessons[0].Teachers[0] != "Преподаватель Б.Б." {
		t.Errorf("вторник: Teachers из строки = %v", tue.Lessons[0].Teachers)
	}
	if tue.Lessons[0].Subgroup != "1 подгруппа" {
		t.Errorf("вторник: Subgroup = %q", tue.Lessons[0].Subgroup)
	}

	// Среда числителя — занятий нет.
	if len(sched.Weeks[0].Days[2].Lessons) != 0 {
		t.Errorf("среда числителя должна быть пустой")
	}

	// Знаменатель, понедельник: пара с двумя преподавателями.
	monDen := sched.Weeks[1].Days[0]
	if len(monDen.Lessons) != 1 || len(monDen.Lessons[0].Teachers) != 2 {
		t.Errorf("знаменатель понедельник: %+v", monDen.Lessons)
	}
	if monDen.Lessons[0].Subject != "Физика" {
		t.Errorf("знаменатель понедельник Subject = %q", monDen.Lessons[0].Subject)
	}
}

func TestParseScheduleNoData(t *testing.T) {
	// Страница без встроенных данных расписания (например, страница выбора).
	_, err := ParseSchedule("<html><body><h3>Выберите группу</h3></body></html>")
	if !errors.Is(err, ErrNoScheduleData) {
		t.Errorf("ожидалась ErrNoScheduleData, получено %v", err)
	}
}

func TestParseScheduleIgnoresFactDaysVar(t *testing.T) {
	// fact_schedule_days[1] не должен распознаваться как days[1].
	html := `<script>fact_schedule_days[1] = 'null'</script>`
	_, err := ParseSchedule(html)
	if !errors.Is(err, ErrNoScheduleData) {
		t.Errorf("fact_schedule_days не должен парситься как days; получено %v", err)
	}
}
