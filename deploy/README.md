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

Поддомен: **`sibsutis.server34.netcraze.club`** (та же схема, что у `fmd.` и
`files.` из репозитория [home-server](https://github.com/BLXCKBXXST/home-server)).

Важно: на сервере Caddy крутится **контейнером** в docker-стеке `/opt/stack` и
проксирует к другим контейнерам по имени. Наш веб-сервер работает на **хосте**
как systemd-сервис, не в Docker — поэтому Caddy ходит к нему через адрес хоста
(`host.docker.internal`), а не `127.0.0.1`.

1. В `/opt/stack/docker-compose.yml`, в сервис `caddy:`, добавь (если ещё нет):

   ```yaml
       extra_hosts:
         - "host.docker.internal:host-gateway"
   ```

2. В `/opt/stack/caddy/Caddyfile` добавь блок:

   ```
   sibsutis.server34.netcraze.club {
       encode gzip
       reverse_proxy host.docker.internal:8080
   }
   ```

3. DNS-запись `sibsutis.server34.netcraze.club` (A/CNAME — туда же, куда
   указывают `fmd.` и `files.`), затем перезапусти Caddy:

   ```bash
   cd /opt/stack && docker compose up -d caddy
   ```

Caddy сам выпустит Let's Encrypt-сертификат при первом обращении. Порт `8080`
наружу (на роутере) пробрасывать **не нужно** — только 80/443 для Caddy.

Без Caddy сайт работает на `http://<server-ip>:8080` (если порт открыт).

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
