#!/bin/bash
# =============================================================
#  sibsutis-schedule-bot — deploy на сервер
#
#  Кросс-компилирует бот, заливает бинарник + systemd-юнит на сервер
#  по SSH и поднимает user-level systemd с linger.
#
#  Использование:
#    1. Скопируй deploy.conf.example в deploy.conf и заполни (опц.).
#    2. ./deploy.sh --host my-server
#       или: ./deploy.sh                      (если SSH_HOST в deploy.conf)
#
#  Параметры командной строки перекрывают deploy.conf:
#    --host <ssh-alias>      алиас сервера из ~/.ssh/config
#    --arch amd64|arm64      архитектура сервера (default: amd64)
#    --source <path>         где лежит проект sibsutis-schedule
#                            (default: $HOME/my-projects/github-repos/sibsutis-schedule)
#
#  Другие режимы:
#    --dry-run               перечислить шаги, ничего не делать
#    --no-restart            собрать и залить, но не дёргать systemctl
#    --uninstall             остановить юнит, удалить unit + бинарник
#                            (config.txt и история не трогаются)
#
#  Идемпотентно — повторный запуск безопасен.
# =============================================================

set -euo pipefail

# ─── Цвета ────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; DIM='\033[2m'; RESET='\033[0m'

info()    { echo -e "${CYAN}[INFO]${RESET}  $1"; }
success() { echo -e "${GREEN}[ OK ]${RESET}  $1"; }
warn()    { echo -e "${YELLOW}[WARN]${RESET}  $1"; }
error()   { echo -e "${RED}[ ERR]${RESET}  $1" >&2; exit 1; }

print_help() {
    cat <<'HELP'
sibsutis-schedule-bot — deploy на сервер

Использование:
  ./deploy.sh [--host <ssh-alias>] [--arch amd64|arm64] [--source <path>]
              [--dry-run] [--no-restart] [--uninstall]

Опции:
  --host <ssh-alias>   алиас сервера из ~/.ssh/config (или user@host)
  --arch amd64|arm64   архитектура серверного бинарника (default: amd64)
  --source <path>      путь к проекту sibsutis-schedule
                       (default: родитель каталога этого скрипта)
  --dry-run            перечислить шаги, ничего не делать
  --no-restart         собрать и залить, но не дёргать systemctl
  --uninstall          остановить юнит, удалить unit и бинарник
                       (config.txt и история не трогаются)
  -h, --help           эта справка

Конфиг deploy.conf рядом со скриптом перекрывается флагами CLI.

Подробности и примеры — в ./README.md.
HELP
}

# ─── Расположение скрипта и дефолтные значения ────────────────
SCRIPT_DIR="$(cd "$(dirname "$(realpath "${BASH_SOURCE[0]}")")" && pwd)"
CONF_FILE="${SCRIPT_DIR}/deploy.conf"

SSH_HOST=""
ARCH="amd64"
# Скрипт лежит в `<sibsutis-schedule>/deploy/`, поэтому родитель — корень репо.
BOT_SRC="$(dirname "$SCRIPT_DIR")"
DRY_RUN=false
NO_RESTART=false
UNINSTALL=false

# Имена ресурсов на сервере — должны совпадать с systemd/sibsutis-schedule-bot.service.
BIN_NAME="sibsutis-schedule-bot"
UNIT_NAME="sibsutis-schedule-bot.service"

if [[ -f "$CONF_FILE" ]]; then
    info "Загружаю конфиг: $CONF_FILE"
    # shellcheck source=/dev/null
    source "$CONF_FILE"
    success "Конфиг загружен"
fi

# ─── Парсинг аргументов (перекрывают .conf) ───────────────────
while [[ $# -gt 0 ]]; do
    case "$1" in
        --host)        SSH_HOST="$2"; shift 2 ;;
        --arch)        ARCH="$2";     shift 2 ;;
        --source)      BOT_SRC="$2";  shift 2 ;;
        --dry-run)     DRY_RUN=true;  shift ;;
        --no-restart)  NO_RESTART=true; shift ;;
        --uninstall)   UNINSTALL=true; shift ;;
        -h|--help)
            print_help
            exit 0
            ;;
        *) error "Неизвестный аргумент: $1 (см. --help)" ;;
    esac
