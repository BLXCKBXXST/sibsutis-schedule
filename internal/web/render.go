// Package web — HTTP-сервер расписания.
//
// Использует html/template из встроенной (embed) ФС, переиспользует
// schedule.Service для кэширования и singleflight'а.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

// renderer держит распарсенные шаблоны страниц.
// Каждая страница парсится отдельно вместе с base.html, чтобы свой блок
// "content" в каждом файле не перетирал чужой.
type renderer struct {
	pages map[string]*template.Template
}

func newRenderer() (*renderer, error) {
	funcs := template.FuncMap{
		"formatTime": func(t time.Time) string {
			if t.IsZero() {
				return "—"
			}
			return t.Local().Format("02.01.2006 15:04")
		},
		"capitalize": func(s string) string {
			r := []rune(s)
			if len(r) == 0 {
				return s
			}
			r[0] = []rune(strings.ToUpper(string(r[0])))[0]
			return string(r)
		},
		"join": func(sep string, parts []string) string { return strings.Join(parts, sep) },
		"urlType": func(t model.TargetType) string {
			if t == model.TypeStudent {
				return "group"
			}
			return string(t)
		},
		"isStudent": func(t model.TargetType) bool { return t == model.TypeStudent },
		"timeRange": func(l model.Lesson) string {
			switch {
			case l.TimeFrom != "" && l.TimeTo != "":
				return l.TimeFrom + "–" + l.TimeTo
			case l.TimeFrom != "":
				return l.TimeFrom
			default:
				return ""
			}
		},
		// lessonClass возвращает CSS-класс для строки пары: "is-now" для
		// идущей сейчас, "is-next" для ближайшей следующей, пусто иначе.
		// Сравнение идёт по слоту (день + TimeFrom), а не по индексу строки —
		// тогда все Lesson-ы одного слота (например, параллельные подгруппы)
		// получают одинаковую подсветку.
		"lessonClass": func(nowSlot, nextSlot *slotRef, wi, di int, timeFrom string) string {
			if nowSlot != nil && nowSlot.WeekIdx == wi && nowSlot.DayIdx == di && nowSlot.TimeFrom == timeFrom {
				return "is-now"
			}
			if nextSlot != nil && nextSlot.WeekIdx == wi && nextSlot.DayIdx == di && nextSlot.TimeFrom == timeFrom {
				return "is-next"
			}
			return ""
		},
		// safeURL помечает строку как уже-безопасный URL (template.URL).
		// Нужно для webcal:// — html/template не знает эту схему и
		// заменяет href на «#ZgotmplZ» из соображений безопасности.
		// Используется только с серверно-сгенерированными ссылками.
		"safeURL": func(s string) template.URL { return template.URL(s) },
		// weekRange форматирует семидневный диапазон с понедельника
		// start в виде «25–31 мая» или «28 мая – 3 июня».
		"weekRange": func(start time.Time) string {
			if start.IsZero() {
				return ""
			}
			end := start.AddDate(0, 0, 6)
			if start.Month() == end.Month() {
				return fmt.Sprintf("%d–%d %s", start.Day(), end.Day(), russianMonthGen(start.Month()))
			}
			return fmt.Sprintf("%d %s – %d %s",
				start.Day(), russianMonthGen(start.Month()),
				end.Day(), russianMonthGen(end.Month()))
		},
	}

	pages := map[string]*template.Template{}
	for _, name := range []string{"home", "schedule", "ambiguous", "error", "history", "diff"} {
		t, err := template.New("base.html").Funcs(funcs).ParseFS(templatesFS,
			"templates/base.html", "templates/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", name, err)
		}
		pages[name] = t
	}
	return &renderer{pages: pages}, nil
}

// render выполняет шаблон страницы name через base.html.
func (r *renderer) render(w http.ResponseWriter, status int, name string, data any) {
	t, ok := r.pages[name]
	if !ok {
		log.Printf("render: unknown page %q", name)
		http.Error(w, "internal: unknown template", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := t.ExecuteTemplate(w, "base.html", data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

// russianMonthGen возвращает название месяца в родительном падеже:
// «мая», «июня», «октября» — для конструкций «25 мая», «28 мая – 3 июня».
func russianMonthGen(m time.Month) string {
	switch m {
	case time.January:
		return "января"
	case time.February:
		return "февраля"
	case time.March:
		return "марта"
	case time.April:
		return "апреля"
	case time.May:
		return "мая"
	case time.June:
		return "июня"
	case time.July:
		return "июля"
	case time.August:
		return "августа"
	case time.September:
		return "сентября"
	case time.October:
		return "октября"
	case time.November:
		return "ноября"
	case time.December:
		return "декабря"
	}
	return ""
}

// staticHandler возвращает обработчик для embed-вшитой папки static/.
func staticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Printf("static sub: %v", err)
		return http.NotFoundHandler()
	}
	return http.FileServer(http.FS(sub))
}
