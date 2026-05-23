// Package model описывает структуру расписания, которую возвращает парсер
// и которая сохраняется в истории версий.
//
// Сайт отдаёт расписание двухнедельным циклом: неделя «числитель» и неделя
// «знаменатель», в каждой 7 дней.
package model

import "time"

// Lesson — одна пара (занятие одной подгруппы в одном слоте времени).
//
// Сайт my.sibsutis.ru не хранит привычную «дату пары»: позиция в двухнедельном
// цикле кодируется только индексом дня (числитель/знаменатель × 1..7), а в
// DateBegin/DateEnd передаются лишь часы и минуты (с датой-заглушкой
// 0001-01-01). Зато для каждого подзанятия есть PROJECT_DATES — массив
// конкретных календарных дат, на которые пара назначена в семестре. Они и
// попадают в Dates: по сути «эта пара пройдёт в эти дни» (обычно 4–6 дат за
// семестр на одно занятие).
type Lesson struct {
	Number   int         `json:"number"`             // порядковый номер пары в дне (по слоту времени)
	Dates    []time.Time `json:"dates,omitempty"`    // все календарные даты проведения этой пары (нулевой в старых версиях истории)
	TimeFrom string      `json:"time_from"`          // "09:50"
	TimeTo   string      `json:"time_to"`            // "11:25"
	Subject  string      `json:"subject"`            // дисциплина
	Type     string      `json:"type,omitempty"`     // вид занятия (лекция/практика/...)
	Teachers []string    `json:"teachers,omitempty"` // преподаватели
	Room     string      `json:"room,omitempty"`     // аудитория
	Groups   []string    `json:"groups,omitempty"`   // группы (важно для расписания преподавателя/аудитории)
	Subgroup string      `json:"subgroup,omitempty"` // подгруппа, если занятие только для неё
}

// Day — один день недели с парами.
type Day struct {
	Weekday string   `json:"weekday"` // "Понедельник"
	Lessons []Lesson `json:"lessons"`
}

// Week — одна неделя двухнедельного цикла.
type Week struct {
	Name string `json:"name"` // "числитель" / "знаменатель"
	Days []Day  `json:"days"`
}

// Schedule — расписание целиком, единица хранения в истории.
type Schedule struct {
	Target    Target    `json:"target"`          // чьё расписание (группа/преподаватель/аудитория)
	Title     string    `json:"title,omitempty"` // подпись с сайта: название группы / ФИО / аудитория
	FetchedAt time.Time `json:"fetched_at"`      // момент успешной выгрузки с сайта
	Weeks     []Week    `json:"weeks"`
	RawHTML   string    `json:"raw_html,omitempty"` // сырой HTML — на случай неполного разбора
}

// IsEmpty сообщает, что в расписании нет ни одной пары.
func (s Schedule) IsEmpty() bool {
	for _, w := range s.Weeks {
		for _, d := range w.Days {
			if len(d.Lessons) > 0 {
				return false
			}
		}
	}
	return true
}

// LessonCount возвращает суммарное число пар во всех неделях.
func (s Schedule) LessonCount() int {
	n := 0
	for _, w := range s.Weeks {
		for _, d := range w.Days {
			n += len(d.Lessons)
		}
	}
	return n
}

// dateKey нормализует момент до начала суток в его собственной локации.
// Используется как ключ сравнения с Lesson.Dates (у которых время = 00:00 UTC,
// см. parse.go: PROJECT_DATES парсятся без часов).
func dateKey(t time.Time) string { return t.Format("2006-01-02") }

// LessonsOn возвращает пары, проведение которых выпадает на указанную дату,
// отсортированные по TimeFrom. Время суток у date игнорируется — сверяется
// только календарный день в локации date. Если расписание не содержит пар
// на эту дату — возвращается nil.
func (s Schedule) LessonsOn(date time.Time) []Lesson {
	key := dateKey(date)
	var out []Lesson
	for _, w := range s.Weeks {
		for _, d := range w.Days {
			for _, l := range d.Lessons {
				for _, ld := range l.Dates {
					if dateKey(ld) == key {
						out = append(out, l)
						break
					}
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	// Сортировка по началу пары; равные TimeFrom — по Number.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			if out[j-1].TimeFrom < out[j].TimeFrom {
				break
			}
			if out[j-1].TimeFrom == out[j].TimeFrom && out[j-1].Number <= out[j].Number {
				break
			}
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// WeekIndexFor возвращает индекс недели (0 — числитель, 1 — знаменатель),
// у которой пары назначены на указанную дату. Если ни одна пара не выпадает
// на эту дату — ok=false.
func (s Schedule) WeekIndexFor(date time.Time) (idx int, ok bool) {
	key := dateKey(date)
	for wi, w := range s.Weeks {
		for _, d := range w.Days {
			for _, l := range d.Lessons {
				for _, ld := range l.Dates {
					if dateKey(ld) == key {
						return wi, true
					}
				}
			}
		}
	}
	return 0, false
}

// NextLessonAfter возвращает первую пару, начало которой строго позже t,
// вместе с моментом её начала (в локации t). Если расписание пусто или все
// пары уже прошли — ok=false. Поиск ведётся по всем Lesson.Dates всех недель.
func (s Schedule) NextLessonAfter(t time.Time) (lesson Lesson, when time.Time, ok bool) {
	loc := t.Location()
	for _, w := range s.Weeks {
		for _, d := range w.Days {
			for _, l := range d.Lessons {
				start, parseErr := parseHHMM(l.TimeFrom)
				if parseErr {
					continue
				}
				for _, ld := range l.Dates {
					begin := time.Date(ld.Year(), ld.Month(), ld.Day(), start.h, start.m, 0, 0, loc)
					if !begin.After(t) {
						continue
					}
					if !ok || begin.Before(when) {
						lesson, when, ok = l, begin, true
					}
				}
			}
		}
	}
	return
}

// StudyDates возвращает отсортированный список уникальных календарных дат,
// на которые расписание назначает хотя бы одну пару.
func (s Schedule) StudyDates() []time.Time {
	seen := make(map[string]time.Time)
	for _, w := range s.Weeks {
		for _, d := range w.Days {
			for _, l := range d.Lessons {
				for _, ld := range l.Dates {
					key := dateKey(ld)
					if _, has := seen[key]; !has {
						seen[key] = ld
					}
				}
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]time.Time, 0, len(seen))
	for _, t := range seen {
		out = append(out, t)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Before(out[j-1]); j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

type hhmm struct{ h, m int }

// parseHHMM разбирает строку "ЧЧ:ММ". При формате ошибочно возвращает
// (zero, true) — вызывающая сторона должна пропустить такую пару.
func parseHHMM(s string) (hhmm, bool) {
	if len(s) != 5 || s[2] != ':' {
		return hhmm{}, true
	}
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	m := int(s[3]-'0')*10 + int(s[4]-'0')
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return hhmm{}, true
	}
	return hhmm{h, m}, false
}
