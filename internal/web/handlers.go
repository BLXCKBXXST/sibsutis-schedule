package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/diff"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/notify"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/resolve"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/schedule"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/store"
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
	NowSlot      *slotRef  // слот, идущий прямо сейчас (или nil)
	NextSlot     *slotRef  // следующий слот по расписанию (или nil)
	NextLessonAt time.Time // точное начало NextSlot (для live-таймера)
	ServerNow time.Time // момент рендера в зоне Asia/Krasnoyarsk
	// View — выбранный режим отображения:
	//   "day"  — два ближайших учебных дня (сегодня + следующий),
	//   "week" — одна неделя целиком,
	//   "pick" — конкретный выбранный день + мини-календарь месяца.
	View string
	// RenderDays — дни, которые шаблон рендерит карточками. В day-режиме
	// 1-2 дня; в week-режиме — до 7; в pick-режиме — ровно 1.
	RenderDays []dayRef
	// WeekStarts[wi] — понедельник ближайшей календарной недели,
	// соответствующей Weeks[wi]. Используется в dayDate и т.п.
	WeekStarts [2]time.Time
	// SelectedDay — выбранный день в pick/week-режиме (или сегодня).
	SelectedDay time.Time
	// MonthLabel и MonthGrid — данные для мини-календаря в pick-режиме.
	// MonthGrid — 6 рядов по 7 ячеек (понедельник…воскресенье).
	MonthLabel   string
	MonthGrid    [][]calCell
	PrevMonthURL string
	NextMonthURL string
	// TelegramSubscribeURL — ссылка на бота с deep-link'ом, добавляющая
	// текущий target в подписки. Пусто — Telegram-бот не настроен,
	// кнопка скрыта.
	TelegramSubscribeURL string
	// IsMyTarget — true, если cookie my_target указывает ровно на этот
	// target. Используется шаблоном, чтобы вместо кнопки «Сделать моим»
	// показать значок «★ Моё расписание» с кнопкой «Открепить».
	IsMyTarget bool
}

// dayRef — день расписания, который шаблон рендерит как карточку.
// Поле Day — копия Day из Schedule.Weeks (а не указатель), чтобы шаблон
// не зависел от индексов и пар.
type dayRef struct {
	WeekIdx    int
	DayIdx     int
	Date       time.Time
	Day        model.Day
	IsToday    bool // совпадает с сегодняшней календарной датой
	IsExactDay bool // совпадает с todayHint.IsExactDay
}

// calCell — одна ячейка мини-календаря в pick-режиме.
type calCell struct {
	Date       time.Time
	Day        int  // число месяца (1..31)
	InMonth    bool // ячейка относится к отображаемому месяцу
	HasLessons bool // в этот день есть хотя бы одна пара
	IsToday    bool
	IsSelected bool
}

// ambiguousData — данные для страницы «уточни запрос».
type ambiguousData struct {
	Title   string
	Target  model.Target
	Options []string
}

// historyData — данные для страницы истории версий target'а.
type historyData struct {
	Title    string
	Target   model.Target
	Versions []historyRow // упорядочены от новых к старым
}

type historyRow struct {
	ID         string
	FetchedAt  time.Time
	Lessons    int
	PrevID     string // ID предыдущей (более старой) версии — для diff-ссылки; пусто у самой ранней
	DiffNumber int    // сколько изменений до предыдущей версии (или 0, если предыдущей нет)
}

// diffData — данные для страницы сравнения двух версий.
type diffData struct {
	Title         string
	Target        model.Target
	OldID, NewID  string
	OldAt, NewAt  time.Time
	Sections      []diffSection // изменения, сгруппированные по виду
	TotalChanges  int
}

