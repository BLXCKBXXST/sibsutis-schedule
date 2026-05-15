// Package render выводит расписание в терминал человекочитаемой таблицей.
package render

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

// Options управляет оформлением вывода.
type Options struct {
	FromCache   bool   // расписание взято из истории, а не выгружено только что
	CacheReason string // почему показана версия из истории (если FromCache)
}

const timeFormat = "02.01.2006 15:04"

// Schedule печатает расписание в w.
func Schedule(w io.Writer, s model.Schedule, opts Options) {
	printHeader(w, s, opts)

	if s.IsEmpty() {
		fmt.Fprintln(w, "\nПар не найдено — в расписании нет занятий (или страница пуста).")
		return
	}

	// Последняя колонка зависит от типа расписания: для группы интересна
	// подгруппа, для преподавателя/аудитории — какие группы на паре.
	lastCol := "Подгруппа"
	if s.Target.Type != model.TypeStudent {
		lastCol = "Группы"
	}

	for _, week := range s.Weeks {
		fmt.Fprintf(w, "\n══ %s ══\n", capitalize(week.Name))

		for _, day := range week.Days {
			fmt.Fprintf(w, "\n%s\n", day.Weekday)
			if len(day.Lessons) == 0 {
				fmt.Fprintln(w, "  — нет занятий")
				continue
			}
			printLessons(w, day.Lessons, lastCol, s.Target.Type)
		}
	}
}

// printHeader выводит шапку: target, время выгрузки или пометку о кэше.
func printHeader(w io.Writer, s model.Schedule, opts Options) {
	title := "Расписание"
	switch {
	case s.Target.Valid():
		title += " · " + s.Target.Label()
	case s.Title != "":
		title += " · " + s.Title
	}
	fmt.Fprintln(w, title)

	when := "—"
	if !s.FetchedAt.IsZero() {
		when = s.FetchedAt.Local().Format(timeFormat)
	}
	if opts.FromCache {
		reason := opts.CacheReason
		if reason == "" {
			reason = "сайт недоступен"
		}
		fmt.Fprintf(w, "⚠ %s — показано из истории, выгружено %s\n", reason, when)
	} else {
		fmt.Fprintf(w, "Выгружено: %s\n", when)
	}
}

// printLessons печатает пары одного дня выровненной таблицей.
func printLessons(w io.Writer, lessons []model.Lesson, lastCol string, typ model.TargetType) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "  №\tВремя\tПара\tТип\tПреподаватель\tАудитория\t%s\n", lastCol)
	for _, l := range lessons {
		num := ""
		if l.Number > 0 {
			num = fmt.Sprintf("%d", l.Number)
		}
		last := dash(l.Subgroup)
		if typ != model.TypeStudent {
			last = dash(strings.Join(l.Groups, ", "))
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			num, timeRange(l), dash(l.Subject), dash(l.Type),
			dash(strings.Join(l.Teachers, ", ")), dash(l.Room), last)
	}
	tw.Flush()
}

// timeRange собирает диапазон времени пары.
func timeRange(l model.Lesson) string {
	switch {
	case l.TimeFrom != "" && l.TimeTo != "":
		return l.TimeFrom + "–" + l.TimeTo
	case l.TimeFrom != "":
		return l.TimeFrom
	default:
		return "—"
	}
}

// dash заменяет пустую строку прочерком, чтобы колонки не «схлопывались».
func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// capitalize переводит первую руну строки в верхний регистр (корректно для кириллицы).
func capitalize(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	return string(r)
}

// optTime форматирует время или прочерк для нулевого значения.
func optTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format(timeFormat)
}

// VersionRow — данные одной строки списка версий. Совпадает по полям со
// store.VersionInfo, но render не зависит от пакета store.
type VersionRow struct {
	ID        string
	FetchedAt time.Time
	Title     string
	Lessons   int
}

// Versions печатает список сохранённых версий (для команды history по target'у).
func Versions(w io.Writer, list []VersionRow) {
	if len(list) == 0 {
		fmt.Fprintln(w, "Для этого target'а в истории нет ни одной версии.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tВыгружено\tПодпись\tПар")
	for _, r := range list {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", r.ID, optTime(r.FetchedAt), dash(r.Title), r.Lessons)
	}
	tw.Flush()
}

// TargetRow — данные одной строки сводки по target'у. Совпадает по полям со
// store.TargetSummary, но render не зависит от пакета store.
type TargetRow struct {
	Key       string
	Target    model.Target
	Versions  int
	LatestAt  time.Time
	LastCheck time.Time
	LastError string
}

// Targets печатает сводку по всем target'ам в истории (для команды history без флага).
func Targets(w io.Writer, rows []TargetRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "История пуста — расписание ещё ни разу не выгружалось.")
		fmt.Fprintln(w, "Запусти `sibsutis-schedule update` или `show --group/--teacher/--room <...>`.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "Target\tВерсий\tПоследняя выгрузка\tПоследняя проверка\tОшибка")
	for _, r := range rows {
		name := r.Target.Label()
		if !r.Target.Valid() {
			name = r.Key
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n",
			dash(name), r.Versions, optTime(r.LatestAt), optTime(r.LastCheck), dash(r.LastError))
	}
	tw.Flush()
}
