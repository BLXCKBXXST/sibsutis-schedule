package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
	DefaultTarget *model.Target // фиксированный target из config.txt
	MyTarget      *model.Target // запомненный из cookie my_target (если задан)
}

// myTargetCookie — имя cookie, в которой запоминается последний просмотренный
// target. Срок жизни — 1 год, SameSite=Lax, HttpOnly (фронту читать незачем).
const myTargetCookie = "my_target"

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
	// WeekStarts[wi] — понедельник ближайшей календарной недели,
	// соответствующей Weeks[wi]. Используется для рендера дат в табах.
	WeekStarts [2]time.Time
	// WeekOrder — порядок отображения недель в табах: первой идёт
	// текущая неделя, второй — следующая.
	WeekOrder [2]int
	// CurrentWeek — индекс текущей по календарю недели (нужен шаблону,
	// чтобы пометить таб «сегодня» независимо от выбранной ShowWeek).
	CurrentWeek int
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
	// Куки персональные — кэширование на дороге между пользователями
	// испортит «Моё расписание». Сейчас сайт работает без CDN, поэтому
	// просто запрещаем shared-кэш.
	w.Header().Set("Cache-Control", "private, max-age=60")
	s.render.render(w, http.StatusOK, "home", homeData{
		Title:         "Расписание SibSUTI",
		Notice:        notice,
		DefaultTarget: s.cfg.DefaultTarget,
		MyTarget:      readMyTargetCookie(r, s.cfg.DefaultTarget),
	})
}

// readMyTargetCookie возвращает запомненный из cookie target.
// Cookie с тем же target, что и DefaultTarget — игнорируется (показывать одну
// и ту же ссылку дважды бессмысленно). Невалидное содержимое — nil без ошибки.
func readMyTargetCookie(r *http.Request, def *model.Target) *model.Target {
	c, err := r.Cookie(myTargetCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	typ, q, ok := strings.Cut(c.Value, "/")
	if !ok {
		return nil
	}
	tt, ok := urlTypeToTargetType(typ)
	if !ok {
		return nil
	}
	query, err := url.PathUnescape(q)
	if err != nil || strings.TrimSpace(query) == "" {
		return nil
	}
	t := &model.Target{Type: tt, Query: query}
	if def != nil && def.Type == t.Type && strings.EqualFold(def.Query, t.Query) {
		return nil
	}
	return t
}

// writeMyTargetCookie запоминает target на 1 год. Cookie HttpOnly+SameSite=Lax,
// Secure ставится только когда запрос пришёл по HTTPS — за Caddy это видно
// через X-Forwarded-Proto / r.TLS.
func writeMyTargetCookie(w http.ResponseWriter, r *http.Request, t model.Target) {
	value := targetTypeToURL(t.Type) + "/" + url.PathEscape(t.Query)
	http.SetCookie(w, &http.Cookie{
		Name:     myTargetCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   60 * 60 * 24 * 365,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
	})
}

// clearMyTargetCookie сбрасывает cookie — MaxAge=-1.
func clearMyTargetCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     myTargetCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
	})
}

// isHTTPS возвращает true, если запрос фактически пришёл по https — учитывая
// reverse-proxy (Caddy ставит X-Forwarded-Proto).
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
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

	// Запомнили выбор пользователя — главная теперь покажет ссылку
	// «Моё расписание (запомнено)».
	writeMyTargetCookie(w, r, target)

	now := time.Now().In(krskLocation)
	hl := highlights(result.Schedule, now)
	today := computeTodayHint(result.Schedule, now)
	showWeek := parseShowWeek(r.URL.Query().Get("week"), today)
	starts, calendarParity := weekStarts(now)
	// «Сейчас» в табах = неделя ближайшего учебного дня. На выходных это
	// будущая неделя (понедельник через 1-2 дня), не уходящая ISO-неделя.
	current := calendarParity
	if today.Found {
		current = today.WeekIdx
	}
	order := [2]int{current, 1 - current}
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
		WeekStarts:   starts,
		WeekOrder:    order,
		CurrentWeek:  current,
	})
}

// weekStarts возвращает понедельники двух ближайших календарных недель и
// индекс той, что соответствует «сейчас». starts[wi] — понедельник недели
// чётности wi; current — wi сегодняшней недели (0=числитель, 1=знаменатель).
func weekStarts(now time.Time) (starts [2]time.Time, current int) {
	now = now.In(krskLocation)
	thisMon := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, krskLocation)
	wd := int(thisMon.Weekday())
	if wd == 0 {
		wd = 7
	}
	thisMon = thisMon.AddDate(0, 0, -(wd - 1))
	current = model.WeekParity(thisMon, time.Time{})
	starts[current] = thisMon
	starts[1-current] = thisMon.AddDate(0, 0, 7)
	return starts, current
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

// handleForget стирает cookie my_target и редиректит на главную. Только POST,
// чтобы случайные ссылки/префетчи не сбрасывали выбор.
func (s *Server) handleForget(w http.ResponseWriter, r *http.Request) {
	clearMyTargetCookie(w, r)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleSuggest отдаёт JSON-список вариантов для автокомплита поиска.
// GET /api/suggest?type=group|teacher|room&q=<подстрока>. Запросы короче
// suggestMinQ возвращают пустой массив без обращения к сайту. Сетевые
// ошибки — тоже пустой массив (фронт не должен лопаться от падения
// my.sibsutis.ru), факт ошибки попадает в лог.
func (s *Server) handleSuggest(w http.ResponseWriter, r *http.Request) {
	typ := r.URL.Query().Get("type")
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	if _, ok := urlTypeToTargetType(typ); !ok || len([]rune(q)) < suggestMinQ {
		writeJSON(w, []suggestItem{})
		return
	}

	items, err := s.suggester.suggest(typ, q)
	if err != nil {
		log.Printf("suggest %s %q: %v", typ, q, err)
		writeJSON(w, []suggestItem{})
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, items)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
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