// diffSection — группа изменений одного вида («отменено», «новое», …).
type diffSection struct {
	Title   string
	Kind    diff.Kind
	Changes []diff.Change
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

	now := time.Now().In(krskLocation)
	hl := highlights(result.Schedule, now)
	today := computeTodayHint(result.Schedule, now)

	view := parseView(r.URL.Query().Get("view"))
	selected := parseDayParam(r.URL.Query().Get("day"), now)
	starts := weekStartsAround(selected)

	renderDays := pickRenderDays(result.Schedule, view, selected, today, now)

	var monthLabel, prevMonthURL, nextMonthURL string
	var grid [][]calCell
	if view == "pick" {
		month := parseMonthParam(r.URL.Query().Get("month"), selected)
		grid = buildMonthGrid(result.Schedule, month, selected, now)
		monthLabel = fmt.Sprintf("%s %d", russianMonthNom(month.Month()), month.Year())
		prev := month.AddDate(0, -1, 0)
		next := month.AddDate(0, 1, 0)
		base := "/schedule/" + targetTypeToURL(target.Type) + "/" + url.PathEscape(target.Query)
		prevMonthURL = base + "?view=pick&month=" + prev.Format("2006-01")
		nextMonthURL = base + "?view=pick&month=" + next.Format("2006-01")
		if !selected.IsZero() {
			prevMonthURL += "&day=" + selected.Format("2006-01-02")
			nextMonthURL += "&day=" + selected.Format("2006-01-02")
		}
	}

	var tgURL string
	if s.tgBotUsername != "" {
		tgURL = "https://t.me/" + s.tgBotUsername + "?start=" + notify.EncodeStartToken(target)
	}
	myTarget := readMyTargetCookie(r, s.cfg.DefaultTarget)
	isMine := myTarget != nil && myTarget.Type == target.Type &&
		strings.EqualFold(myTarget.Query, target.Query)
	if s.onTouch != nil {
		s.onTouch(target)
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	s.render.render(w, http.StatusOK, "schedule", scheduleData{
		Title:                target.Label() + " — расписание",
		Schedule:             result.Schedule,
		Target:               target,
		FromCache:            fromCache,
		CacheReason:          cacheReason,
		Today:                today,
		NowSlot:              hl.Now,
		NextSlot:             hl.Next,
		NextLessonAt:         hl.NextAt,
		ServerNow:            now,
		View:                 view,
		RenderDays:           renderDays,
		WeekStarts:           starts,
		SelectedDay:          selected,
		MonthLabel:           monthLabel,
		MonthGrid:            grid,
		PrevMonthURL:         prevMonthURL,
		NextMonthURL:         nextMonthURL,
		TelegramSubscribeURL: tgURL,
		IsMyTarget:           isMine,
	})
}

// parseView разбирает ?view=… c фолбэком на "day".
func parseView(raw string) string {
	switch raw {
	case "week", "pick":
		return raw
	}
	return "day"
}

// parseDayParam разбирает ?day=YYYY-MM-DD в дату 00:00 KRSK. Невалидный
// или пустой формат → сегодня.
func parseDayParam(raw string, now time.Time) time.Time {
	if raw != "" {
		if t, err := time.ParseInLocation("2006-01-02", raw, krskLocation); err == nil {
			return t
		}
	}
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, krskLocation)
}

// parseMonthParam разбирает ?month=YYYY-MM, дефолт — месяц selected.
func parseMonthParam(raw string, selected time.Time) time.Time {
	if raw != "" {
		if t, err := time.ParseInLocation("2006-01", raw, krskLocation); err == nil {
			return t
		}
	}
	return time.Date(selected.Year(), selected.Month(), 1, 0, 0, 0, 0, krskLocation)
}

// weekStartsAround — понедельники двух соседних недель относительно date,
// положенные по индексу WeekParity. starts[parity(date)] = пн его недели,
// starts[1-parity] = пн следующей.
func weekStartsAround(date time.Time) [2]time.Time {
	date = date.In(krskLocation)
	thisMon := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, krskLocation)
	wd := int(thisMon.Weekday())
	if wd == 0 {
		wd = 7
	}
	thisMon = thisMon.AddDate(0, 0, -(wd - 1))
	parity := model.WeekParity(thisMon, time.Time{})
	var starts [2]time.Time
	starts[parity] = thisMon
	starts[1-parity] = thisMon.AddDate(0, 0, 7)
	return starts
}

