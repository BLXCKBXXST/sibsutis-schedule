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
	}

	pages := map[string]*template.Template{}
	for _, name := range []string{"home", "schedule", "ambiguous", "error"} {
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

// staticHandler возвращает обработчик для embed-вшитой папки static/.
func staticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Printf("static sub: %v", err)
		return http.NotFoundHandler()
	}
	return http.FileServer(http.FS(sub))
}
