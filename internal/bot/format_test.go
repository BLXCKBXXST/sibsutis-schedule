package bot

import (
	"strings"
	"testing"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

func sampleSchedule() model.Schedule {
	return model.Schedule{
		Target:    model.Target{Type: model.TypeStudent, Query: "ИКС-531"},
		Title:     "ИКС-531",
		FetchedAt: time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC),
		Weeks: []model.Week{
			{Name: "числитель", Days: []model.Day{
				{Weekday: "Понедельник", Lessons: []model.Lesson{
					{Number: 2, TimeFrom: "09:50", TimeTo: "11:25", Subject: "Физика",
						Type: "Лабораторные", Teachers: []string{"Лубский В.В."},
						Room: "а.307 (К.5)", Subgroup: "Подгруппа 2", Groups: []string{"ИКС-531"}},
				}},
				{Weekday: "Вторник"},
				{Weekday: "Среда"}, {Weekday: "Четверг"}, {Weekday: "Пятница"},
				{Weekday: "Суббота"}, {Weekday: "Воскресенье"},
			}},
			{Name: "знаменатель", Days: make([]model.Day, 7)},
		},
	}
}

func TestFormatWeekContainsKeyParts(t *testing.T) {
	msgs := FormatWeek(sampleSchedule(), 0)
	if len(msgs) == 0 {
		t.Fatal("FormatWeek вернул 0 сообщений")
	}
	joined := strings.Join(msgs, "\n")
	checks := []string{
		"<b>Расписание", "группа ИКС-531", "числитель",
		"Понедельник", "Физика", "<b>09:50–11:25</b>",
		"Лубский В.В.", "а.307 (К.5)", "Подгруппа 2",
		"нет занятий", // у пустых дней
	}
	for _, c := range checks {
		if !strings.Contains(joined, c) {
			t.Errorf("в сообщении нет %q", c)
		}
	}
}

func TestFormatDay(t *testing.T) {
	msg := FormatDay(sampleSchedule(), 0, 0)
	if !strings.Contains(msg, "Понедельник") || !strings.Contains(msg, "Физика") {
		t.Errorf("FormatDay не содержит ожидаемых полей: %q", msg)
	}
}

func TestFormatTeacherTargetShowsGroups(t *testing.T) {
	s := sampleSchedule()
	s.Target = model.Target{Type: model.TypeTeacher, Query: "Лубский В.В."}
	msgs := FormatWeek(s, 0)
	joined := strings.Join(msgs, "\n")
	if !strings.Contains(joined, "👥") {
		t.Errorf("для расписания преподавателя должна быть колонка групп")
	}
	if strings.Contains(joined, "Подгруппа 2") {
		t.Errorf("для расписания преподавателя подгруппа не нужна")
	}
}

func TestFormatHTMLEscaping(t *testing.T) {
	s := model.Schedule{
		Target:    model.Target{Type: model.TypeStudent, Query: "<x>"},
		FetchedAt: time.Now(),
		Weeks: []model.Week{
			{Name: "числитель", Days: []model.Day{
				{Weekday: "Понедельник", Lessons: []model.Lesson{
					{TimeFrom: "08:00", TimeTo: "09:35", Subject: "Brackets <test>",
						Teachers: []string{"A&B"}},
				}},
				{}, {}, {}, {}, {}, {},
			}},
		},
	}
	msgs := FormatWeek(s, 0)
	joined := strings.Join(msgs, "\n")
	if strings.Contains(joined, "<test>") {
		t.Error("< > внутри значения должны быть экранированы")
	}
	if !strings.Contains(joined, "A&amp;B") {
		t.Error("& должна стать &amp;")
	}
}

func TestJoinIntoMessagesSplits(t *testing.T) {
	header := "<b>H</b>"
	// 10 блоков по ~1000 символов → должно разбиться на несколько сообщений.
	var blocks []string
	for i := 0; i < 10; i++ {
		blocks = append(blocks, "\n\n"+strings.Repeat("x", 1000))
	}
	msgs := joinIntoMessages(header, blocks)
	if len(msgs) < 2 {
		t.Errorf("ожидалось разбиение на >=2 сообщений, %d", len(msgs))
	}
	for i, m := range msgs {
		if len(m) > maxMessage {
			t.Errorf("сообщение %d длиннее лимита: %d > %d", i, len(m), maxMessage)
		}
		if !strings.HasPrefix(m, header) {
			t.Errorf("сообщение %d не начинается с header", i)
		}
	}
}
