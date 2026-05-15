// Package parse разбирает страницу расписания my.sibsutis.ru в model.Schedule.
//
// Расписание не лежит в HTML напрямую — страница встраивает его как
// JavaScript-переменные days[1]..days[14] (JSON-строки в одинарных кавычках) и
// рендерит на клиенте. Парсер вытаскивает эти JSON-блоки и разбирает их:
// days[1..7] — неделя «числитель», days[8..14] — неделя «знаменатель»,
// внутри каждого дня — слоты времени (ScheduleCell) с занятиями (Subgroup).
package parse

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

// ErrNoScheduleData означает, что на странице не найдены данные расписания
// (страница выбора группы, ошибка сайта или изменилась вёрстка).
var ErrNoScheduleData = errors.New("на странице нет данных расписания")

// daysEntryRe вытаскивает присваивания вида: days[7] = '{...json...}'.
// \bdays — граница слова, чтобы не цеплять fact_schedule_days / exam_schedule_days.
var daysEntryRe = regexp.MustCompile(`\bdays\[(\d{1,2})\]\s*=\s*'(.+)'`)

// timeRe вытаскивает время ЧЧ:ММ из строки даты вида "0001-01-01T09:50:00".
var timeRe = regexp.MustCompile(`(\d{1,2}):(\d{2})`)

var (
	weekdayNames = []string{"Понедельник", "Вторник", "Среда", "Четверг", "Пятница", "Суббота", "Воскресенье"}
	weekNames    = []string{"числитель", "знаменатель"}
)

// ParseSchedule разбирает HTML страницы расписания. RawHTML сохраняется всегда.
// Если данных расписания на странице нет — возвращает ErrNoScheduleData.
func ParseSchedule(html string) (model.Schedule, error) {
	sched := model.Schedule{RawHTML: html}

	byIdx := extractDays(html)
	if len(byIdx) == 0 {
		return sched, ErrNoScheduleData
	}

	// Двухнедельный цикл: 2 недели по 7 дней. days[1..7] — числитель,
	// days[8..14] — знаменатель; день недели = (idx-1) % 7.
	sched.Weeks = make([]model.Week, 0, 2)
	for wk := 0; wk < 2; wk++ {
		week := model.Week{Name: weekNames[wk]}
		for dow := 0; dow < 7; dow++ {
			day := model.Day{Weekday: weekdayNames[dow]}
			if rd, ok := byIdx[wk*7+dow+1]; ok {
				day.Lessons = lessonsFromCells(rd.ScheduleCell)
			}
			week.Days = append(week.Days, day)
		}
		sched.Weeks = append(sched.Weeks, week)
	}
	return sched, nil
}

// extractDays находит и разбирает все блоки days[1..14].
func extractDays(html string) map[int]rawDay {
	byIdx := make(map[int]rawDay)
	for _, m := range daysEntryRe.FindAllStringSubmatch(html, -1) {
		idx, err := strconv.Atoi(m[1])
		if err != nil || idx < 1 || idx > 14 {
			continue
		}
		raw := []byte(m[2])

		var rd rawDay
		if err := json.Unmarshal(raw, &rd); err != nil {
			// Возможно экранирование одинарных кавычек в JS-строке — снимаем и пробуем ещё раз.
			if err2 := json.Unmarshal(bytes.ReplaceAll(raw, []byte(`\'`), []byte(`'`)), &rd); err2 != nil {
				continue // день не разобрался — пропускаем, остальные не теряем
			}
		}
		byIdx[idx] = rd
	}
	return byIdx
}

// lessonsFromCells разворачивает слоты времени дня в плоский список пар.
func lessonsFromCells(cells []rawCell) []model.Lesson {
	var lessons []model.Lesson
	for i, c := range cells {
		from, to := hhmm(c.DateBegin), hhmm(c.DateEnd)
		for _, it := range c.Subgroup {
			subject := strings.TrimSpace(it.Discipline)
			if subject == "" {
				continue // пустой слот без занятия
			}
			lessons = append(lessons, model.Lesson{
				Number:   i + 1,
				TimeFrom: from,
				TimeTo:   to,
				Subject:  subject,
				Type:     strings.TrimSpace(it.TypeLesson),
				Teachers: it.Teacher.clean(),
				Room:     strings.TrimSpace(it.Classroom),
				Groups:   it.Group.clean(),
				Subgroup: strings.TrimSpace(it.Subgroup),
			})
		}
	}
	return lessons
}

// hhmm извлекает время ЧЧ:ММ из строки даты-времени.
func hhmm(s string) string {
	m := timeRe.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	h := m[1]
	if len(h) == 1 {
		h = "0" + h
	}
	return h + ":" + m[2]
}

// --- структуры JSON, который встроен в страницу ---

type rawDay struct {
	ScheduleCell []rawCell `json:"ScheduleCell"`
}

type rawCell struct {
	DateBegin string    `json:"DateBegin"`
	DateEnd   string    `json:"DateEnd"`
	Subgroup  []rawItem `json:"Subgroup"`
}

type rawItem struct {
	Discipline string      `json:"DISCIPLINE"`
	TypeLesson string      `json:"TYPE_LESSON"`
	Teacher    flexStrings `json:"TEACHER"`
	Classroom  string      `json:"CLASSROOM"`
	Group      flexStrings `json:"GROUP"`
	Subgroup   string      `json:"SUBGROUP"`
	WeekDay    string      `json:"WEEK_DAY"`
}

// flexStrings разбирает поле, которое сайт отдаёт то массивом строк, то одной
// строкой, то null — и приводит к срезу строк.
type flexStrings []string

func (f *flexStrings) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		*f = nil
		return nil
	}
	if data[0] == '[' {
		var arr []string
		if err := json.Unmarshal(data, &arr); err != nil {
			return err
		}
		*f = arr
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*f = []string{s}
	return nil
}

// clean убирает пустые элементы и обрезает пробелы.
func (f flexStrings) clean() []string {
	var out []string
	for _, s := range f {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}
