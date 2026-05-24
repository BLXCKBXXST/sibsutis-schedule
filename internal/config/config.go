// Package config загружает настройки парсера из простого key=value файла.
//
// Формат файла (config.txt):
//
//	login=ваш_логин
//	password=ваш_пароль
//	# target по умолчанию — ровно один из group/teacher/room (можно не задавать,
//	# тогда target указывается флагом --group/--teacher/--room):
//	group=ИБ-211
//	# опционально:
//	# schedule_url=https://my.sibsutis.ru/students/schedule/
//	# cache_freshness_minutes=15
//	# web_listen_addr=:8080
//
// Реальный config.txt не попадает в репозиторий (см. .gitignore); в репозитории
// лежит config.example.txt. Пользователь сам сохраняет логин и пароль в файл —
// учётные данные нигде не передаются, кроме этого локального файла.
package config

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

// Значения по умолчанию.
const (
	DefaultScheduleURL    = "https://my.sibsutis.ru/students/schedule/"
	DefaultAuthURL        = "https://my.sibsutis.ru/auth/"
	DefaultCacheFreshness = 15 * time.Minute
	DefaultWebListenAddr  = ":8080"
	DefaultWatchInterval  = 60 * time.Minute
	DefaultWatchTTL       = 14 * 24 * time.Hour
	MinWatchInterval      = 5 * time.Minute
)

// Config — разобранные настройки.
type Config struct {
	Login          string
	Password       string
	ScheduleURL    string        // базовый URL страницы расписания
	AuthURL        string        // страница авторизации Bitrix
	DefaultTarget  *model.Target // target по умолчанию из config.txt; nil — не задан
	CacheFreshness time.Duration // как долго версия в кэше считается свежей (для web-сервера)
	WebListenAddr  string        // адрес и порт прослушивания web-сервера (напр. ":8080")
	Path           string        // откуда загружен конфиг (для сообщений)
	// ICSSecret — HMAC-ключ для подписи токенов webcal-подписок. Если в
	// config.txt не задан ics_secret — генерируется случайно при старте;
	// в этом случае токены инвалидируются при каждом перезапуске процесса
	// (приемлемо для small-scale деплоя; для production стабильности —
	// прописать ics_secret в config.txt).
	ICSSecret []byte
	// WatchInterval — как часто фоновый воркер обновляет «горячие» target'ы
	// в watch.json. Дефолт 60 минут. Жёстко лимитируется снизу 5 минутами —
	// чтобы случайно не задосить my.sibsutis.ru.
	WatchInterval time.Duration
	// WatchTTL — через сколько без просмотров target выкидывается из
	// реестра. Дефолт 14 дней.
	WatchTTL time.Duration
}

// Load ищет и читает конфиг. Если explicitPath не пуст — используется только он.
// Иначе проверяются ./config.txt и ~/.config/sibsutis-schedule/config.txt.
func Load(explicitPath string) (*Config, error) {
	path, err := resolvePath(explicitPath)
	if err != nil {
		return nil, err
	}

	warnIfWorldReadable(path)

	values, err := parseFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Login:         values["login"],
		Password:      values["password"],
		ScheduleURL:   values["schedule_url"],
		AuthURL:       values["auth_url"],
		WebListenAddr: values["web_listen_addr"],
		Path:          path,
	}
	if cfg.ScheduleURL == "" {
		cfg.ScheduleURL = DefaultScheduleURL
	}
	if cfg.AuthURL == "" {
		cfg.AuthURL = DefaultAuthURL
	}
	if cfg.WebListenAddr == "" {
		cfg.WebListenAddr = DefaultWebListenAddr
	}

	if cfg.Login == "" || cfg.Password == "" {
		return nil, fmt.Errorf("в %s не заданы login и/или password", path)
	}

	target, err := parseTarget(values)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.DefaultTarget = target

	if cfg.CacheFreshness, err = parseFreshness(values["cache_freshness_minutes"]); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	cfg.ICSSecret, err = parseICSSecret(values["ics_secret"])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	cfg.WatchInterval, err = parseMinutes(values["watch_interval_minutes"], DefaultWatchInterval, MinWatchInterval)
	if err != nil {
		return nil, fmt.Errorf("%s: watch_interval_minutes: %w", path, err)
	}
	cfg.WatchTTL, err = parseMinutes(values["watch_ttl_minutes"], DefaultWatchTTL, time.Hour)
	if err != nil {
		return nil, fmt.Errorf("%s: watch_ttl_minutes: %w", path, err)
	}

	return cfg, nil
}