done

# ─── Валидация ────────────────────────────────────────────────
[[ -n "$SSH_HOST" ]] || error "укажи --host <ssh-alias> (или SSH_HOST в deploy.conf)"
case "$ARCH" in
    amd64|arm64) ;;
    *) error "поддерживаются только amd64 / arm64; получено '$ARCH'" ;;
esac

# Helper: выполнить локально или просто напечатать в dry-run.
# Принимает одну командную строку (внутри неё может быть pipeline / && / etc).
run_local() {
    local cmd="$1"
    if $DRY_RUN; then
        echo -e "${DIM}    \$ ${cmd}${RESET}"
    else
        bash -c "$cmd"
    fi
}

# Helper: выполнить блок на сервере (heredoc через ssh).
# Использование: ssh_run <<'REMOTE' ... REMOTE
ssh_run() {
    if $DRY_RUN; then
        echo -e "${DIM}    \$ ssh ${SSH_HOST} bash -s <<REMOTE${RESET}"
        sed 's/^/      | /' >&2
        echo -e "${DIM}    REMOTE${RESET}"
    else
        ssh "$SSH_HOST" 'bash -s'
    fi
}

# ============================================================
#  --uninstall
# ============================================================
if $UNINSTALL; then
    info "Uninstall: останавливаю и удаляю юнит на ${BOLD}${SSH_HOST}${RESET}"
    ssh_run <<'REMOTE'
        set -e
        systemctl --user disable --now sibsutis-schedule-bot.service 2>/dev/null || true
        rm -f "$HOME/.config/systemd/user/sibsutis-schedule-bot.service"
        rm -f "$HOME/.local/bin/sibsutis-schedule-bot"
        systemctl --user daemon-reload 2>/dev/null || true
        echo "uninstall: бинарник и юнит удалены"
        echo "  config.txt и каталог истории на месте — удали вручную, если нужно:"
        echo "  rm -rf $HOME/.config/sibsutis-schedule $HOME/.local/share/sibsutis-schedule"
        echo "  loginctl disable-linger \$(whoami)   # если linger тебе больше не нужен"
REMOTE
    success "Uninstall выполнен"
    exit 0
fi

# ============================================================
#  Фаза 1: prereq-чек локально
# ============================================================
info "Фаза 1/6: проверка локального окружения"
command -v go      >/dev/null 2>&1 || error "не найден 'go' — нужен для сборки (https://go.dev/dl/)"
command -v ssh     >/dev/null 2>&1 || error "не найден 'ssh'"
command -v scp     >/dev/null 2>&1 || error "не найден 'scp'"
[[ -d "$BOT_SRC" ]] || error "проект не найден: $BOT_SRC (укажи --source)"
[[ -f "$BOT_SRC/cmd/bot/main.go" ]] || error "это не похоже на sibsutis-schedule: нет cmd/bot/main.go в $BOT_SRC"

UNIT_SRC="$BOT_SRC/systemd/sibsutis-schedule-bot.service"
[[ -f "$UNIT_SRC" ]] || error "не найден unit-файл: $UNIT_SRC"

success "ok: go, ssh, scp; источник: $BOT_SRC"

# ============================================================
#  Фаза 2: кросс-компиляция
# ============================================================
info "Фаза 2/6: сборка под linux/${ARCH}"
BIN_OUT="$BOT_SRC/dist/${BIN_NAME}-linux-${ARCH}"
run_local "cd '$BOT_SRC' && mkdir -p dist && GOOS=linux GOARCH=${ARCH} CGO_ENABLED=0 go build -ldflags='-s -w' -o '$BIN_OUT' ./cmd/bot"
if ! $DRY_RUN; then
    [[ -x "$BIN_OUT" ]] || error "бинарник не появился: $BIN_OUT"
    SIZE=$(du -h "$BIN_OUT" | cut -f1)
    success "собрано: $BIN_OUT ($SIZE)"
