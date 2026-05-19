# deploy/

Деплой `sibsutis-schedule-web` (HTTP-сервер расписания) на сервер одной командой:
кросс-сборка → `scp` → systemd-user + linger → старт сервиса.

Запускать **из этого каталога**: скрипт сам определяет корень репо как родителя
своего каталога, поэтому флаг `--source` обычно не нужен.

## Зачем

Чтобы каждый push новой версии не превращался в ручной обход трёх каталогов
на сервере. `./deploy.sh --host my-server` — и всё.

Скрипт идемпотентен: повторный запуск пересобирает бинарник, обновляет его
атомарно (`install -m 755`) и перезапускает юнит. `config.txt` с секретами
**никогда не трогается**.

## Требования

- Локально: **Go 1.25+**, `ssh`, `scp`.
- На сервере: Linux с **systemd**, SSH-доступ по ключу (или `ssh-agent` с
  passphrase). `loginctl enable-linger` обычно не требует `sudo`, на свежих
  системах — может попросить (скрипт делает `sudo -A` fallback).
- На сервере **не нужны** ни Go, ни git, ни какие-то пакеты — всё в одном
  статическом бинарнике.

## Быстрый старт

```bash
cp deploy.conf.example deploy.conf
nano deploy.conf                          # SSH_HOST=my-server и т.д. (опц.)

./deploy.sh                               # если SSH_HOST задан в deploy.conf
# или:
./deploy.sh --host my-server --arch amd64
```

При первом запуске на сервере появится **пустой** `~/.config/sibsutis-schedule/config.txt`
с правами 600. Впиши секреты и перезапусти юнит:

```bash
ssh my-server 'nano ~/.config/sibsutis-schedule/config.txt'
# login=...
# password=...
# group=ИКС-531                          (опц., для бейджа «моё расписание»)
# web_listen_addr=:8080                  (опц., default :8080)
# cache_freshness_minutes=15             (опц., default 15)
ssh my-server 'systemctl --user restart sibsutis-schedule-web.service'
```

## Аргументы

| Флаг | По умолчанию | Что |
|---|---|---|
| `--host <alias>` | из `deploy.conf` | алиас сервера из `~/.ssh/config` или `user@host` |
| `--arch amd64\|arm64` | `amd64` | архитектура серверного бинарника |
| `--source <path>` | родитель каталога скрипта (= корень репо) | путь к проекту |
| `--dry-run` | — | перечислить шаги, ничего не делать |
| `--no-restart` | — | собрать и залить, но не дёргать `systemctl` |
| `--uninstall` | — | остановить юнит, удалить unit + бинарник (config.txt и история на месте) |
| `--help`, `-h` | — | справка |

## Что куда кладётся на сервере

```
~/.local/bin/sibsutis-schedule-web                          # бинарник
~/.config/systemd/user/sibsutis-schedule-web.service        # юнит
~/.config/sibsutis-schedule/config.txt                      # секреты (chmod 600)
~/.local/share/sibsutis-schedule/                           # история версий
```

`config.txt` и `~/.local/share/sibsutis-schedule/` — данные пользователя, скрипт
их не перезаписывает.

## HTTPS через Caddy

На сервере уже стоит Caddy — добавь в `Caddyfile` блок:

```
schedule.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Caddy сам получит TLS-сертификат через ACME. После `caddy reload` сайт доступен
по HTTPS. Без Caddy сайт всё равно работает — просто на `http://<server>:8080`.

## Диагностика

```bash
ssh my-server 'systemctl --user status sibsutis-schedule-web.service'
ssh my-server 'journalctl --user -u sibsutis-schedule-web.service -f'
ssh my-server 'curl -s localhost:8080/healthz'
```

Если статус `failed` сразу после первого деплоя — скорее всего пустой
`config.txt`. Впиши секреты и `restart`.

## Известные грабли

- **linger не включается без sudo.** На некоторых дистрибутивах
  `loginctl enable-linger` требует root. Скрипт делает `sudo -A` fallback, но
  если нет TTY для askpass — `enable-linger` пропустится и юнит не переживёт
  logout. В этом случае подключись по SSH и сделай вручную:
  `sudo loginctl enable-linger $(whoami)`.
- **Архитектура несовпадает.** Скрипт сам пробует через `uname -m`
  определить серверную архитектуру и предупредить. ARMv7 / RISC-V — не
  поддерживается в скрипте, собирай вручную через `GOARCH=...`.
- **Порт 8080 занят.** Поменяй `web_listen_addr` в `config.txt` (например `:8181`)
  и поправь `reverse_proxy` в Caddyfile.

## Удаление

```bash
./deploy.sh --host my-server --uninstall
ssh my-server 'rm -rf ~/.config/sibsutis-schedule ~/.local/share/sibsutis-schedule'
ssh my-server 'sudo loginctl disable-linger $(whoami)'   # если linger тебе больше не нужен
```