// pickRenderDays строит список dayRef в зависимости от выбранного view.
//   - "day"  → два ближайших учебных дня от now (или selected, если он будущий).
//   - "week" → 7 дней недели, в которой лежит selected (даже пустые).
//   - "pick" → ровно один день selected (даже если пар нет).
func pickRenderDays(s model.Schedule, view string, selected time.Time, today todayHint, now time.Time) []dayRef {
	if len(s.Weeks) < 2 {
		return nil
	}
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, krskLocation)

	switch view {
	case "week":
		mon := mondayOfDate(selected)
		out := make([]dayRef, 0, 7)
		for i := 0; i < 7; i++ {
			d := mon.AddDate(0, 0, i)
			wi := model.WeekParity(d, time.Time{})
			di := weekdayIndex(d)
			if wi >= len(s.Weeks) || di >= len(s.Weeks[wi].Days) {
				continue
			}
			out = append(out, makeDayRef(s, wi, di, d, todayStart, today))
		}
		return out
	case "pick":
		wi := model.WeekParity(selected, time.Time{})
		di := weekdayIndex(selected)
		if wi >= len(s.Weeks) || di >= len(s.Weeks[wi].Days) {
			return nil
		}
		return []dayRef{makeDayRef(s, wi, di, selected, todayStart, today)}
	}

	// "day": два ближайших учебных дня от сегодня.
	out := make([]dayRef, 0, 2)
	for off := 0; off < 14 && len(out) < 2; off++ {
		d := todayStart.AddDate(0, 0, off)
		wi := model.WeekParity(d, time.Time{})
		di := weekdayIndex(d)
		if wi >= len(s.Weeks) || di >= len(s.Weeks[wi].Days) {
			continue
		}
		if len(s.Weeks[wi].Days[di].Lessons) == 0 {
			continue
		}
		out = append(out, makeDayRef(s, wi, di, d, todayStart, today))
	}
	return out
}

func makeDayRef(s model.Schedule, wi, di int, date, todayStart time.Time, today todayHint) dayRef {
	isToday := date.Equal(todayStart)
	isExact := isToday && today.Found && today.IsExactDay
	return dayRef{
		WeekIdx:    wi,
		DayIdx:     di,
		Date:       date,
		Day:        s.Weeks[wi].Days[di],
		IsToday:    isToday,
		IsExactDay: isExact,
	}
}

func mondayOfDate(t time.Time) time.Time {
	t = t.In(krskLocation)
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7
	}
	return time.Date(t.Year(), t.Month(), t.Day()-(wd-1), 0, 0, 0, 0, krskLocation)
}

// buildMonthGrid строит сетку 6×7 для мини-календаря отображаемого месяца.
// Первая колонка — понедельник. Пустые недели в конце месяца обрезаются,
// чтобы не плодить лишние строки (фактически рядов 4-6).
func buildMonthGrid(s model.Schedule, month, selected, now time.Time) [][]calCell {
	first := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, krskLocation)
	wd := int(first.Weekday())
	if wd == 0 {
		wd = 7
	}
	gridStart := first.AddDate(0, 0, -(wd - 1))
	todayDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, krskLocation)
	selDate := time.Date(selected.Year(), selected.Month(), selected.Day(), 0, 0, 0, 0, krskLocation)

	var rows [][]calCell
	for r := 0; r < 6; r++ {
		row := make([]calCell, 7)
		anyInMonth := false
		for c := 0; c < 7; c++ {
			d := gridStart.AddDate(0, 0, r*7+c)
			inMonth := d.Month() == month.Month()
			row[c] = calCell{
				Date:       d,
				Day:        d.Day(),
				InMonth:    inMonth,
				HasLessons: dayHasLessons(s, d),
				IsToday:    d.Equal(todayDate),
				IsSelected: d.Equal(selDate),
			}
			if inMonth {
				anyInMonth = true
			}
		}
		rows = append(rows, row)
		if r >= 3 && !anyInMonth {
			rows = rows[:len(rows)-1]
			break
		}
	}
	return rows
}

func dayHasLessons(s model.Schedule, d time.Time) bool {
	if len(s.Weeks) < 2 {
		return false
	}
	wi := model.WeekParity(d, time.Time{})
	di := weekdayIndex(d)
	if wi >= len(s.Weeks) || di >= len(s.Weeks[wi].Days) {
		return false
	}
	return len(s.Weeks[wi].Days[di].Lessons) > 0
}

