package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/config"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/resolve"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/schedule"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/store"
)

// stubFetcher — управляемая реализация schedule.Fetcher.
type stubFetcher struct {
	err   error
	sched model.Schedule
}

func (s *stubFetcher) Fetch(_ context.Context, t model.Target) (model.Schedule, error) {
	if s.err != nil {
		return model.Schedule{}, s.err
	}
	if s.sched.Target.Type == "" {
		// дефолтное расписание
		return model.Schedule{
			Target:    t,
			Title:     t.Query,
			FetchedAt: time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC),
			Weeks: []model.Week{
				{Name: "числитель", Days: []model.Day{
					{Weekday: "Понедельник", Lessons: []model.Lesson{
						{Number: 1, TimeFrom: "08:00", TimeTo: "09:35",
							Subject: "Физика", Type: "Лекция",
							Teachers: []string{"Иванов И.И."}, Room: "а.101"},
					}},
				}},
			},
		}, nil
	}
	return s.sched, nil
}

func newTestServer(t *testing.T, fetcher schedule.Fetcher, cfg *config.Config) *Server {
	t.Helper()
	if cfg == nil {
		cfg = &config.Config{
			Login: "u", Password: "p",
			ScheduleURL: "http://example.com/", AuthURL: "http://example.com/auth/",
			CacheFreshness: time.Minute, WebListenAddr: ":0",
		}
	}
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := schedule.New(fetcher, st)
	srv, err := New(cfg, svc, st)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

func TestHealthz(t *testing.T) {
	srv := newTestServer(t, &stubFetcher{}, nil)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestHomeRenders(t *testing.T) {
	cfg := &config.Config{
		Login: "u", Password: "p",
		ScheduleURL: "http://example.com/", AuthURL: "http://example.com/auth/",
		CacheFreshness: time.Minute,
		DefaultTarget:  &model.Target{Type: model.TypeStudent, Query: "ИКС-531"},
	}
	srv := newTestServer(t, &stubFetcher{}, cfg)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	checks := []string{"Расписание SibSUTI", "Найти расписание", "ИКС-531", "Моё расписание"}
	for _, c := range checks {
		if !strings.Contains(string(body), c) {
			t.Errorf("в / нет %q", c)
		}
	}
}

func TestSearchRedirects(t *testing.T) {
	srv := newTestServer(t, &stubFetcher{}, nil)
	// httptest клиент не должен следовать редиректу — мы хотим увидеть Location
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	form := url.Values{"type": {"group"}, "q": {"ИКС-531"}}
	resp, err := client.PostForm(ts.URL+"/search", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/schedule/group/") {
		t.Errorf("Location = %q, want /schedule/group/...", loc)
	}
}

func TestScheduleRenders(t *testing.T) {
	srv := newTestServer(t, &stubFetcher{}, nil)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/schedule/group/" + url.PathEscape("ИКС-531"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, body=%s", resp.StatusCode, body)
	}
	checks := []string{"группа ИКС-531", "Физика", "Иванов И.И.", "08:00–09:35", "Понедельник"}
	for _, c := range checks {
		if !strings.Contains(string(body), c) {
			t.Errorf("на странице расписания нет %q", c)
		}
	}
}

func TestScheduleNotFoundRenders(t *testing.T) {
	srv := newTestServer(t, &stubFetcher{err: fmt.Errorf("wrap: %w", resolve.ErrNotFound)}, nil)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/schedule/group/" + url.PathEscape("НЕТ"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Ничего не найдено") {
		t.Errorf("error.html не отрисовался: %s", body)
	}
}

func TestScheduleAmbiguousRenders(t *testing.T) {
	errMsg := fmt.Errorf("%w: по запросу %q (аудитория) подходит 3:\n  - а.101\n  - а.102\n  - а.103",
		resolve.ErrAmbiguous, "1")
	srv := newTestServer(t, &stubFetcher{err: errMsg}, nil)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/schedule/room/1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Уточни запрос") {
		t.Errorf("ambiguous.html не отрисовался: %s", body)
	}
	// варианты как ссылки (href URL-кодируется html/template'ом в контексте атрибута URL)
	if !strings.Contains(string(body), `>а.101</a>`) {
		t.Errorf("вариант не превратился в ссылку: %s", body)
	}
}

func TestSuggestShortQuery(t *testing.T) {
	srv := newTestServer(t, &stubFetcher{}, nil)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	// Один символ — меньше suggestMinQ, сайт не дёргаем, отдаём [].
	resp, err := http.Get(ts.URL + "/api/suggest?type=group&q=" + url.QueryEscape("А"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json…", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if got := strings.TrimSpace(string(body)); got != "[]" {
		t.Errorf("body = %q, want []", got)
	}
}

func TestSuggestUnknownType(t *testing.T) {
	srv := newTestServer(t, &stubFetcher{}, nil)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/suggest?type=ufo&q=" + url.QueryEscape("Иванов"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if got := strings.TrimSpace(string(body)); got != "[]" {
		t.Errorf("body = %q, want []", got)
	}
}

