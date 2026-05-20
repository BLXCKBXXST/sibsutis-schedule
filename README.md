# sibsutis-schedule

Парсер расписания с **[my.sibsutis.ru](https://my.sibsutis.ru/students/schedule/)**.

Логинится в личный кабинет, выгружает расписание **по группе, преподавателю или
аудитории**, сохраняет каждую выгрузку в локальную **историю версий**. Два фронта:

- **CLI** (`sibsutis-schedule`) — расписание таблицей в терминал.
- **Веб-сервер** (`sibsutis-schedule-web`) — открытый HTTP-сайт с формой поиска и
  страницами расписаний; деплоится одной командой через [`deploy/`](deploy/).

Если my.sibsutis.ru недоступен, обе фронты отдают последнюю успешную выгрузку
из истории с пометкой `⚠`.

## Зачем

Расписание на my.sibsutis.ru доступно только после авторизации, а сам сайт периодически
лежит. Утилита держит локальный кэш всех выгрузок: даже когда сайт недоступен, расписание
можно посмотреть — будет показана последняя сохранённая версия. Через systemd timer кэш
обновляется в фоне, поэтому он всегда свежий.

- Авторизация через стандартную форму Bitrix, сессия в cookie
- Три режима: расписание группы, преподавателя или аудитории
- История версий ведётся отдельно для каждого target'а; новая версия сохраняется,
  только когда расписание реально изменилось
- Откат на кэш при недоступности сайта
- Один статический бинарник, минимум зависимостей (только `goquery` для разбора
  формы входа; само расписание — это встроенный в страницу JSON)

## Требования для сборки

- **Go 1.25+**
- Подключение к интернету для скачивания зависимостей при первой сборке

## Сборка

```bash
cd sibsutis-schedule
go mod tidy

# CLI
go build -ldflags="-s -w" -o sibsutis-schedule .

# Веб-сервер
go build -ldflags="-s -w" -o sibsutis-schedule-web ./cmd/web

# (опционально) положить в PATH
install -m 755 sibsutis-schedule sibsutis-schedule-web ~/.local/bin/
```

## Настройка

Учётные данные хранятся в локальном файле `config.txt` (в репозиторий не попадает):

```bash
cp config.example.txt config.txt
chmod 600 config.txt
# открой config.txt и впиши свои login, password и target по умолчанию
```

Формат `config.txt` — `key=value`:

```
login=ваш_логин
password=ваш_пароль

# target по умолчанию — РОВНО ОДИН из group / teacher / room
# (можно не задавать — тогда target указывается флагом при каждом запуске):
group=ИБ-211
# teacher=Иванов И.И.
# room=1-101

# schedule_url=https://my.sibsutis.ru/students/schedule/   (опционально)
```

Конфиг ищется в таком порядке: путь из флага `--config` → `./config.txt` →
`~/.config/sibsutis-schedule/config.txt`.

## Использование

```bash
# Показать расписание target'а по умолчанию из config.txt
sibsutis-schedule
sibsutis-schedule show

# Расписание конкретной группы / преподавателя / аудитории
sibsutis-schedule show --group ИБ-211
sibsutis-schedule show --teacher "Иванов И.И."
sibsutis-schedule show --room 1-101

# Показать без обращения к сайту — сразу из истории
sibsutis-schedule show --group ИБ-211 --no-fetch

# Выгрузить и сохранить в историю (тихо) — для cron / systemd timer
sibsutis-schedule update
sibsutis-schedule update --teacher "Иванов И.И."

# Список target'ов в истории
sibsutis-schedule history
# Версии конкретного target'а
sibsutis-schedule history --group ИБ-211
# Показать конкретную версию из истории (ID берётся из history)
sibsutis-schedule show --group ИБ-211 --version 2026-05-14T20-42-00

# Справка и версия
sibsutis-schedule help
sibsutis-schedule version
```

### Выбор target'а

Команды `show`, `update`, `history` принимают **ровно один** флаг выбора target'а.
Если флаг не задан — берётся target по умолчанию из `config.txt`.

| Флаг | Что показывает | Поиск по |
|------|----------------|----------|
| `--group <название>` | расписание группы | названию группы |
| `--teacher <ФИО>` | расписание преподавателя | ФИО (можно частично) |
| `--room <аудитория>` | расписание аудитории | номеру/названию аудитории |

Запрос ищется через тот же поиск, что и выпадающий список на сайте. Если найдено
несколько вариантов и ни один не совпал точно — утилита покажет список и попросит
уточнить запрос.

### Прочие флаги

| Флаг | Команды | Описание |
|------|---------|----------|
| `--config <путь>` | `show`, `update` | путь к `config.txt` |
| `--data-dir <путь>` | все | каталог истории (по умолчанию `~/.local/share/sibsutis-schedule`) |
| `--no-fetch` | `show` | не обращаться к сайту, показать последнюю версию из истории |
| `--version <ID>` | `show` | показать конкретную версию из истории |
| `--quiet` | `update` | печатать только ошибки |

## Где хранятся данные

История ведётся отдельно для каждого target'а:

```
~/.local/share/sibsutis-schedule/
└── history/
    ├── student-иб-211/
    │   ├── 2026-05-14T20-42-00.json   # версия расписания (структура + сырой HTML)
    │   ├── ...
    │   └── meta.json                  # время последней проверки и последняя ошибка
    ├── teacher-иванов-и-и/
    │   └── ...
    └── room-1-101/
        └── ...
```

Каталог переопределяется флагом `--data-dir` или переменной `XDG_DATA_HOME`.

## Автообновление кэша (systemd timer)

Чтобы кэш обновлялся в фоне (тогда при падении сайта в истории всегда свежая версия),
есть готовые **user-level** unit-файлы (sudo не нужен) в каталоге [`systemd/`](systemd/).
Таймер запускает `sibsutis-schedule update`, который обновляет target по умолчанию из
`config.txt`.

```bash
# 1. Положить бинарник и конфиг по путям из unit-файла (или поправить пути в нём)
install -m 755 sibsutis-schedule ~/.local/bin/
mkdir -p ~/.config/sibsutis-schedule
cp config.txt ~/.config/sibsutis-schedule/

# 2. Установить unit-файлы
mkdir -p ~/.config/systemd/user
cp systemd/sibsutis-schedule.service systemd/sibsutis-schedule.timer ~/.config/systemd/user/

# 3. Включить таймер
systemctl --user daemon-reload
systemctl --user enable --now sibsutis-schedule.timer

# Проверка
systemctl --user list-timers sibsutis-schedule.timer
journalctl --user -u sibsutis-schedule.service
```

По умолчанию таймер запускает выгрузку каждые 3 часа (и через 5 минут после загрузки).
Интервал меняется в `sibsutis-schedule.timer` (`OnUnitActiveSec`).

Чтобы фоном обновлять несколько target'ов, скопируй unit-файлы под другими именами и
добавь в `ExecStart` нужный флаг, например `update --quiet --teacher "Иванов И.И."`.

### Вариант через cron

```cron
# crontab -e — обновлять кэш каждые 3 часа
0 */3 * * * ~/.local/bin/sibsutis-schedule update --quiet --config ~/.config/sibsutis-schedule/config.txt
```

## Как это работает

1. **auth** — логин стандартной формой Bitrix на `/auth/`, сессия остаётся в cookie.
2. **resolve** — человекочитаемый запрос (`ИБ-211`, `Иванов`, `1-101`) превращается в
   ID через AJAX-эндпоинт сайта (`get_groups_soap.php` / `get_pps.php` / `get_room.php`).
3. **fetch** — запрашивается `/students/schedule/?type=<тип>&<param>=<ID>`.
4. **parse** — расписание встроено в страницу как JS-переменные `days[1..14]`
   (JSON-строки): `days[1..7]` — неделя «числитель», `days[8..14]` — «знаменатель».
   Парсер разбирает этот JSON, а не вёрстку, — это надёжнее.
5. **store** — версия сохраняется в историю target'а (с дедупом по содержимому).
6. **render** — расписание печатается таблицей; при недоступности сайта берётся
   последняя версия из истории с пометкой `⚠`.

## Веб-сайт

В репозитории есть второй бинарник — `sibsutis-schedule-web`, открытый HTTP-сервер
поверх парсера. Любой может зайти и посмотреть расписание любой группы,
преподавателя или аудитории; все запросы идут через одни креденшелы владельца
сервера к my.sibsutis.ru.

### Защита от нагрузки

- **Кэш-свежесть**: повторный запрос того же target'а в течение
  `cache_freshness_minutes` (по умолчанию 15 мин) отвечает из истории, не
  дёргая my.sibsutis.ru.
- **Singleflight**: параллельные запросы одного target'а сливаются в один
  HTTP-цикл на upstream.
- **HTTP-кэш**: страницы расписания отдаются с `Cache-Control: public, max-age=300`
  (5 мин) — браузеры и CDN сами кэшируют.
- **Фолбэк на историю**: если my.sibsutis.ru недоступен, страница отдаёт
  последнюю успешную версию с пометкой `⚠ сайт недоступен`.

### Маршруты

| Метод | Путь | Что |
|---|---|---|
| GET | `/` | главная с формой поиска и кнопкой «моё расписание» (если задан target в config) |
| POST | `/search` | принимает форму, 303-редирект на `/schedule/...` |
| GET | `/schedule/group/{q}` | расписание группы |
| GET | `/schedule/teacher/{q}` | расписание преподавателя |
| GET | `/schedule/room/{q}` | расписание аудитории |
| GET | `/static/*` | CSS и прочая статика (вшиты через `embed`) |
| GET | `/healthz` | `200 OK` для readiness/liveness |

Если запрос неоднозначен (например, `/schedule/teacher/Иванов` и Ивановых много) —
сервер отдаёт страницу со списком вариантов как ссылок.

### Локальный запуск

```bash
go build -ldflags="-s -w" -o sibsutis-schedule-web ./cmd/web
./sibsutis-schedule-web --config ./config.txt
# теперь сайт на http://localhost:8080
```

Адрес и порт можно сменить через `web_listen_addr=:8181` в `config.txt`.

### Деплой на сервер

Готовый скрипт [`deploy/deploy.sh`](deploy/deploy.sh) запускается **на самом
сервере** — качает готовый бинарник из GitHub Releases, ставит systemd-user
юнит, включает linger и запускает сервис. Go/git на сервере не нужны.

```bash
# на сервере:
./deploy.sh                  # последний релиз → ~/.local/bin → systemd → старт
```

Подробности (config.txt, обновление, удаление) — в [`deploy/README.md`](deploy/README.md).

### HTTPS через Caddy

На сервере с Caddy — добавь в `Caddyfile`:

```
schedule.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Caddy сам получит сертификат через ACME. Без HTTPS сайт всё равно работает —
на `http://<server>:8080`.

## Структура проекта

```
sibsutis-schedule/
├── main.go                       # CLI: разбор аргументов, команды, выбор target'а
├── cmd/web/                      # entry point HTTP-сервера
├── deploy/                       # deploy.sh — установщик веб-сервера на сервере
├── config.example.txt            # образец конфига
├── systemd/                      # unit-файлы для CLI-таймера и web-сервера
├── .github/workflows/release.yml # кросс-сборка бинарников по тегу
└── internal/
    ├── config/   # загрузка config.txt
    ├── model/    # типы Target, Schedule / Week / Day / Lesson
    ├── auth/     # вход в личный кабинет (Bitrix), сессия в cookie
    ├── resolve/  # поиск ID группы/преподавателя/аудитории через AJAX
    ├── fetch/    # запрос страницы расписания авторизованным клиентом
    ├── parse/    # разбор встроенного JSON расписания в Schedule
    ├── store/    # история версий по каждому target'у: сохранение, чтение, дедуп
    ├── render/   # вывод расписания таблицей в терминал (для CLI)
    ├── schedule/ # обёртка fetchFresh с кэш-свежестью и singleflight
    └── web/      # HTTP-сервер: маршруты, html/template, embedded static
```

### Зависимости

| Пакет | Назначение |
|-------|-----------|
| `github.com/PuerkitoBio/goquery` | разбор HTML формы авторизации |
| `golang.org/x/sync/singleflight` | дедуп параллельных фетчей в schedule.Service |

Конфиг, разбор расписания (JSON), история, шаблоны и таблица вывода — на стандартной библиотеке.

## Лицензия

MIT
