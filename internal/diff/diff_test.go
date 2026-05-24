package diff

import (
	"testing"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

func base() model.Schedule {
	return model.Schedule{
		Weeks: []model.Week{
			{Name: "числитель", Days: []model.Day{
				{Weekday: "Понедельник"},
				{Weekday: "Вторник", Lessons: []model.Lesson{
					{Number: 2, TimeFrom: "09:50", TimeTo: "11:25", Subject: "Программирование",
						Type: "Лекция", Teachers: []string{"Иванов И.И."}, Room: "а.101"},
					{Number: 3, TimeFrom: "11:40", TimeTo: "13:15", Subject: "Математика",
						Type: "Практика", Teachers: []string{"Петров П.П."}, Room: "а.102"},
				}},
				{Weekday: "Среда"}, {Weekday: "Четверг"}, {Weekday: "Пятница"},
				{Weekday: "Суббота"}, {Weekday: "Воскресенье"},
			}},
			{Name: "знаменатель", Days: []model.Day{
				{Weekday: "Понедельник"},
				{Weekday: "Вторник"}, {Weekday: "Среда"}, {Weekday: "Четверг"},
				{Weekday: "Пятница"}, {Weekday: "Суббота"}, {Weekday: "Воскресенье"},
			}},
		},
	}
}

func TestNoChanges(t *testing.T) {
	a := base()
	b := base()
	if got := DiffSchedule(a, b); len(got) != 0 {
		t.Errorf("ожидалось 0 изменений, получено %d: %+v", len(got), got)
	}
}

func TestRemoveLesson(t *testing.T) {
	a := base()
	b := base()
	// Удаляем Математику во вторник.
	b.Weeks[0].Days[1].Lessons = b.Weeks[0].Days[1].Lessons[:1]

	changes := DiffSchedule(a, b)
	if len(changes) != 1 {
		t.Fatalf("ожидалось 1 изменение, получено %d", len(changes))
	}
	c := changes[0]
	if c.Kind != KindRemoved {
		t.Errorf("Kind = %s, want removed", c.Kind)
	}
	if c.Old.Subject != "Математика" {
		t.Errorf("Old.Subject = %q", c.Old.Subject)
	}
	if c.Weekday != "Вторник" {
		t.Errorf("Weekday = %q", c.Weekday)
	}
}

func TestAddLesson(t *testing.T) {
	a := base()
	b := base()
	b.Weeks[0].Days[1].Lessons = append(b.Weeks[0].Days[1].Lessons, model.Lesson{
		Number: 4, TimeFrom: "13:45", TimeTo: "15:20",
		Subject: "Физика", Teachers: []string{"Сидоров С.С."}, Room: "а.103",
	})
	changes := DiffSchedule(a, b)
	if len(changes) != 1 || changes[0].Kind != KindAdded || changes[0].New.Subject != "Физика" {
		t.Errorf("ожидался +Физика, получено %+v", changes)
	}
}

func TestChangeRoom(t *testing.T) {
	a := base()
	b := base()
	b.Weeks[0].Days[1].Lessons[0].Room = "а.999"
	changes := DiffSchedule(a, b)
	if len(changes) != 1 || changes[0].Kind != KindRoom {
		t.Fatalf("ожидался RoomChanged, получено %+v", changes)
	}
	if changes[0].Old.Room != "а.101" || changes[0].New.Room != "а.999" {
		t.Errorf("Old/New room = %q/%q", changes[0].Old.Room, changes[0].New.Room)
	}
}

func TestChangeTime(t *testing.T) {
	a := base()
	b := base()
	b.Weeks[0].Days[1].Lessons[1].TimeFrom = "12:00"
	b.Weeks[0].Days[1].Lessons[1].TimeTo = "13:35"
	changes := DiffSchedule(a, b)
	if len(changes) != 1 || changes[0].Kind != KindTime {
		t.Fatalf("ожидался TimeChanged, %+v", changes)
	}
}

func TestChangeTeacherOrderIgnored(t *testing.T) {
	a := base()
	b := base()
	// Два препода в обратном порядке — diff не должен срабатывать.
	a.Weeks[0].Days[1].Lessons[0].Teachers = []string{"Иванов И.И.", "Иванова И.И."}
	b.Weeks[0].Days[1].Lessons[0].Teachers = []string{"Иванова И.И.", "Иванов И.И."}
	if got := DiffSchedule(a, b); len(got) != 0 {
		t.Errorf("разный порядок преподов не должен считаться изменением: %+v", got)
	}
}

func TestChangeTeacherDifferent(t *testing.T) {
	a := base()
	b := base()
	b.Weeks[0].Days[1].Lessons[0].Teachers = []string{"НовыйПрепод Н.Н."}
	changes := DiffSchedule(a, b)
	if len(changes) != 1 || changes[0].Kind != KindTeacher {
		t.Fatalf("ожидался TeacherChanged, %+v", changes)
	}
}

func TestMultipleChangesSamLesson(t *testing.T) {
	// У одной пары сразу сменились и аудитория, и препод —
	// diff выдаёт ДВА отдельных Change.
	a := base()
	b := base()
	b.Weeks[0].Days[1].Lessons[0].Room = "а.999"
	b.Weeks[0].Days[1].Lessons[0].Teachers = []string{"Новый Н.Н."}
	changes := DiffSchedule(a, b)
	if len(changes) != 2 {
		t.Fatalf("ожидалось 2 изменения, получено %d", len(changes))
	}
	kinds := map[Kind]bool{}
	for _, c := range changes {
		kinds[c.Kind] = true
	}
	if !kinds[KindRoom] || !kinds[KindTeacher] {
		t.Errorf("ожидались room+teacher, получено %v", kinds)
	}
}
