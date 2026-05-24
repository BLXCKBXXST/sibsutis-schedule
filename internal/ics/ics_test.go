package ics

import (
	"strings"
	"testing"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

// sample — пара Физика во вторник числителя 09:50–11:25.
func sample() model.Schedule {
	return model.Schedule{
		Target: model.Target{Type: model.TypeStudent, Query: "ИКС-531"},
		Title:  "ИКС-531",
		Weeks: []model.Week{
			{Name: "числитель", Days: []model.Day{
				{Weekday: "Понедельник"},
				{Weekday: "Вторник", Lessons: []model.Lesson{
					{Number: 2, TimeFrom: "09:50", TimeTo: "11:25",
						Subject: "Физика", Type: "Лекция",
						Teachers: []string{"Иванов И.И."}, Room: "а.101"},
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
}

func TestRenderScheduleStructure(t *testing.T) {
	s := sample()
	loc := krskLoc()
	anchor := time.Date(2026, 5, 24, 12, 0, 0, 0, loc) // вс 24.05
	until := time.Date(2026, 6, 30, 0, 0, 0, 0, loc)

	out := string(RenderSchedule(s, Options{Anchor: anchor, Until: until}))

	// Базовая обёртка должна быть на месте.
	for _, mustHave := range []string{
		"BEGIN:VCALENDAR\r\n",
		"END:VCALENDAR\r\n",
		"BEGIN:VTIMEZONE\r\n",
		"TZID:Asia/Krasnoyarsk\r\n",
		"BEGIN:VEVENT\r\n",
		"END:VEVENT\r\n",
		"SUMMARY:Физика (Лекция)",
		"LOCATION:а.101",
		"DESCRIPTION:Иванов И.И.",
		"RRULE:FREQ=WEEKLY;INTERVAL=2;UNTIL=",
	} {
		if !strings.Contains(out, mustHave) {
			t.Errorf("в .ics не найдено: %q\n%s", mustHave, out)
		}
	}

	// Ближайший вторник числителя после 24.05.2026 — 26.05.2026.
	if !strings.Contains(out, "DTSTART;TZID=Asia/Krasnoyarsk:20260526T095000") {
		t.Errorf("DTSTART должен указывать на 26.05.2026 09:50: %s", out)
	}
	if !strings.Contains(out, "DTEND;TZID=Asia/Krasnoyarsk:20260526T112500") {
		t.Errorf("DTEND должен указывать на 26.05.2026 11:25: %s", out)
	}
}

func TestEscapeICSText(t *testing.T) {
	cases := map[string]string{
		"Простой текст":  "Простой текст",
		"a, b; c \\ d":   `a\, b\; c \\ d`,
		"line1\nline2":   `line1\nline2`,
		"line1\r\nline2": `line1\nline2`,
	}
	for in, want := range cases {
		if got := escapeICSText(in); got != want {
			t.Errorf("escape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUIDStable(t *testing.T) {
	l := model.Lesson{Number: 2, Subject: "Физика", Subgroup: ""}
	a := lessonUID("student-икс-531", 0, 1, l)
	b := lessonUID("student-икс-531", 0, 1, l)
	if a != b {
		t.Errorf("UID должен быть стабильным: %s != %s", a, b)
	}
	// Изменение Subject меняет UID; изменение Room — нет (в lessonUID Room не учтён).
	l2 := l
	l2.Subject = "Математика"
	if lessonUID("student-икс-531", 0, 1, l2) == a {
		t.Error("UID не должен совпадать при смене дисциплины")
	}
	l3 := l
	l3.Room = "а.999"
	if lessonUID("student-икс-531", 0, 1, l3) != a {
		t.Error("UID должен сохраняться при смене аудитории")
	}
}

func TestFirstOccurrenceSkipsBadTime(t *testing.T) {
	loc := krskLoc()
	anchor := time.Date(2026, 5, 24, 0, 0, 0, 0, loc)
	bad := model.Lesson{Number: 1, TimeFrom: "", TimeTo: ""}
	if _, _, ok := firstOccurrence(0, 1, bad, anchor, loc); ok {
		t.Error("пара без времени должна быть пропущена")
	}
}