// russianMonthNom — название месяца в именительном падеже («Май»).
// В genitive у нас уже есть russianMonthGen (для дат «26 мая»); здесь
// нужен заголовок мини-календаря.
func russianMonthNom(m time.Month) string {
	switch m {
	case time.January:
		return "Январь"
	case time.February:
		return "Февраль"
	case time.March:
		return "Март"
	case time.April:
		return "Апрель"
	case time.May:
		return "Май"
	case time.June:
		return "Июнь"
	case time.July:
		return "Июль"
	case time.August:
		return "Август"
	case time.September:
		return "Сентябрь"
	case time.October:
		return "Октябрь"
	case time.November:
		return "Ноябрь"
	case time.December:
		return "Декабрь"
	}
	return ""
}

// handleServiceWorker отдаёт static/sw.js под корневым путём /sw.js,
// чтобы у service worker'а получился scope = "/" (если файл доступен
// только через /static/sw.js, scope ограничивается /static/ и SW не
// сможет перехватывать запросы к расписанию). Альтернатива — слать
// заголовок Service-Worker-Allowed: /, но проще обслужить файл из корня.
func (s *Server) handleServiceWorker(w http.ResponseWriter, _ *http.Request) {
	b, err := staticFS.ReadFile("static/sw.js")
	if err != nil {
		http.Error(w, "sw.js missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Service-Worker-Allowed", "/")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}

// handleAPISchedule отдаёт расписание target'а в JSON. Тот же source-of-truth,
// что и HTML-страница (svc.Get с кэшем 15 минут и фолбэком на историю), но
// в машиночитаемом виде — для расширений, ботов и собственных интеграций.
//
// GET /api/schedule/{type}/{q} → 200 model.Schedule (без RawHTML);
//   404 — если group/teacher/room не найден;
//   422 — ambiguous: возвращает {"error":"ambiguous","options":[...]};
//   502 — сайт недоступен и в истории ничего нет.
func (s *Server) handleAPISchedule(w http.ResponseWriter, r *http.Request) {
	typ := r.PathValue("type")
	tt, ok := urlTypeToTargetType(typ)
	if !ok {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "unknown type"})
		return
	}
	q := r.PathValue("q")
	if q == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "empty query"})
		return
	}
	target := model.Target{Type: tt, Query: q}

	result, err := s.svc.Get(r.Context(), target, s.cfg.CacheFreshness)
	// Сетевая ошибка — фолбэк на последний снапшот.
	if err != nil &&
		!errors.Is(err, resolve.ErrNotFound) &&
		!errors.Is(err, resolve.ErrAmbiguous) {
		if sched, _, lerr := s.store.Latest(target.Key()); lerr == nil {
			result = schedule.Result{Schedule: sched, Source: schedule.SourceCache}
			err = nil
		}
	}
	if errors.Is(err, resolve.ErrNotFound) {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if errors.Is(err, resolve.ErrAmbiguous) {
		writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]any{
			"error":   "ambiguous",
			"options": parseAmbiguousOptions(err.Error()),
		})
		return
	}
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}

	// RawHTML не отдаём наружу: оно весит ~150KB и не нужно потребителям API.
	sched := result.Schedule
	sched.RawHTML = ""
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, sched)
}

