// Package ics рендерит расписание в формат iCalendar (RFC 5545) для импорта
// в Google Calendar / Apple Calendar / Outlook / любой нативный календарь.
//
// PROJECT_DATES сайта не подходит для построения дат пар (см. parse.go и
// комментарий у Lesson.Dates). Поэтому каждое занятие рендерится как
// событие с правилом повторения «каждые 2 недели» (RRULE) от якорной даты
// — первого ближайшего будущего проведения этой пары, определённого по
// WeekParity. UNTIL ограничивает повторения, чтобы пары не уходили в
// бесконечность за пределы семестра.
package ics

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

// Options управляет рендерингом календаря.
type Options struct {
	// Anchor — момент, с которого ищется первое проведение каждой пары.
	// Используется для расчёта DTSTART; обычно — time.Now().
	Anchor time.Time
	// Until — крайний срок повторений RRULE. По умолчанию — Anchor + 180 дней
	// (примерно один семестр).
	Until time.Time
	// Loc — часовой пояс, в котором рендерятся DTSTART/DTEND. Должен
	// соответствовать тому, чему университет читает расписание.
	Loc *time.Location
	// CalendarName — DISPLAY-имя календаря, видно в клиенте.
	CalendarName string
	// ProductID — заполняется в PRODID; для отладки и идентификации источника.
	ProductID string
}

// defaults подставляет нулевые поля Options разумными дефолтами.
func (o Options) defaults() Options {
	if o.Loc == nil {
		o.Loc = krskLoc()
	}
	if o.Anchor.IsZero() {
		o.Anchor = time.Now().In(o.Loc)
	}
	if o.Until.IsZero() {
		o.Until = o.Anchor.AddDate(0, 6, 0)
	}
	if o.CalendarName == "" {
		o.CalendarName = "SibSUTI"
	}
	if o.ProductID == "" {
		o.ProductID = "-//sibsutis-schedule//RU"
	}
	return o
}

// krskLoc — Asia/Krasnoyarsk с фолбэком на FixedZone, если tzdata недоступна.
func krskLoc() *time.Location {
	if loc, err := time.LoadLocation("Asia/Krasnoyarsk"); err == nil {
		return loc
	}
	return time.FixedZone("KRSK", 7*3600)
}

// RenderSchedule превращает Schedule в байты .ics. Никаких сетевых вызовов
// и побочных эффектов — чистая функция.
func RenderSchedule(s model.Schedule, opts Options) []byte {
	opts = opts.defaults()
	now := time.Now().UTC()
	dtstamp := formatICSDateTimeUTC(now)

	var b strings.Builder
	w := newICSWriter(&b)
	w.line("BEGIN:VCALENDAR")
	w.line("VERSION:2.0")
	w.line("PRODID:" + opts.ProductID)
	w.line("CALSCALE:GREGORIAN")
	w.line("METHOD:PUBLISH")
	w.escapedLine("X-WR-CALNAME", opts.CalendarName+" — "+s.Target.Label())
	w.escapedLine("X-WR-TIMEZONE", opts.Loc.String())

	writeVTimezone(w, opts.Loc)

	until := formatICSDateTimeUTC(opts.Until.UTC())

	for wi, week := range s.Weeks {
		for di, day := range week.Days {
			for _, l := range day.Lessons {
				start, end, ok := firstOccurrence(wi, di, l, opts.Anchor, opts.Loc)
				if !ok {
					continue
				}
				uid := lessonUID(s.Target.Key(), wi, di, l)
				w.line("BEGIN:VEVENT")
				w.line("UID:" + uid)
				w.line("DTSTAMP:" + dtstamp)
				w.line("DTSTART;TZID=" + opts.Loc.String() + ":" + formatICSDateTimeLocal(start))
				w.line("DTEND;TZID=" + opts.Loc.String() + ":" + formatICSDateTimeLocal(end))
				w.line("RRULE:FREQ=WEEKLY;INTERVAL=2;UNTIL=" + until)
				w.escapedLine("SUMMARY", lessonSummary(l))
				if loc := strings.TrimSpace(l.Room); loc != "" {
					w.escapedLine("LOCATION", loc)
				}
				if desc := lessonDescription(l); desc != "" {
					w.escapedLine("DESCRIPTION", desc)
				}
				w.line("END:VEVENT")
			}
		}
	}

	w.line("END:VCALENDAR")
	return []byte(b.String())
}

// firstOccurrence находит ближайшее будущее проведение пары (wi, di) после
// anchor: дату, на которой WeekParity и weekday совпадают с (wi, di),
// плюс часы/минуты из l.TimeFrom/TimeTo. Если у l нет валидного времени —
// возвращает ok=false.
func firstOccurrence(wi, di int, l model.Lesson, anchor time.Time, loc *time.Location) (start, end time.Time, ok bool) {
	from, ferr := parseHM(l.TimeFrom)
	to, terr := parseHM(l.TimeTo)
	if ferr || terr {
		return time.Time{}, time.Time{}, false
	}
	anchor = anchor.In(loc)
	day := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, loc)
	for off := 0; off < 14; off++ {
		d := day.AddDate(0, 0, off)
		if model.WeekParity(d, time.Time{}) != wi {
			continue
		}
		if weekdayIndex(d) != di {
			continue
		}
		begin := time.Date(d.Year(), d.Month(), d.Day(), from.h, from.m, 0, 0, loc)
		endT := time.Date(d.Year(), d.Month(), d.Day(), to.h, to.m, 0, 0, loc)
		return begin, endT, true
	}
	return time.Time{}, time.Time{}, false
}

