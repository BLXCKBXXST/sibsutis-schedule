package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/resolve"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/schedule"
)

// homeData — данные для main page.
type homeData struct {
	Title         string
	Notice        string
	DefaultTarget *model.Target
}

// scheduleData — данные для страницы расписания.
type scheduleData struct {
	Title        string
	Schedule     model.Schedule
	Target       model.Target
	FromCache    bool
	CacheReason  string
	Today        todayHint
	NowLesson    *lessonRef // пара, идущая прямо сейчас (или nil)
	NextLesson   *lessonRef // следующая пара по расписанию (или nil)
	NextLessonAt time.Time  // точное начало NextLesson (для live-таймера)
	ServerNow    time.Time  // момент рендера в зоне Asia/Krasnoyarsk
	// ShowWeek управляет тем, какие недели рендерить:
	//   0/1   — только числитель/знаменатель,
	//   -1    — обе подряд (старый layout).
	// По умолчанию (URL без ?week=) подставляется активная неделя из Today.
	ShowWeek int
}

// ambiguousData — данные для страницы «уточни запрос».
type ambiguousData struct {
	Title   string
	Target  model.Target
	Options []string
}

// errorData — данные для error.html.
type errorData struct {
	Title   string
	Message string
}

// urlTypeToTargetType мапит сегмент URL на тип target'а.
func urlTypeToTargetType(s string) (model.TargetType, bool) {
	switch s {
	case "group":
		return model.TypeStudent, true
	case "teacher":
		return model.TypeTeacher, true
	case "room":
		return model.TypeRoom, true
	default:
		return "", false
	}
}

// targetTypeToURL обратный маппинг.
func targetTypeToURL(t model.TargetType) string {
	if t == model.TypeStudent {
		return "group"
	}
	return string(t)
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	notice := r.URL.Query().Get("notice")
	w.Header().Set("Cache-Control", "public, max-age=60")
	s.render.render(w, http.StatusOK, "home", homeData{
		Title:         "Расписание SibSUTI",
		Notice:        notice,
		DefaultTarget: s.cfg.DefaultTarget,
	})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	typ := r.FormValue("type")
	q := strings.TrimSpace(r.FormValue("q"))

	if _, ok := urlTypeToTargetType(typ); !ok {
		s.redirectHome(w, r, "выбери group / teacher / room")
		return
	}
	if q == "" {
		s.redirectHome(w, r, "пустой запрос")
		return
	}
	http.Redirect(w, r, "/schedule/"+typ+"/"+url.PathEscape(q), http.StatusSeeOther)
}

func (s *Server) handleSchedule(w http.ResponseWriter, r *http.Request) {
	typ := r.PathValue("type")
	tt, ok := urlTypeToTargetType(typ)
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := r.PathValue("q")
	if q == "" {
		s.redirectHome(w, r, "пустой запрос")
		return
	}
	target := model.Target{Type: tt, Query: q}

	result, err := s.svc.Get(r.Context(), target, s.cfg.CacheFreshness)

	// Сетевая ошибка — пробуем фолбэк на последний снимок из истории.
	fromCache := false
	cacheReason := ""
	if err != nil &&
		!errors.Is(err, resolve.ErrNotFound) &&
		!errors.Is(err, resolve.ErrAmbiguous) {
		if sched, _, lerr := s.store.Latest(target.Key()); lerr == nil {
			result = schedule.Result{Schedule: sched, Source: schedule.SourceCache}
			err = nil
			fromCache = true
			cacheReason = "сайт недоступен"
		}
	}

	if errors.Is(err, resolve.ErrNotFound) {
		s.render.render(w, http.StatusNotFound, "error", errorData{
			Title:   "Ничего не найдено",
			Message: err.Error(),
		})
		return
	}
	if errors.Is(err, resolve.ErrAmbiguous) {
		s.render.render(w, http.StatusOK, "ambiguous", ambiguousData{
			Title:   "Уточни запрос",
			Target:  target,
			Options: parseAmbiguousOptions(err.Error()),
		})
		return
	}
	if err != nil {
		s.render.render(w, http.StatusBadGateway, "error", errorData{
			Title:   "Сайт недоступен",
			Message: err.Error(),
		})
		return
	}

	if result.Source == schedule.SourceCache && !fromCache {
		fromCache = true
		cacheReason = "из кэша"
	}

	now := time.Now().In(krskLocation)
	hl := highlights(result.Schedule, now)
	today := computeTodayHint(result.Schedule, now)
	showWeek := parseShowWeek(r.URL.Query().Get("week"), today)
	w.Header().Set("Cache-Control", "public, max-age=300")
	s.render.render(w, http.StatusOK, "schedule", scheduleData{
		Title:        target.Label() + " — расписание",
		Schedule:     result.Schedule,
		Target:       target,
		FromCache:    fromCache,
		CacheReason:  cacheReason,
		Today:        today,
		NowLesson:    hl.Now,
		NextLesson:   hl.Next,
		NextLessonAt: hl.NextAt,
		ServerNow:    now,
		ShowWeek:     showWeek,
	})
}

// parseShowWeek разбирает ?week=. Пустое или невалидное значение → активная
// неделя из today (или -1, если сегодня не покрыто).
func parseShowWeek(raw string, today todayHint) int {
	switch raw {
	case "0":
		return 0
	case "1":
		return 1
	case "all":
		return -1
	}
	if today.Found {
		return today.WeekIdx
	}
	return -1
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "OK")
}

// redirectHome — редирект на / с notice в query.
func (s *Server) redirectHome(w http.ResponseWriter, r *http.Request, notice string) {
	http.Redirect(w, r, "/?notice="+url.QueryEscape(notice), http.StatusSeeOther)
}

// parseAmbiguousOptions вытаскивает варианты из текста resolve.ErrAmbiguous.
// Формат сообщения от resolve.go: "...:\n  - вариант 1\n  - вариант 2\n  …".
func parseAmbiguousOptions(s string) []string {
	var opts []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "- "); ok {
			opts = append(opts, after)
		}
	}
	return opts
}