// parseMinutes — общий парсер «значение в минутах»; пусто/0 → def, иначе
// проверка на минимум.
func parseMinutes(s string, def, min time.Duration) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("ожидалось неотрицательное целое в минутах, получено %q", s)
	}
	if n == 0 {
		return def, nil
	}
	d := time.Duration(n) * time.Minute
	if d < min {
		return 0, fmt.Errorf("должно быть не меньше %s (получено %s)", min, d)
	}
	return d, nil
}

// parseICSSecret разбирает ics_secret из конфига. Допустимые форматы:
// - пустая строка / не задано → генерируется 32 случайных байта и в stderr
//   печатается предупреждение (токены подписок инвалидируются при рестарте);
// - hex-строка (64 символа `[0-9a-fA-F]`) — декодируется как 32 байта;
// - любая другая непустая строка — берётся как байты «как есть» (≥16 байт
//   для базовой защиты от подбора).
func parseICSSecret(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, fmt.Errorf("ics_secret: не удалось сгенерировать случайные байты: %w", err)
		}
		fmt.Fprintln(os.Stderr,
			"предупреждение: ics_secret не задан в config.txt — сгенерирован случайно. "+
				"Подписные webcal-URL'ы будут инвалидироваться при каждом перезапуске сервера. "+
				"Чтобы зафиксировать, добавь в config.txt строку:\n"+
				"  ics_secret="+hex.EncodeToString(secret))
		return secret, nil
	}
	if len(raw) == 64 {
		if b, err := hex.DecodeString(raw); err == nil {
			return b, nil
		}
	}
	if len(raw) < 16 {
		return nil, fmt.Errorf("ics_secret слишком короткий (минимум 16 символов или 64-символьная hex-строка)")
	}
	return []byte(raw), nil
}

// parseFreshness разбирает cache_freshness_minutes (целое число минут).
// Пусто/0 → DefaultCacheFreshness.
func parseFreshness(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return DefaultCacheFreshness, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("cache_freshness_minutes должно быть неотрицательным целым, получено %q", s)
	}
	if n == 0 {
		return DefaultCacheFreshness, nil
	}
	return time.Duration(n) * time.Minute, nil
}

// parseTarget извлекает target по умолчанию из ключей group/teacher/room.
// Допустим ровно один из них; если не задан ни один — target отсутствует (nil).
func parseTarget(values map[string]string) (*model.Target, error) {
	candidates := []struct {
		key string
		typ model.TargetType
	}{
		{"group", model.TypeStudent},
		{"teacher", model.TypeTeacher},
		{"room", model.TypeRoom},
	}

	var found *model.Target
	for _, c := range candidates {
		v := strings.TrimSpace(values[c.key])
		if v == "" {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("задайте только один из group/teacher/room, а не несколько")
		}
		found = &model.Target{Type: c.typ, Query: v}
	}
	return found, nil
}

// resolvePath возвращает путь к существующему конфигу либо ошибку со списком
// проверенных мест.
func resolvePath(explicitPath string) (string, error) {
	if explicitPath != "" {
		if _, err := os.Stat(explicitPath); err != nil {
			return "", fmt.Errorf("конфиг не найден: %s", explicitPath)
		}
		return explicitPath, nil
	}

	candidates := []string{"config.txt"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".config", "sibsutis-schedule", "config.txt"))
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("конфиг не найден. Создай config.txt по образцу config.example.txt "+
		"(проверены: %s)", strings.Join(candidates, ", "))
}

// parseFile читает key=value строки, игнорируя пустые строки и комментарии (#).
func parseFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть конфиг: %w", err)
	}
	defer f.Close()

	values := make(map[string]string)
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		key, val, ok := strings.Cut(raw, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: ожидался формат key=value", path, line)
		}
		values[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(val)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("ошибка чтения конфига: %w", err)
	}
	return values, nil
}

// warnIfWorldReadable печатает предупреждение в stderr, если файл с паролем
// доступен на чтение группе или остальным.
func warnIfWorldReadable(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if info.Mode().Perm()&0o077 != 0 {
		fmt.Fprintf(os.Stderr,
			"предупреждение: %s доступен на чтение другим пользователям; "+
				"рекомендуется chmod 600 %s\n", path, path)
	}
}