// weekdayIndex — 0=Пн, 6=Вс (та же конвенция, что в model и web).
func weekdayIndex(t time.Time) int {
	wd := int(t.Weekday())
	if wd == 0 {
		return 6
	}
	return wd - 1
}

type hm struct{ h, m int }

func parseHM(s string) (hm, bool) {
	if len(s) != 5 || s[2] != ':' {
		return hm{}, true
	}
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	mn := int(s[3]-'0')*10 + int(s[4]-'0')
	if h < 0 || h > 23 || mn < 0 || mn > 59 {
		return hm{}, true
	}
	return hm{h, mn}, false
}

// lessonSummary — короткий заголовок события: дисциплина (плюс тип в скобках).
func lessonSummary(l model.Lesson) string {
	subj := strings.TrimSpace(l.Subject)
	t := strings.TrimSpace(l.Type)
	if t == "" {
		return subj
	}
	return subj + " (" + t + ")"
}

// lessonDescription — расширенное описание: преподаватели, подгруппа.
func lessonDescription(l model.Lesson) string {
	var parts []string
	if t := strings.Join(l.Teachers, ", "); t != "" {
		parts = append(parts, t)
	}
	if sg := strings.TrimSpace(l.Subgroup); sg != "" {
		parts = append(parts, "Подгруппа: "+sg)
	}
	return strings.Join(parts, "\n")
}

// lessonUID — стабильный идентификатор события, чтобы повторные импорты
// обновляли запись, а не плодили дубликаты. Меняется при переименовании
// дисциплины или смене подгруппы; не меняется при смене аудитории/препода.
func lessonUID(targetKey string, wi, di int, l model.Lesson) string {
	h := sha1.New()
	fmt.Fprintf(h, "%s|%d|%d|%d|%s|%s", targetKey, wi, di, l.Number, l.Subject, l.Subgroup)
	return hex.EncodeToString(h.Sum(nil)) + "@sibsutis"
}

// writeVTimezone выводит блок VTIMEZONE для Asia/Krasnoyarsk (или другого
// постоянного смещения). В Красноярске DST не используется, поэтому достаточно
// одного STANDARD-блока.
func writeVTimezone(w *icsWriter, loc *time.Location) {
	// Берём текущее смещение в loc — оно стабильное.
	_, offsetSec := time.Now().In(loc).Zone()
	offset := formatTZOffset(offsetSec)
	w.line("BEGIN:VTIMEZONE")
	w.line("TZID:" + loc.String())
	w.line("BEGIN:STANDARD")
	w.line("DTSTART:19700101T000000")
	w.line("TZOFFSETFROM:" + offset)
	w.line("TZOFFSETTO:" + offset)
	w.line("TZNAME:" + loc.String())
	w.line("END:STANDARD")
	w.line("END:VTIMEZONE")
}

func formatTZOffset(secs int) string {
	sign := "+"
	if secs < 0 {
		sign = "-"
		secs = -secs
	}
	h := secs / 3600
	m := (secs % 3600) / 60
	return fmt.Sprintf("%s%02d%02d", sign, h, m)
}

func formatICSDateTimeUTC(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

func formatICSDateTimeLocal(t time.Time) string {
	return t.Format("20060102T150405")
}

// --- ICS line writing with CRLF and folding -----------------------------

type icsWriter struct {
	b *strings.Builder
}

func newICSWriter(b *strings.Builder) *icsWriter { return &icsWriter{b: b} }

// line добавляет полностью готовую строку с CRLF (без экранирования).
func (w *icsWriter) line(s string) {
	w.writeFolded(s)
	w.b.WriteString("\r\n")
}

// escapedLine добавляет "KEY:value" — экранирует value по правилам RFC 5545
// (запятая, точка с запятой, бэкслэш, перенос) и сворачивает длинные строки.
func (w *icsWriter) escapedLine(key, value string) {
	w.writeFolded(key + ":" + escapeICSText(value))
	w.b.WriteString("\r\n")
}

// writeFolded реализует line-folding по RFC 5545 §3.1: строки длиннее 75
// октетов разбиваются на куски и продолжаются на следующей строке с пробелом
// в начале. Считаем длину по байтам — Go-строка UTF-8, что совместимо.
func (w *icsWriter) writeFolded(s string) {
	const max = 75
	if len(s) <= max {
		w.b.WriteString(s)
		return
	}
	// Простая реализация — режем по байтам, не разрывая UTF-8 рунами.
	i := 0
	for i < len(s) {
		end := i + max
		if end > len(s) {
			end = len(s)
		} else {
			// Не рвём посередине UTF-8 рунa: отступаем назад, пока не будем
			// в начале рунa (по правилу первого байта).
			for end > i && (s[end]&0xC0) == 0x80 {
				end--
			}
		}
		if i > 0 {
			w.b.WriteString("\r\n ")
		}
		w.b.WriteString(s[i:end])
		i = end
	}
}

// escapeICSText экранирует значение текстового поля iCalendar.
// Правила: \ → \\, ; → \;, , → \,, перевод строки → \n.
func escapeICSText(s string) string {
	r := strings.NewReplacer(
		"\\", "\\\\",
		";", "\\;",
		",", "\\,",
		"\r\n", "\\n",
		"\n", "\\n",
		"\r", "\\n",
	)
	return r.Replace(s)
}
