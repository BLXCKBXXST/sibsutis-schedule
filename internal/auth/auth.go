// Package auth выполняет вход в личный кабinет my.sibsutis.ru (Bitrix CMS) и
// возвращает HTTP-клиент с авторизованной сессией в cookie.
//
// Сайт использует стандартную форму авторизации Bitrix. Чтобы не зависеть от
// точных имён полей, форма считывается со страницы /auth/: берутся её action,
// все скрытые поля и имена полей логина/пароля. Если форму распознать не
// удалось — используется стандартный набор полей Bitrix как запасной вариант.
package auth

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// ErrAuthFailed возвращается, когда сайт не принял логин/пароль.
var ErrAuthFailed = errors.New("авторизация не удалась: проверь login и password в config.txt")

// userAgent — представляемся обычным браузером, чтобы запрос не отсекли.
const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/124.0 Safari/537.36"

// Стандартные имена полей формы авторизации Bitrix — запасной вариант, если
// распарсить форму со страницы не получилось.
const (
	defaultLoginField    = "USER_LOGIN"
	defaultPasswordField = "USER_PASSWORD"
)

// loginForm — распознанная форма авторизации.
type loginForm struct {
	action        string            // абсолютный URL, куда отправлять POST
	loginField    string            // имя поля логина
	passwordField string            // имя поля пароля
	hidden        map[string]string // скрытые и прочие предзаполненные поля
}

// Login авторизуется на сайте и возвращает клиент с живой сессией.
// authURL — адрес страницы авторизации (обычно https://my.sibsutis.ru/auth/).
func Login(authURL, login, password string, timeout time.Duration) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("создание cookie jar: %w", err)
	}
	client := &http.Client{Jar: jar, Timeout: timeout}

	// 1. Забираем страницу авторизации — получаем cookie сессии и саму форму.
	doc, err := getDoc(client, authURL)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть страницу авторизации: %w", err)
	}

	// 2. Распознаём форму (или берём стандартную раскладку Bitrix).
	form := detectForm(doc, authURL)

	// 3. Отправляем учётные данные.
	values := url.Values{}
	for k, v := range form.hidden {
		values.Set(k, v)
	}
	values.Set(form.loginField, login)
	values.Set(form.passwordField, password)
	// Поля, которые Bitrix ожидает независимо от вёрстки страницы.
	values.Set("AUTH_FORM", "Y")
	values.Set("TYPE", "AUTH")
	values.Set("USER_REMEMBER", "Y")
	if values.Get("Login") == "" {
		values.Set("Login", "Войти")
	}

	if err := postForm(client, form.action, values); err != nil {
		return nil, fmt.Errorf("отправка формы авторизации: %w", err)
	}

	// 4. Проверяем, что вход выполнен: на /auth/ больше не должно быть поля пароля.
	check, err := getDoc(client, authURL)
	if err != nil {
		return nil, fmt.Errorf("проверка авторизации: %w", err)
	}
	if check.Find("input[type=password]").Length() > 0 {
		return nil, ErrAuthFailed
	}
	return client, nil
}

// detectForm ищет на странице форму с полем пароля и вытаскивает её параметры.
// Если форма не найдена — возвращает стандартную раскладку Bitrix.
func detectForm(doc *goquery.Document, pageURL string) loginForm {
	form := loginForm{
		action:        pageURL,
		loginField:    defaultLoginField,
		passwordField: defaultPasswordField,
		hidden:        map[string]string{},
	}

	base, _ := url.Parse(pageURL)
	var found bool
	doc.Find("form").EachWithBreak(func(_ int, f *goquery.Selection) bool {
		if f.Find("input[type=password]").Length() == 0 {
			return true // не форма входа — ищем дальше
		}
		found = true

		if action, ok := f.Attr("action"); ok && strings.TrimSpace(action) != "" {
			if ref, err := url.Parse(strings.TrimSpace(action)); err == nil && base != nil {
				form.action = base.ResolveReference(ref).String()
			}
		}

		f.Find("input").Each(func(_ int, in *goquery.Selection) {
			name, ok := in.Attr("name")
			if !ok || name == "" {
				return
			}
			typ := strings.ToLower(in.AttrOr("type", "text"))
			switch {
			case typ == "password":
				form.passwordField = name
			case (typ == "text" || typ == "email") && strings.Contains(strings.ToUpper(name), "LOGIN"):
				form.loginField = name
			case typ == "hidden", typ == "submit":
				form.hidden[name] = in.AttrOr("value", "")
			}
		})
		return false // форма найдена — останавливаемся
	})

	_ = found // форму не нашли — остаются дефолты Bitrix, что тоже рабочий вариант
	return form
}

// getDoc выполняет GET и парсит ответ как HTML-документ.
func getDoc(client *http.Client, rawURL string) (*goquery.Document, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("сервер ответил %s", resp.Status)
	}
	return goquery.NewDocumentFromReader(resp.Body)
}

// postForm отправляет форму методом POST.
func postForm(client *http.Client, rawURL string, values url.Values) error {
	req, err := http.NewRequest(http.MethodPost, rawURL, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Bitrix после входа отдаёт 200 или редирект на backurl — обе ситуации норм.
	if resp.StatusCode >= 400 {
		return fmt.Errorf("сервер ответил %s", resp.Status)
	}
	return nil
}