func TestScheduleSetsMyTargetCookie(t *testing.T) {
	srv := newTestServer(t, &stubFetcher{}, nil)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/schedule/group/" + url.PathEscape("ИКС-531"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "my_target" {
			got = c
		}
	}
	if got == nil {
		t.Fatal("Set-Cookie my_target отсутствует")
	}
	if !strings.HasPrefix(got.Value, "group/") {
		t.Errorf("cookie value = %q, ожидался префикс 'group/'", got.Value)
	}
	if got.HttpOnly != true {
		t.Errorf("cookie должен быть HttpOnly")
	}
	if got.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", got.SameSite)
	}
}

func TestForgetClearsCookieAndRedirects(t *testing.T) {
	srv := newTestServer(t, &stubFetcher{}, nil)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := client.Post(ts.URL+"/forget", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
	// Cookie с пустым value + MaxAge<=0.
	cleared := false
	for _, c := range resp.Cookies() {
		if c.Name == "my_target" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("cookie my_target не был сброшен")
	}
}

func TestHomeShowsMyTargetFromCookie(t *testing.T) {
	srv := newTestServer(t, &stubFetcher{}, nil)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/", nil)
	req.AddCookie(&http.Cookie{Name: "my_target", Value: "teacher/" + url.PathEscape("Иванов И.И.")})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Открыть последнее") {
		t.Errorf("на главной нет ссылки «Открыть последнее»: %s", body)
	}
	if !strings.Contains(string(body), "Иванов И.И.") {
		t.Errorf("на главной нет ФИО из cookie: %s", body)
	}
}

func TestHistoryRendersVersions(t *testing.T) {
	srv := newTestServer(t, &stubFetcher{}, nil)
	target := model.Target{Type: model.TypeStudent, Query: "ИКС-531"}

	v1 := model.Schedule{
		Target: target, Title: "ИКС-531",
		FetchedAt: time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC),
		Weeks: []model.Week{
			{Name: "числитель", Days: []model.Day{
				{Weekday: "Понедельник", Lessons: []model.Lesson{
					{Number: 1, TimeFrom: "08:00", TimeTo: "09:35", Subject: "Физика", Room: "а.101"},
				}},
			}},
		},
	}
	v2 := v1
	v2.FetchedAt = time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC)
	d := append([]model.Day(nil), v1.Weeks[0].Days...)
	d[0] = model.Day{Weekday: "Понедельник", Lessons: []model.Lesson{
		{Number: 1, TimeFrom: "08:00", TimeTo: "09:35", Subject: "Физика", Room: "а.202"}, // сменилась аудитория
	}}
	v2.Weeks = []model.Week{{Name: "числитель", Days: d}}

	if _, _, err := srv.store.Save(target.Key(), v1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := srv.store.Save(target.Key(), v2); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/history/group/" + url.PathEscape("ИКС-531"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "История версий") {
		t.Errorf("нет заголовка истории: %s", body)
	}
	if !strings.Contains(string(body), "показать diff") {
		t.Errorf("нет ссылки на diff: %s", body)
	}
}

func TestDiffRenders(t *testing.T) {
	srv := newTestServer(t, &stubFetcher{}, nil)
	target := model.Target{Type: model.TypeStudent, Query: "ИКС-531"}

	v1 := model.Schedule{
		Target: target, FetchedAt: time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC),
		Weeks: []model.Week{
			{Name: "числитель", Days: []model.Day{
				{Weekday: "Понедельник", Lessons: []model.Lesson{
					{Number: 1, TimeFrom: "08:00", TimeTo: "09:35", Subject: "Физика", Room: "а.101"},
				}},
			}},
		},
	}
	v2 := model.Schedule{
		Target: target, FetchedAt: time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC),
		Weeks: []model.Week{
			{Name: "числитель", Days: []model.Day{
				{Weekday: "Понедельник", Lessons: []model.Lesson{
					{Number: 1, TimeFrom: "08:00", TimeTo: "09:35", Subject: "Физика", Room: "а.999"},
				}},
			}},
		},
	}
	_, id1, _ := srv.store.Save(target.Key(), v1)
	_, id2, _ := srv.store.Save(target.Key(), v2)

	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/history/group/" + url.PathEscape("ИКС-531") + "/" + id1 + ".." + id2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Сменилась аудитория") {
		t.Errorf("нет раздела «Сменилась аудитория»: %s", body)
	}
	if !strings.Contains(string(body), "а.101") || !strings.Contains(string(body), "а.999") {
		t.Errorf("в diff нет старого/нового room: %s", body)
	}
}

func TestAPIScheduleRendersJSON(t *testing.T) {
	srv := newTestServer(t, &stubFetcher{}, nil)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/schedule/group/" + url.PathEscape("ИКС-531"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"weeks"`) {
		t.Errorf("в ответе нет weeks: %s", body)
	}
	if strings.Contains(string(body), `"raw_html"`) {
		t.Errorf("в JSON оказался raw_html: %s", body)
	}
}

