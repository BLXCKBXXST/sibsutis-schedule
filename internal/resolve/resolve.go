// Package resolve превращает человекочитаемый запрос (название группы, ФИО
// преподавателя, номер аудитории) в идентификатор, который сайт ждёт в URL
// страницы расписания.
//
// Для этого используется тот же AJAX-эндпоинт, что и выпадающий список (select2)
// на странице выбора: он принимает поисковый термин и возвращает JSON
// {"results":[{"id":...,"text":"..."}]}.
package resolve

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

// Ошибки, означающие проблему с самим запросом пользователя (а не с доступностью
// сайта) — вызывающий код не должен подменять их устаревшим кэшем.
var (
	ErrNotFound  = errors.New("ничего не найдено")
	ErrAmbiguous = errors.New("найдено несколько вариантов — уточни запрос")
)

const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/124.0 Safari/537.36"

// maxOptions — сколько вариантов показывать в тексте ошибки ErrAmbiguous.
const maxOptions = 15

// Match — найденный target: идентификатор для URL и его подпись с сайта.
type Match struct {
	ID   string
	Text string
}

// Resolve ищет target через AJAX-эндпоинт соответствующего типа и сводит
// результат к единственному совпадению или типизированной ошибке
// (ErrNotFound / ErrAmbiguous). scheduleBaseURL нужен только чтобы взять
// схему и хост сайта.
func Resolve(client *http.Client, scheduleBaseURL string, t model.Target) (Match, error) {
	matches, meta, err := Search(client, scheduleBaseURL, t)
	if err != nil {
		return Match{}, err
	}
	return pick(matches, t, meta)
}

// Search возвращает все совпадения по запросу без сужения к одному.
// Используется автокомплитом в веб-фронте.
func Search(client *http.Client, scheduleBaseURL string, t model.Target) ([]Match, model.TypeMeta, error) {
	meta, ok := t.Meta()
	if !ok {
		return nil, model.TypeMeta{}, fmt.Errorf("неизвестный тип расписания: %q", t.Type)
	}
	endpoint, err := buildAjaxURL(scheduleBaseURL, meta, t.Query)
	if err != nil {
		return nil, meta, err
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, meta, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := client.Do(req)
	if err != nil {
		return nil, meta, fmt.Errorf("запрос к %s: %w", meta.AjaxPath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, meta, fmt.Errorf("эндпоинт поиска ответил %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, meta, fmt.Errorf("чтение ответа поиска: %w", err)
	}
	matches, err := parseResults(body)
	if err != nil {
		return nil, meta, err
	}
	return matches, meta, nil
}

// pick выбирает единственное совпадение или возвращает типизированную ошибку.
func pick(matches []Match, t model.Target, meta model.TypeMeta) (Match, error) {
	switch len(matches) {
	case 0:
		return Match{}, fmt.Errorf("%w: %s %q", ErrNotFound, meta.Label, t.Query)
	case 1:
		return matches[0], nil
	}

	// Несколько совпадений: берём точное по тексту, иначе просим уточнить.
	q := strings.ToLower(strings.TrimSpace(t.Query))
	var exact []Match
	for _, m := range matches {
		if strings.ToLower(strings.TrimSpace(m.Text)) == q {
			exact = append(exact, m)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	return Match{}, fmt.Errorf("%w: по запросу %q (%s) подходит %d:\n%s",
		ErrAmbiguous, t.Query, meta.Label, len(matches), formatOptions(matches))
}

// buildAjaxURL собирает URL AJAX-эндпоинта на хосте из scheduleBaseURL.
func buildAjaxURL(scheduleBaseURL string, meta model.TypeMeta, query string) (string, error) {
	base, err := url.Parse(scheduleBaseURL)
	if err != nil || base.Host == "" {
		return "", fmt.Errorf("некорректный schedule_url: %q", scheduleBaseURL)
	}
	u := url.URL{Scheme: base.Scheme, Host: base.Host, Path: meta.AjaxPath}
	q := url.Values{}
	q.Set(meta.AjaxParam, query)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// ajaxResponse — формат ответа select2-эндпоинта.
type ajaxResponse struct {
	Results []struct {
		ID   flexString `json:"id"`
		Text string     `json:"text"`
	} `json:"results"`
}

// parseResults разбирает JSON ответа в список совпадений.
func parseResults(body []byte) ([]Match, error) {
	var parsed ajaxResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("разбор ответа поиска (%d байт): %w", len(body), err)
	}
	matches := make([]Match, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		matches = append(matches, Match{ID: string(r.ID), Text: strings.TrimSpace(r.Text)})
	}
	return matches, nil
}

// formatOptions форматирует список вариантов для текста ошибки ErrAmbiguous.
func formatOptions(matches []Match) string {
	var b strings.Builder
	for i, m := range matches {
		if i >= maxOptions {
			fmt.Fprintf(&b, "  … и ещё %d", len(matches)-maxOptions)
			break
		}
		fmt.Fprintf(&b, "  - %s\n", m.Text)
	}
	return strings.TrimRight(b.String(), "\n")
}

// flexString разбирает значение JSON, которое может прийти как строкой, так и
// числом (id в ответе бывает и тем, и другим).
type flexString string

func (f *flexString) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if string(trimmed) == "null" {
		*f = ""
		return nil
	}
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	*f = flexString(trimmed)
	return nil
}
