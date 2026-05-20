#!/bin/bash
# =============================================================
#  sibsutis-schedule-web — установка на этот сервер
#
#  Запускать НА СЕРВЕРЕ. Скрипт качает готовый бинарник из GitHub
#  Releases, ставит user-level systemd-юнит, включает linger и
#  запускает сервис. Go, git и прочее на сервере не нужны.
#
#  Использование:
#    ./deploy.sh                     установить последний релиз
#    ./deploy.sh --version v0.3.0    установить конкретный тег
#    ./deploy.sh --no-restart        поставить, но не запускать
#    ./deploy.sh --uninstall         остановить и удалить
#
#  Идемпотентно — повторный запуск просто обновляет бинарник.
# =============================================================

set -euo pipefail

# ─── Цвета ────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

info()    { echo -e "${CYAN}[INFO]${RESET}  $1"; }
success() { echo -e "${GREEN}[ OK ]${RESET}  $1"; }
warn()    { echo -e "${YELLOW}[WARN]${RESET}  $1"; }
error()   { echo -e "${RED}[ ERR]${RESET}  $1" >&2; exit 1; }

print_help() {
    cat <<'HELP'
sibsutis-schedule-web — установка на сервер

Использование:
  ./deploy.sh [--version <tag>] [--no-restart] [--uninstall]

Опции:
  --version <tag>   установить конкретный релиз (напр. v0.3.0); default — latest
  --no-restart      поставить бинарник и юнит, но не запускать сервис
  --uninstall       остановить юнит, удалить unit-файл и бинарник
                    (config.txt и история не трогаются)
  -h, --help        эта справка

Что делает: качает sibsutis-schedule-web-linux-<arch> из GitHub Releases,
кладёт в ~/.local/bin, ставит ~/.config/systemd/user/-юнит, включает linger,
запускает сервис. Подробности — в ./README.md.
HELP
}

# ─── Константы ────────────────────────────────────────────────
REPO="BLXCKBXXST/sibsutis-schedule"
BIN_NAME="sibsutis-schedule-web"
UNIT_NAME="sibsutis-schedule-web.service"
BIN_DIR="$HOME/.local/bin"
UNIT_DIR="$HOME/.config/systemd/user"
CONFIG_DIR="$HOME/.config/sibsutis-schedule"
CONFIG_FILE="$CONFIG_DIR/config.txt"

RELEASE_TAG="latest"
NO_RESTART=false
UNINSTALL=false

# ─── Аргументы ────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
    case "$1" in
        --version)    RELEASE_TAG="$2"; shift 2 ;;
        --no-restart) NO_RESTART=true;  shift ;;
        --uninstall)  UNINSTALL=true;   shift ;;
        -h|--help)    print_help; exit 0 ;;
        *) error "Неизвестный аргумент: $1 (см. --help)" ;;
    esac
done

# ─── Uninstall ────────────────────────────────────────────────
if $UNINSTALL; then
    info "Удаляю сервис и бинарник"
    systemctl --user disable --now "$UNIT_NAME" 2>/dev/null || true
    rm -f "$UNIT_DIR/$UNIT_NAME" "$BIN_DIR/$BIN_NAME"
    systemctl --user daemon-reload 2>/dev/null || true
    success "юнит и бинарник удалены"
    echo "  config.txt и история не тронуты. Если нужно дочистить:"
    echo "    rm -rf $CONFIG_DIR $HOME/.local/share/sibsutis-schedule"
    echo "    loginctl disable-linger $(whoami)"
    exit 0
fi

# ─── 1. Архитектура ───────────────────────────────────────────
info "Определяю архитектуру"
case "$(uname -m)" in
    x86_64)        ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) error "архитектура $(uname -m) не поддерживается (только amd64 / arm64)" ;;
esac
success "linux/$ARCH"

# ─── 2. Скачивание бинарника ──────────────────────────────────
command -v curl >/dev/null 2>&1 || error "не найден 'curl' — поставь его (apt install curl)"

ASSET="${BIN_NAME}-linux-${ARCH}"
if [[ "$RELEASE_TAG" == "latest" ]]; then
    URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"
