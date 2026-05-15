# deploy/

Деплой `sibsutis-schedule-bot` на сервер одной командой:
кросс-сборка → `scp` → systemd-user + linger → старт сервиса.

Запускать **из этого каталога**: скрипт сам определяет корень репо как родителя
своего каталога — флаг `--source` нужен только если запускаешь из чужого места.

## Зачем

Чтобы каждый push новой версии бота не превращался в ручной обход трёх каталогов
на сервере. `./deploy.sh --host my-server` — и всё.

Скрипт идемпотентен: повторный запуск пересобирает бинарник, обновляет его
атомарно (`install -m 755`) и перезапускает юнит. `config.txt` с секретами
**никогда не трогается**.

## Требования

- Локально: **Go 1.25+** (бот живёт в репо рядом и собирается отсюда), `ssh`, `scp`.
- На сервере: Linux с **systemd**, SSH-доступ по ключу (или `ssh-agent` с
  passphrase). Сам `loginctl enable-linger` обычно не требует `sudo`, на свежих
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
с правами 600. Скрипт напомнит вписать секреты и перезапустить юнит:

```bash
ssh my-server 'nano ~/.config/sibsutis-schedule/config.txt'
# login=...
# password=...
# group=ИКС-531
# telegram_token=...   (получи у @BotFather)
# cycle_anchor=2025-09-01    (опц., для /today)
ssh my-server 'systemctl --user restart sibsutis-schedule-bot.service'
```

## Аргументы

| Флаг | По умолчанию | Что |
|---|---|---|
| `--host <alias>` | из `deploy.conf` | алиас сервера из `~/.ssh/config` или `user@host` |
| `--arch amd64\|arm64` | `amd64` | архитектура серверного бинарника |
| `--source <path>` | родитель каталога скрипта (= корень репо) | путь к проекту бота |
| `--dry-run` | — | перечислить шаги, ничего не делать |
| `--no-restart` | — | собрать и залить, но не дёргать `systemctl` |
| `--uninstall` | — | остановить юнит, удалить unit + бинарник (config.txt и история на месте) |
| `--help`, `-h` | — | справка |

## Что куда кладётся на сервере

```
~/.local/bin/sibsutis-schedule-bot                          # бинарник
~/.config/systemd/user/sibsutis-schedule-bot.service        # юнит
~/.config/sibsutis-schedule/config.txt                      # секреты (chmod 600)
~/.local/share/sibsutis-schedule/                           # история + subscriptions.json
```

`config.txt` и `~/.local/share/sibsutis-schedule/` — данные пользователя, скрипт
их не перезаписывает.

## Диагностика

```bash
ssh my-server 'systemctl --user status sibsutis-schedule-bot.service'
ssh my-server 'journalctl --user -u sibsutis-schedule-bot.service -f'
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
  определить серверную архитектуру и предупредить. Для ARMv7 / RISC-V — собирай
  бинарник вручную и копируй, deploy.sh пока поддерживает только amd64/arm64.
- **SSH-agent / passphrase.** Скрипт сначала пробует SSH в `BatchMode`; если ключ
  с passphrase и агента нет — переключается на интерактивный режим (введёшь
  passphrase в терминале).

## Удаление

```bash
./deploy.sh --host my-server --uninstall
# вручную, если нужно дочистить:
ssh my-server 'rm -rf ~/.config/sibsutis-schedule ~/.local/share/sibsutis-schedule'
ssh my-server 'sudo loginctl disable-linger $(whoami)'   # если linger тебе больше не нужен
```