fi

# ============================================================
#  Фаза 3: probe сервера
# ============================================================
info "Фаза 3/6: проверка доступности ${BOLD}${SSH_HOST}${RESET}"
if $DRY_RUN; then
    echo -e "${DIM}    \$ ssh ${SSH_HOST} 'whoami; uname -m'${RESET}"
else
    if ! REMOTE_INFO=$(ssh -o BatchMode=yes -o ConnectTimeout=10 "$SSH_HOST" 'echo "user=$(whoami); arch=$(uname -m); home=$HOME"' 2>&1); then
        # вернёмся к обычному ssh — возможно, требуется passphrase
        warn "BatchMode не прошёл — пробую интерактивно (введи passphrase, если попросит)"
        REMOTE_INFO=$(ssh -o ConnectTimeout=15 "$SSH_HOST" 'echo "user=$(whoami); arch=$(uname -m); home=$HOME"') || \
            error "ssh до '$SSH_HOST' не работает (см. ~/.ssh/config и ssh-agent)"
    fi
    echo "    $REMOTE_INFO"
    # Грубо сверим архитектуру.
    REMOTE_ARCH=$(echo "$REMOTE_INFO" | sed -n 's/.*arch=\([^;]*\).*/\1/p')
    case "$REMOTE_ARCH" in
        x86_64) EXPECTED=amd64 ;;
        aarch64|arm64) EXPECTED=arm64 ;;
        *) warn "неизвестная архитектура сервера '$REMOTE_ARCH' — продолжаю на свой страх"; EXPECTED="$ARCH" ;;
    esac
    if [[ "$EXPECTED" != "$ARCH" ]]; then
        warn "архитектура сервера '$REMOTE_ARCH' не совпадает с --arch '$ARCH' (ожидалось $EXPECTED). Бот может не запуститься."
    fi
    success "сервер доступен"
fi

# ============================================================
#  Фаза 4: scp бинарника и unit-файла
# ============================================================
info "Фаза 4/6: копирую артефакты на ${SSH_HOST}"
if $DRY_RUN; then
    echo -e "${DIM}    \$ scp $BIN_OUT ${SSH_HOST}:/tmp/${BIN_NAME}.new${RESET}"
    echo -e "${DIM}    \$ scp $UNIT_SRC ${SSH_HOST}:/tmp/${UNIT_NAME}.new${RESET}"
else
    scp -q "$BIN_OUT"  "${SSH_HOST}:/tmp/${BIN_NAME}.new"
    scp -q "$UNIT_SRC" "${SSH_HOST}:/tmp/${UNIT_NAME}.new"
    success "артефакты на /tmp/${BIN_NAME}.new и /tmp/${UNIT_NAME}.new"
fi

