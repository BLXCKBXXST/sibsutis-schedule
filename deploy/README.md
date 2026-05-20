# deploy/

Установка `sibsutis-schedule-web` (HTTP-сервер расписания) **на сервер** одной
командой: скачивание готового бинарника из GitHub Releases → systemd-user юнит
→ linger → запуск.

Скрипт запускается **на самом сервере**. Ни Go, ни git, ни сборка на сервере не
нужны — берётся готовый статический бинарник из релиза.

## Быстрый старт

```bash
# на сервере, в любом каталоге:
./deploy.sh                      # ставит последний релиз
```

При первом запуске создаётся пустой `~/.config/sibsutis-schedule/config.txt`
(права 600) — впиши туда секреты и перезапусти сервис:

```bash
nano ~/.config/sibsutis-schedule/config.txt
# login=...
# password=...
# group=ИКС-531              (опц., для кнопки «моё расписание» на главной)
# web_listen_addr=:8080      (опц., default :8080)
systemctl --user restart sibsutis-schedule-web.service
```

## Команды

| Вызов | Что |
|---|---|
| `./deploy.sh` | установить / обновить до последнего релиза и запустить |
| `./deploy.sh --status` | статус сервиса, последние логи, проверка `healthz` |
| `./deploy.sh --uninstall` | остановить юнит, удалить unit-файл и бинарник (config.txt и история не трогаются) |
| `./deploy.sh --help` | справка |

Установка идемпотентна: повторный запуск обновляет бинарник до свежего релиза и
перезапускает сервис.

## Требования

- Linux с **systemd**, `curl`.
- Архитектура `amd64` или `arm64` (определяется автоматически через `uname -m`).
- `loginctl enable-linger` обычно работает без `sudo`; на части систем просит root
  — скрипт делает `sudo -A` fallback, иначе подскажет команду.

## Что куда кладётся

```
~/.local/bin/sibsutis-schedule-web                     # бинарник
~/.config/systemd/user/sibsutis-schedule-web.service   # юнит (генерируется скриптом)
~/.config/sibsutis-schedule/config.txt                 # секреты (chmod 600)
~/.local/share/sibsutis-schedule/                      # история версий расписания
```

## HTTPS через Caddy

Сервис слушает локальный порт (по умолчанию `:8080`). Чтобы открыть наружу по
HTTPS — пробрось субдомен в `Caddyfile`:

```
schedule.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Caddy сам получит сертификат через ACME. Без Caddy сайт работает на
`http://<server>:8080` (если порт открыт фаерволом).

## Диагностика

```bash
curl -s localhost:8080/healthz                          # → OK
systemctl --user status sibsutis-schedule-web.service
journalctl --user -u sibsutis-schedule-web.service -f
```

Статус `failed` сразу после первой установки — обычно пустой `config.txt`.
Впиши секреты и `systemctl --user restart sibsutis-schedule-web.service`.

## Откуда берётся бинарник

Релизы собирает GitHub Actions ([`.github/workflows/release.yml`](../.github/workflows/release.yml))
по тегу `v*`: кросс-компиляция `sibsutis-schedule-web-linux-amd64` и `-arm64`,
публикация в Releases. `deploy.sh` качает нужный по архитектуре сервера.
