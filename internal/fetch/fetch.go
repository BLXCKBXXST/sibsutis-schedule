// Package fetch загружает HTML страницы расписания через авторизованный клиент.
//
// Реальное расписание находится по URL вида
// /students/schedule/?type=<student|teacher|room>&<param>=<id>, где id заранее
// получен через пакет resolve.
//
// Любая сетевая проблема, ответ не-200 или редирект обратно на страницу входа
// оборачиваются в ErrSiteUnavailable — это сигнал вызывающему коду перейти на
// показ расписания из локальной истории.
package fetch

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

// ErrSiteUnavailable означает, что расписание не получено: сайт не ответил,
// вернул ошибку или сбросил сессию.
var ErrSiteUnavailable = errors.New("сайт недоступен")

const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/124.0 Safari/537.36"

// Schedule загружает HTML страницы расписания для target'а t с идентификатором id.
// Возвращаемая ошибка оборачивает ErrSiteUnavailable при любых проблемах с сайтом.
func Schedule(client *http.Client, scheduleBaseURL string, t model.Target, id string) (string, error) {
	meta, ok := t.Meta()
	if !ok {
		return "", fmt.Errorf("неизвестный тип расписания: %q", t.Type)
	}

	pageURL, err := buildScheduleURL(scheduleBaseURL, t, meta, id)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrSiteUnavailable, err)
	}

	req, err := http.NewRequest(http.MethodGet, pageURL, nil)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrSiteUnavailable, err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrSiteUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: сервер ответил %s", ErrSiteUnavailable, resp.Status)
	}

	// Сессия могла истечь — тогда сайт отдаёт страницу входа вместо расписания.
	if u := resp.Request.URL; u != nil && strings.Contains(u.Path, "/auth/") {
		return "", fmt.Errorf("%w: сессия сброшена, требуется повторный вход", ErrSiteUnavailable)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("%w: чтение ответа: %v", ErrSiteUnavailable, err)
	}

	html := string(body)
	if strings.Contains(html, "AUTH_FORM") && strings.Contains(strings.ToLower(html), "password") {
		return "", fmt.Errorf("%w: вместо расписания отдана форма входа", ErrSiteUnavailable)
	}
	return html, nil
}

// buildScheduleURL подставляет в базовый URL параметры type и <param>=<id>.
func buildScheduleURL(scheduleBaseURL string, t model.Target, meta model.TypeMeta, id string) (string, error) {
	u, err := url.Parse(scheduleBaseURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("некорректный schedule_url: %q", scheduleBaseURL)
	}
	q := u.Query()
	q.Set("type", string(t.Type))
	q.Set(meta.URLParam, id)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
