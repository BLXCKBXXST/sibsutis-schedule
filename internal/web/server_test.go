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
