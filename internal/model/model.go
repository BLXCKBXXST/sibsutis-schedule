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
// 0001-01-01). Тип календарной недели (числитель/знаменатель) для конкретной
// даты считается формулой WeekParity, повторяющей JS-логику самого сайта.
type Lesson struct {
	Number int `json:"number"` // порядковый номер пары в дне (по слоту времени)
	// Dates — даты сдачи семестрового проекта по дисциплине из поля
	// PROJECT_DATES страницы расписания. На проде у всех пар одной группы
	// этот массив одинаковый: это НЕ даты проведения данной пары, а общий
	// календарь сдачи проекта. Поле не должно использоваться для определения
	// «сегодня / следующая пара» — для этого есть WeekParity. Хранится в
	// JSON для совместимости со старой историей и потенциального UI
	// «проектные сроки».
	Dates    []time.Time `json:"dates,omitempty"`
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

