// Package bot — Telegram-бот: команды, форматирование сообщений, фоновые
// обновления.
package bot

import (
	"fmt"
	"html"
	"strings"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

// maxMessage — безопасный размер сообщения Telegram (лимит 4096, берём с запасом
// под служебные символы и эмодзи).
const maxMessage = 3800

// FormatWeek формирует одно или несколько HTML-сообщений для одной недели
// расписания. Разбивает по дням, если в одно сообщение всё не помещается.
func FormatWeek(s model.Schedule, weekIdx int) []string {
	if weekIdx < 0 || weekIdx >= len(s.Weeks) {
		return []string{"<i>Нет данных за эту неделю</i>"}
	}
	week := s.Weeks[weekIdx]

	header := weekHeader(s, week)
	var dayBlocks []string
	for _, day := range week.Days {
		dayBlocks = append(dayBlocks, formatDay(s.Target.Type, day))
	}
	return joinIntoMessages(header, dayBlocks)
}

// FormatDay возвращает сообщение для одного дня указанной недели.
func FormatDay(s model.Schedule, weekIdx, dayIdx int) string {
	if weekIdx < 0 || weekIdx >= len(s.Weeks) {
		return "<i>Нет данных за эту неделю</i>"
	}
	week := s.Weeks[weekIdx]
	if dayIdx < 0 || dayIdx >= len(week.Days) {
		return "<i>Нет данных за этот день</i>"
	}
	return weekHeader(s, week) + formatDay(s.Target.Type, week.Days[dayIdx])
}

// FormatChangeIntro — короткое сообщение об изменении расписания. После него
// бот шлёт обычные сообщения недели(ь).
func FormatChangeIntro(s model.Schedule) string {
	label := "—"
	switch {
	case s.Target.Valid():
		label = s.Target.Label()
	case s.Title != "":
		label = s.Title
	}
	return fmt.Sprintf("📅 <b>Расписание изменилось</b>\n<i>%s</i>", html.EscapeString(label))
}

// weekHeader — шапка сообщения недели.
func weekHeader(s model.Schedule, week model.Week) string {
	var sb strings.Builder
	sb.WriteString("<b>Расписание")
	switch {
	case s.Target.Valid():
		sb.WriteString(" · ")
		sb.WriteString(html.EscapeString(s.Target.Label()))
	case s.Title != "":
		sb.WriteString(" · ")
		sb.WriteString(html.EscapeString(s.Title))
	}
	if week.Name != "" {
		sb.WriteString(" · ")
		sb.WriteString(html.EscapeString(week.Name))
	}
	sb.WriteString("</b>")
	if !s.FetchedAt.IsZero() {
		sb.WriteString("\n<i>Выгружено: ")
		sb.WriteString(s.FetchedAt.Local().Format("02.01.2006 15:04"))
		sb.WriteString("</i>")
	}
	return sb.String()
}

// formatDay — блок одного дня: заголовок + пары.
func formatDay(typ model.TargetType, day model.Day) string {
	var sb strings.Builder
	sb.WriteString("\n\n📅 <b>")
	sb.WriteString(html.EscapeString(day.Weekday))
	sb.WriteString("</b>")
	if len(day.Lessons) == 0 {
		sb.WriteString("\n  <i>нет занятий</i>")
		return sb.String()
	}
	for _, l := range day.Lessons {
		sb.WriteString("\n")
		sb.WriteString(formatLesson(typ, l))
	}
	return sb.String()
}

// formatLesson — одна пара.
func formatLesson(typ model.TargetType, l model.Lesson) string {
	var sb strings.Builder
	if l.Number > 0 {
		fmt.Fprintf(&sb, "%d. ", l.Number)
	}
	if tr := timeRange(l); tr != "" {
		sb.WriteString("<b>")
		sb.WriteString(html.EscapeString(tr))
		sb.WriteString("</b> ")
	}
	sb.WriteString(html.EscapeString(l.Subject))
	if l.Type != "" {
		sb.WriteString(" <i>(")
		sb.WriteString(html.EscapeString(l.Type))
		sb.WriteString(")</i>")
	}

	var details []string
	if len(l.Teachers) > 0 {
		details = append(details, "👤 "+html.EscapeString(strings.Join(l.Teachers, ", ")))
	}
	if l.Room != "" {
		details = append(details, "🚪 "+html.EscapeString(l.Room))
	}
	// Для расписания группы интересна подгруппа; для преподавателя/аудитории — группы.
	if typ == model.TypeStudent {
		if l.Subgroup != "" {
			details = append(details, html.EscapeString(l.Subgroup))
		}
	} else {
		if len(l.Groups) > 0 {
			details = append(details, "👥 "+html.EscapeString(strings.Join(l.Groups, ", ")))
		}
	}
	if len(details) > 0 {
		sb.WriteString("\n   ")
		sb.WriteString(strings.Join(details, " · "))
	}
	return sb.String()
}

// timeRange собирает диапазон времени пары.
func timeRange(l model.Lesson) string {
	switch {
	case l.TimeFrom != "" && l.TimeTo != "":
		return l.TimeFrom + "–" + l.TimeTo
	case l.TimeFrom != "":
		return l.TimeFrom
	default:
		return ""
	}
}

// joinIntoMessages соединяет header и блоки дней в одно или несколько сообщений
// длиной не более maxMessage. Каждое последующее сообщение тоже начинается
// заголовком — чтобы при прокрутке было понятно, чьё это расписание.
func joinIntoMessages(header string, blocks []string) []string {
	var out []string
	cur := header
	for _, b := range blocks {
		candidate := cur + b
		if len(candidate) > maxMessage && cur != header {
			out = append(out, cur)
			cur = header + b
		} else {
			cur = candidate
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
