# sibsutis-schedule

Парсер расписания с **[my.sibsutis.ru](https://my.sibsutis.ru/students/schedule/)**.

Логинится в личный кабинет, выгружает расписание **по группе, преподавателю или
аудитории**, сохраняет каждую выгрузку в локальную **историю версий** и печатает
расписание таблицей в терминал. Если сайт недоступен — показывает последнюю версию
из истории с пометкой.

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
go build -ldflags="-s -w" -o sibsutis-schedule .

# (опционально) положить в PATH
install -m 755 sibsutis-schedule ~/.local/bin/
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

## Telegram-бот

В репозитории есть второй бинарник — `sibsutis-schedule-bot`, открытый
Telegram-бот поверх парсера: команды для запроса расписания + подписки на
уведомления при изменениях. Любой пользователь Telegram может обратиться;
все используют твои креденшелы my.sibsutis.ru для выгрузки.

### Защита от нагрузки

- **Кэш-свежесть**: повторный запрос того же target'а в течение `telegram_freshness_minutes`
  (по умолчанию 15 мин) отвечает из истории, не дёргая сайт.
- **Singleflight**: параллельные запросы одного target'а сливаются в один fetch.
- **Per-chat throttle**: не чаще 1 команды в 3 секунды на чат.
- **Фоновое обновление**: бот сам каждые 30 минут обновляет default target и все
  target'ы из подписок; отдельный systemd-таймер от CLI не нужен.

### Настройка

```bash
# 1. Получи токен у @BotFather и допиши в config.txt (БЕЗ чтения файла):
printf '\ntelegram_token=123456:AA...\n' >> config.txt

# 2. (опц.) задай начало двухнедельного цикла, чтобы работали /today и /tomorrow:
printf 'cycle_anchor=2025-09-01\n' >> config.txt   # понедельник любой недели «числителя»

# 3. Собери и поставь бинарник
go build -ldflags="-s -w" -o sibsutis-schedule-bot ./cmd/bot
install -m 755 sibsutis-schedule-bot ~/.local/bin/
```

### Запуск как user-сервис

Локально вручную:

```bash
mkdir -p ~/.config/systemd/user
cp systemd/sibsutis-schedule-bot.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now sibsutis-schedule-bot.service
journalctl --user -u sibsutis-schedule-bot.service -f
```

На удалённый сервер — есть готовый деплой-скрипт в [deploy/](deploy/):

```bash
cd deploy
./deploy.sh --host my-server     # кросс-сборка + scp + systemd-user + linger
```

См. [deploy/README.md](deploy/README.md).

### Команды бота

| Команда | Что делает |
|---|---|
| `/start`, `/help` | Подсказка |
| `/schedule` | Расписание target'а по умолчанию (из `config.txt`) |
| `/group <название>` | Расписание группы (`ИКС-531`) |
| `/teacher <ФИО>` | Расписание преподавателя |
| `/room <номер>` | Расписание аудитории |
| `/today`, `/tomorrow` | Расписание на день (нужен `cycle_anchor`) |
| `/week numerator\|denominator` | Конкретная неделя |
| `/subscribe <group\|teacher\|room> <запрос>` | Подписаться на изменения |
| `/subscriptions` | Список моих подписок |
| `/unsubscribe ...` | Отписаться |

При неоднозначном запросе (`/teacher Иванов`, а Ивановых много) бот показывает
список вариантов — нужно дописать запрос точнее.

## Структура проекта

```
sibsutis-schedule/
├── main.go                       # CLI: разбор аргументов, команды, выбор target'а
├── cmd/bot/                      # entry point Telegram-бота
├── config.example.txt            # образец конфига
├── systemd/                      # unit-файлы для CLI-таймера и бота
├── .github/workflows/release.yml # кросс-сборка бинарников по тегу
└── internal/
    ├── config/   # загрузка config.txt
    ├── model/    # типы Target, Schedule / Week / Day / Lesson
    ├── auth/     # вход в личный кабинет (Bitrix), сессия в cookie
    ├── resolve/  # поиск ID группы/преподавателя/аудитории через AJAX
    ├── fetch/    # запрос страницы расписания авторизованным клиентом
    ├── parse/    # разбор встроенного JSON расписания в Schedule
    ├── store/    # история версий по каждому target'у: сохранение, чтение, дедуп
    ├── render/   # вывод расписания таблицей в терминал
    ├── schedule/ # обёртка fetchFresh с кэш-свежестью и singleflight
    ├── subs/     # подписки Telegram-чатов на изменения расписания
    └── bot/      # Telegram-бот: команды, форматирование, уведомления
```

### Зависимости

| Пакет | Назначение |
|-------|-----------|
| `github.com/PuerkitoBio/goquery` | разбор HTML формы авторизации |
| `github.com/go-telegram-bot-api/telegram-bot-api/v5` | Telegram Bot API (только в боте) |
| `golang.org/x/sync/singleflight` | дедуп параллельных фетчей (только в боте) |

Конфиг, разбор расписания (JSON), история и таблица вывода — на стандартной библиотеке.

## Лицензия

MIT