// handleAPIHistory отдаёт список сохранённых версий target'а в JSON.
// Опциональный query-параметр ?limit=N ограничивает выдачу (по умолчанию 50);
// порядок — от новых к старым (как ListLatest в store).
//
// GET /api/history/{type}/{q} → 200 [{id, fetched_at, title, lessons}].
func (s *Server) handleAPIHistory(w http.ResponseWriter, r *http.Request) {
	target, ok := parseAPITarget(w, r)
	if !ok {
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	infos, err := s.store.ListLatest(target.Key(), limit)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if infos == nil {
		infos = []store.VersionInfo{} // не отдавать null
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, infos)
}

// handleAPIHistoryVersion отдаёт конкретную сохранённую версию.
// Опциональный ?diff_to=<id> добавляет рядом diff к указанной версии:
// `{"schedule":{...},"diff":[{...}]}`. Без diff_to отдаётся одна Schedule.
//
// GET /api/history/{type}/{q}/{id}[?diff_to=<id>] → 200 Schedule | {schedule, diff}.
func (s *Server) handleAPIHistoryVersion(w http.ResponseWriter, r *http.Request) {
	target, ok := parseAPITarget(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "empty id"})
		return
	}
	sched, err := s.store.Load(target.Key(), id)
	if err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	sched.RawHTML = ""

	diffTo := r.URL.Query().Get("diff_to")
	if diffTo == "" {
		w.Header().Set("Cache-Control", "public, max-age=300")
		writeJSON(w, sched)
		return
	}
	other, err := s.store.Load(target.Key(), diffTo)
	if err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "diff_to: " + err.Error()})
		return
	}
	other.RawHTML = ""

	// Семантика: diff_to — «другая версия, с которой сравнить». Считаем
	// diff_to → id, т.е. показываем, что изменилось ПРИ ПЕРЕХОДЕ от
	// diff_to к id. Это совпадает с UI: на странице history ссылка
	// /history/.../{prev}..{this} означает «что добавилось в this
	// относительно prev».
	changes := diff.DiffSchedule(other, sched)
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, map[string]any{
		"schedule": sched,
		"diff_to":  diffTo,
		"diff":     changes,
	})
}

// parseAPITarget парсит {type}/{q} из URL пути или возвращает ошибку клиенту.
// Если type невалиден или q пуст — пишет 404/400 и возвращает ok=false.
func parseAPITarget(w http.ResponseWriter, r *http.Request) (model.Target, bool) {
	tt, ok := urlTypeToTargetType(r.PathValue("type"))
	if !ok {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "unknown type"})
		return model.Target{}, false
	}
	q := r.PathValue("q")
	if q == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "empty query"})
		return model.Target{}, false
	}
	return model.Target{Type: tt, Query: q}, true
}

// writeJSONStatus — writeJSON с заданным HTTP-кодом.
func writeJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

// handleHistory отдаёт страницу со списком сохранённых версий target'а.
// /history/{type}/{q} → templates/history.html. Для каждой версии вычисляется
// количество отличий от предыдущей — пользователю сразу видно, в каких
// снапшотах что-то поменялось, а в каких только переснимок.
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	typ := r.PathValue("type")
	tt, ok := urlTypeToTargetType(typ)
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := r.PathValue("q")
	if q == "" {
		http.NotFound(w, r)
		return
	}
	target := model.Target{Type: tt, Query: q}

	infos, err := s.store.ListLatest(target.Key(), 50)
	if err != nil {
		s.render.render(w, http.StatusInternalServerError, "error", errorData{
			Title:   "Не получилось прочитать историю",
			Message: err.Error(),
		})
		return
	}
	rows := buildHistoryRows(s.store, target.Key(), infos)
	s.render.render(w, http.StatusOK, "history", historyData{
		Title:    "История — " + target.Label(),
		Target:   target,
		Versions: rows,
	})
}

// buildHistoryRows формирует строки для history.html: на каждой версии
// проставляется prev и количество изменений между парами соседей.
// Diff считается дёшево даже для 50 версий — Load(JSON-файл) + DiffSchedule.
func buildHistoryRows(st diffStore, key string, infos []store.VersionInfo) []historyRow {
	rows := make([]historyRow, len(infos))
	// infos идут от новых к старым. Для каждой считаем diff с СЛЕДУЮЩЕЙ
	// (более старой) — это будет «изменения этого снапшота относительно
	// предыдущего по времени».
	var loaded []model.Schedule
	for _, v := range infos {
		sched, err := st.Load(key, v.ID)
		if err != nil {
			loaded = append(loaded, model.Schedule{})
			continue
		}
		loaded = append(loaded, sched)
	}
	for i, v := range infos {
		r := historyRow{ID: v.ID, FetchedAt: v.FetchedAt, Lessons: v.Lessons}
		if i+1 < len(infos) {
			r.PrevID = infos[i+1].ID
			r.DiffNumber = len(diff.DiffSchedule(loaded[i+1], loaded[i]))
		}
		rows[i] = r
	}
	return rows
}