else
    URL="https://github.com/${REPO}/releases/download/${RELEASE_TAG}/${ASSET}"
fi

info "Скачиваю $URL"
TMP_BIN="$(mktemp)"
trap 'rm -f "$TMP_BIN"' EXIT
if ! curl -fL --retry 3 --retry-delay 2 -o "$TMP_BIN" "$URL"; then
    error "не удалось скачать бинарник.
       Проверь, что релиз ${RELEASE_TAG} опубликован:
       https://github.com/${REPO}/releases"
fi
[[ -s "$TMP_BIN" ]] || error "скачан пустой файл"
success "скачано ($(du -h "$TMP_BIN" | cut -f1))"

# ─── 3. Установка бинарника ───────────────────────────────────
mkdir -p "$BIN_DIR"
install -m 755 "$TMP_BIN" "$BIN_DIR/$BIN_NAME"
success "бинарник: $BIN_DIR/$BIN_NAME"

# ─── 4. systemd-юнит ──────────────────────────────────────────
mkdir -p "$UNIT_DIR"
cat > "$UNIT_DIR/$UNIT_NAME" <<UNIT
[Unit]
Description=SibSUTI schedule web server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%h/.local/bin/${BIN_NAME} --config %h/.config/sibsutis-schedule/config.txt
Restart=on-failure
RestartSec=10s

[Install]
WantedBy=default.target
UNIT
success "юнит: $UNIT_DIR/$UNIT_NAME"

# ─── 5. config.txt ────────────────────────────────────────────
mkdir -p "$CONFIG_DIR"
NEED_CONFIG=false
if [[ ! -f "$CONFIG_FILE" ]]; then
    touch "$CONFIG_FILE"
    chmod 600 "$CONFIG_FILE"
    NEED_CONFIG=true
    warn "создан пустой $CONFIG_FILE — впиши секреты (см. ниже)"
fi

# ─── 6. Linger ────────────────────────────────────────────────
if loginctl show-user "$(whoami)" 2>/dev/null | grep -q '^Linger=yes'; then
    info "linger уже включён"
else
    info "включаю linger (чтобы сервис жил без активного логина)"
    if loginctl enable-linger "$(whoami)" 2>/dev/null; then
        success "linger включён"
    elif sudo loginctl enable-linger "$(whoami)"; then
        success "linger включён (через sudo)"
    else
        warn "не удалось включить linger — запусти вручную:"
        echo "    sudo loginctl enable-linger $(whoami)"
    fi
fi

# ─── 7. Запуск ────────────────────────────────────────────────
systemctl --user daemon-reload

if $NO_RESTART; then
    info "--no-restart: сервис не запускаю"
else
    if systemctl --user is-active --quiet "$UNIT_NAME"; then
        systemctl --user restart "$UNIT_NAME"
        success "сервис перезапущен"
    else
        systemctl --user enable --now "$UNIT_NAME"
        success "сервис включён и запущен"
    fi
    sleep 1
    systemctl --user --no-pager status "$UNIT_NAME" | head -10 || true
fi

# ─── Финал ────────────────────────────────────────────────────
echo
echo -e "${BOLD}Установка завершена.${RESET}"

if $NEED_CONFIG; then
    echo
    warn "config.txt пустой — впиши логин/пароль и перезапусти сервис:"
    cat <<EOF
    nano $CONFIG_FILE
    # внутри:
    #   login=<логин my.sibsutis.ru>
    #   password=<пароль>
    #   group=ИКС-531              # опц., для кнопки «моё расписание»
    #   web_listen_addr=:8080      # опц.
    systemctl --user restart $UNIT_NAME
EOF
fi

cat <<EOF

Проверка:
  curl -s localhost:8080/healthz          # должно ответить OK
  systemctl --user status $UNIT_NAME
  journalctl --user -u $UNIT_NAME -f

HTTPS — пробрось субдомен на сервис через Caddy:
  schedule.<домен> {
      reverse_proxy 127.0.0.1:8080
  }
EOF
