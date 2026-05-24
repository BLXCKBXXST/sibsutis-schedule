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
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/ics"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
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

// handleScheduleICS отдаёт расписание target'а в iCalendar-формате.
// GET /schedule/{type}/{q}.ics. Источник тот же — svc.Get с фолбэком на
// историю; RawHTML в .ics не идёт. Content-Disposition заставляет браузер
// предложить «открыть в календаре» вместо рендера в окне.
func (s *Server) handleScheduleICS(w http.ResponseWriter, r *http.Request) {
	tt, ok := urlTypeToTargetType(r.PathValue("type"))
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

	result, err := s.svc.Get(r.Context(), target, s.cfg.CacheFreshness)
	if err != nil &&
		!errors.Is(err, resolve.ErrNotFound) &&
		!errors.Is(err, resolve.ErrAmbiguous) {
		if sched, _, lerr := s.store.Latest(target.Key()); lerr == nil {
			result = schedule.Result{Schedule: sched, Source: schedule.SourceCache}
			err = nil
		}
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	body := ics.RenderSchedule(result.Schedule, ics.Options{
		Anchor: time.Now().In(krskLocation),
		Loc:    krskLocation,
	})
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=900")
	filename := icsFilename(target)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	_, _ = w.Write(body)
}

// icsFilename — латиница-only имя файла, чтобы не воевать с кодировками
// в HTTP-заголовке. Структура: sibsutis-<type>-<хэш ключа>.ics.
func icsFilename(t model.Target) string {
	key := strings.ReplaceAll(t.Key(), "/", "-")
	return "sibsutis-" + key + ".ics"
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