# ============================================================
#  Фаза 5: установка на сервере (один ssh-блок)
# ============================================================
info "Фаза 5/6: установка на сервере (бинарник, юнит, linger)"
NEED_CONFIG_FLAG_FILE="/tmp/.sibsutis-bot-deploy.need-config"
ssh_run <<REMOTE
    set -e
    BIN_NEW=/tmp/${BIN_NAME}.new
    UNIT_NEW=/tmp/${UNIT_NAME}.new
    NEED_CONFIG_FLAG=${NEED_CONFIG_FLAG_FILE}

    mkdir -p "\$HOME/.local/bin" "\$HOME/.config/systemd/user" "\$HOME/.config/sibsutis-schedule"

    install -m 755 "\$BIN_NEW"  "\$HOME/.local/bin/${BIN_NAME}"
    install -m 644 "\$UNIT_NEW" "\$HOME/.config/systemd/user/${UNIT_NAME}"
    rm -f "\$BIN_NEW" "\$UNIT_NEW"

    # Пустой config.txt при первой установке — пользователь сам впишет секреты.
    if [ ! -f "\$HOME/.config/sibsutis-schedule/config.txt" ]; then
        touch "\$HOME/.config/sibsutis-schedule/config.txt"
        chmod 600 "\$HOME/.config/sibsutis-schedule/config.txt"
        touch "\$NEED_CONFIG_FLAG"
    else
        rm -f "\$NEED_CONFIG_FLAG"
    fi

    # Linger — чтобы юнит выживал logout.
    if ! loginctl show-user "\$(whoami)" 2>/dev/null | grep -q '^Linger=yes'; then
        echo "linger не включён — включаю"
        if ! loginctl enable-linger "\$(whoami)" 2>/dev/null; then
            sudo -A loginctl enable-linger "\$(whoami)" 2>/dev/null \
                || sudo loginctl enable-linger "\$(whoami)" \
                || { echo "не удалось включить linger автоматически. Запусти вручную:"; \
                     echo "  sudo loginctl enable-linger \$(whoami)"; exit 0; }
        fi
        echo "linger включён"
    else
        echo "linger уже включён"
    fi

    systemctl --user daemon-reload
REMOTE
success "сервер настроен"

# Проверяем, был ли создан пустой config.txt.
NEED_CONFIG=false
if ! $DRY_RUN; then
    # NEED_CONFIG_FLAG_FILE — путь, который мы сами задали выше; намеренно
    # подставляем его на клиенте, чтобы команда на сервере была проще.
    # shellcheck disable=SC2029
    if ssh "$SSH_HOST" "test -f $NEED_CONFIG_FLAG_FILE && rm -f $NEED_CONFIG_FLAG_FILE && echo yes" 2>/dev/null | grep -q yes; then
        NEED_CONFIG=true
    fi
fi

# ============================================================
#  Фаза 6: запуск/перезапуск сервиса
# ============================================================
if $NO_RESTART; then
    info "Фаза 6/6: пропускаю restart (--no-restart)"
else
    info "Фаза 6/6: запуск/перезапуск сервиса"
    ssh_run <<REMOTE
        set -e
        if systemctl --user is-active --quiet ${UNIT_NAME}; then
            systemctl --user restart ${UNIT_NAME}
            echo "сервис перезапущен"
        else
            systemctl --user enable --now ${UNIT_NAME}
            echo "сервис включён и запущен"
        fi
        sleep 1
        systemctl --user --no-pager status ${UNIT_NAME} | head -12 || true
        echo
        echo "--- последние логи ---"
        journalctl --user -u ${UNIT_NAME} -n 8 --no-pager || true
REMOTE
    success "готово"
fi

# ============================================================
#  Финальное сообщение
# ============================================================
echo
echo -e "${BOLD}Деплой завершён.${RESET}"
echo -e "  host:   ${BOLD}${SSH_HOST}${RESET}"
echo -e "  arch:   ${BOLD}linux/${ARCH}${RESET}"
echo -e "  unit:   ${BOLD}${UNIT_NAME}${RESET} (user-level)"

if $NEED_CONFIG; then
    echo
    warn "config.txt создан пустым — впиши секреты и перезапусти юнит:"
    cat <<EOF
    ssh $SSH_HOST 'nano ~/.config/sibsutis-schedule/config.txt'
    # внутри:
    #   login=<твой логин my.sibsutis.ru>
    #   password=<твой пароль>
    #   group=ИКС-531
    #   telegram_token=<токен от @BotFather>
    #   # cycle_anchor=2025-09-01    # для /today (опц.)
    ssh $SSH_HOST 'systemctl --user restart $UNIT_NAME'
EOF
fi

echo
info "Диагностика:"
echo "    ssh $SSH_HOST 'systemctl --user status $UNIT_NAME'"
echo "    ssh $SSH_HOST 'journalctl --user -u $UNIT_NAME -f'"