// diffStore — узкий интерфейс к store.Store: нужен только для тестов
// buildHistoryRows без подъёма всего пакета store.
type diffStore interface {
	Load(key, id string) (model.Schedule, error)
}

// handleDiff отрисовывает изменения между двумя версиями.
// /history/{type}/{q}/{id1}..{id2}: id1 — более старая, id2 — более новая.
// Порядок в URL фиксированный, чтобы постоянная ссылка имела однозначный
// смысл «что изменилось от id1 к id2».
func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	typ := r.PathValue("type")
	tt, ok := urlTypeToTargetType(typ)
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := r.PathValue("q")
	span := r.PathValue("span")
	id1, id2, ok := strings.Cut(span, "..")
	if !ok || id1 == "" || id2 == "" {
		http.NotFound(w, r)
		return
	}
	target := model.Target{Type: tt, Query: q}

	oldS, err := s.store.Load(target.Key(), id1)
	if err != nil {
		s.render.render(w, http.StatusNotFound, "error", errorData{
			Title:   "Версия не найдена",
			Message: "id1: " + err.Error(),
		})
		return
	}
	newS, err := s.store.Load(target.Key(), id2)
	if err != nil {
		s.render.render(w, http.StatusNotFound, "error", errorData{
			Title:   "Версия не найдена",
			Message: "id2: " + err.Error(),
		})
		return
	}

	changes := diff.DiffSchedule(oldS, newS)
	s.render.render(w, http.StatusOK, "diff", diffData{
		Title:        "Изменения — " + target.Label(),
		Target:       target,
		OldID:        id1,
		NewID:        id2,
		OldAt:        oldS.FetchedAt,
		NewAt:        newS.FetchedAt,
		Sections:     groupChanges(changes),
		TotalChanges: len(changes),
	})
}

// groupChanges раскладывает изменения по разделам в человекочитаемом порядке.
// Пустые разделы пропускаются, чтобы шаблон не рисовал ненужные заголовки.
func groupChanges(changes []diff.Change) []diffSection {
	order := []struct {
		kind  diff.Kind
		title string
	}{
		{diff.KindRemoved, "Отменено"},
		{diff.KindAdded, "Добавлено"},
		{diff.KindTime, "Перенесено по времени"},
		{diff.KindRoom, "Сменилась аудитория"},
		{diff.KindTeacher, "Сменился преподаватель"},
		{diff.KindType, "Сменился тип занятия"},
	}
	out := make([]diffSection, 0, len(order))
	for _, o := range order {
		var items []diff.Change
		for _, c := range changes {
			if c.Kind == o.kind {
				items = append(items, c)
			}
		}
		if len(items) > 0 {
			out = append(out, diffSection{Title: o.title, Kind: o.kind, Changes: items})
		}
	}
	return out
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "OK")
}

// handleForget стирает cookie my_target и редиректит на главную. Только POST,
// чтобы случайные ссылки/префетчи не сбрасывали выбор.
func (s *Server) handleForget(w http.ResponseWriter, r *http.Request) {
	clearMyTargetCookie(w, r)
	dest := r.FormValue("next")
	if dest == "" || !strings.HasPrefix(dest, "/") {
		dest = "/"
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// handleMyTargetSave явно записывает выбранный target в cookie my_target.
// Только POST: cookie меняется лишь по осознанному клику пользователя, а
// не автоматически при просмотре чужого расписания.
// Параметры формы: type=group|teacher|room, q=<query>, next=<URL для 303>.
func (s *Server) handleMyTargetSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	typ := r.FormValue("type")
	q := strings.TrimSpace(r.FormValue("q"))
	tt, ok := urlTypeToTargetType(typ)
	if !ok || q == "" {
		http.Error(w, "bad target", http.StatusBadRequest)
		return
	}
	writeMyTargetCookie(w, r, model.Target{Type: tt, Query: q})
	dest := r.FormValue("next")
	if dest == "" || !strings.HasPrefix(dest, "/") {
		dest = "/schedule/" + typ + "/" + url.PathEscape(q)
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
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