func TestAPIScheduleNotFound(t *testing.T) {
	srv := newTestServer(t, &stubFetcher{err: fmt.Errorf("wrap: %w", resolve.ErrNotFound)}, nil)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/schedule/group/" + url.PathEscape("НЕТУ"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestAPIHistoryListAndDiff(t *testing.T) {
	srv := newTestServer(t, &stubFetcher{}, nil)
	target := model.Target{Type: model.TypeStudent, Query: "ИКС-531"}

	v1 := model.Schedule{
		Target: target, FetchedAt: time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC),
		Weeks: []model.Week{
			{Name: "числитель", Days: []model.Day{
				{Weekday: "Понедельник", Lessons: []model.Lesson{
					{Number: 1, TimeFrom: "08:00", TimeTo: "09:35", Subject: "Физика", Room: "а.101"},
				}},
			}},
		},
	}
	v2 := model.Schedule{
		Target: target, FetchedAt: time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC),
		Weeks: []model.Week{
			{Name: "числитель", Days: []model.Day{
				{Weekday: "Понедельник", Lessons: []model.Lesson{
					{Number: 1, TimeFrom: "08:00", TimeTo: "09:35", Subject: "Физика", Room: "а.999"},
				}},
			}},
		},
	}
	_, id1, _ := srv.store.Save(target.Key(), v1)
	_, id2, _ := srv.store.Save(target.Key(), v2)

	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	// Список версий.
	resp, err := http.Get(ts.URL + "/api/history/group/" + url.PathEscape("ИКС-531"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("history status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), id2) || !strings.Contains(string(body), id1) {
		t.Errorf("в списке нет обоих id: %s", body)
	}

	// Конкретная версия с diff_to.
	url2 := ts.URL + "/api/history/group/" + url.PathEscape("ИКС-531") + "/" + id2 + "?diff_to=" + id1
	resp2, err := http.Get(url2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(body2), `"schedule"`) || !strings.Contains(string(body2), `"diff"`) {
		t.Errorf("ожидались schedule+diff: %s", body2)
	}
	if !strings.Contains(string(body2), `"kind":"room"`) {
		t.Errorf("ожидался room-change: %s", body2)
	}
}

func TestScheduleICSRenders(t *testing.T) {
	srv := newTestServer(t, &stubFetcher{}, nil)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/calendar/group/" + url.PathEscape("ИКС-531"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/calendar") {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, mustHave := range []string{"BEGIN:VCALENDAR", "BEGIN:VEVENT", "RRULE:FREQ=WEEKLY;INTERVAL=2"} {
		if !strings.Contains(string(body), mustHave) {
			t.Errorf("в .ics нет %q", mustHave)
		}
	}
}

func TestICSSubscribeEndpoint(t *testing.T) {
	cfg := &config.Config{
		Login: "u", Password: "p",
		ScheduleURL: "http://example.com/", AuthURL: "http://example.com/auth/",
		CacheFreshness: time.Minute,
		ICSSecret:      []byte("0123456789abcdef0123456789abcdef"),
	}
	srv := newTestServer(t, &stubFetcher{}, cfg)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	tok := signICSTarget(cfg.ICSSecret, model.Target{Type: model.TypeStudent, Query: "ИКС-531"})

	// Валидный токен — 200 + text/calendar.
	resp, err := http.Get(ts.URL + "/ics/" + tok)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("valid token status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/calendar") {
		t.Errorf("Content-Type = %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "BEGIN:VCALENDAR") {
		t.Errorf("в теле подписки нет VCALENDAR: %s", body)
	}

	// Битый токен — 404.
	resp2, err := http.Get(ts.URL + "/ics/garbage-token-xxx")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 404 {
		t.Errorf("bad token status = %d, want 404", resp2.StatusCode)
	}
}

func TestNetworkErrorFallsBackToCache(t *testing.T) {
	srv := newTestServer(t, &stubFetcher{err: errors.New("боже сайт лёг")}, nil)
	// положим в store кэш для target
	target := model.Target{Type: model.TypeStudent, Query: "ИКС-531"}
	cached := model.Schedule{
		Target:    target,
		FetchedAt: time.Now().Add(-2 * time.Hour),
		Weeks: []model.Week{
			{Name: "числитель", Days: []model.Day{
				{Weekday: "Понедельник", Lessons: []model.Lesson{
					{Number: 1, TimeFrom: "08:00", TimeTo: "09:35", Subject: "СтараяВерсия"},
				}},
			}},
		},
	}
	if _, _, err := srv.store.Save(target.Key(), cached); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/schedule/group/" + url.PathEscape("ИКС-531"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "сайт недоступен") {
		t.Errorf("ожидалась пометка ⚠ сайт недоступен: %s", body)
	}
	if !strings.Contains(string(body), "СтараяВерсия") {
		t.Errorf("должно показать содержимое кэша: %s", body)
	}
}
