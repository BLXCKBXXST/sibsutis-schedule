package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

// errBadToken — все формальные ошибки декодирования токена. Снаружи
// преобразуется в 404, чтобы не намекать клиенту, валиден ли target.
var errBadToken = errors.New("bad ics token")

// signICSTarget кодирует target в самопроверяющий токен webcal-подписки.
// Структура: base64url("<urlType>/<query>|<hmac>"), где hmac =
// HMAC-SHA256(secret, "<urlType>/<query>"), сокращённый до 16 байт.
//
// Зачем токен, а не открытый URL: с открытым URL любой может подписаться
// на любого студента — мы только хотим, чтобы это сделал тот, кто реально
// открыл свою страницу. Токен подписан секретом сервера и не раскрывает
// query напрямую (нужно сначала декодировать base64), но он не приватен —
// при шеринге URL получатель тоже сможет подписаться. Это сознательный
// trade-off: основная цель — не подбор URL'ов перебором, а не приватность.
func signICSTarget(secret []byte, t model.Target) string {
	payload := urlTypeOf(t) + "/" + t.Query
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	sig := mac.Sum(nil)[:16] // 128 бит — более чем достаточно
	raw := payload + "|" + base64.RawURLEncoding.EncodeToString(sig)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// verifyICSToken декодирует токен и проверяет HMAC. Возвращает target или
// errBadToken (структурно битый, неверная подпись, неизвестный type).
func verifyICSToken(secret []byte, token string) (model.Target, error) {
	rawBytes, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return model.Target{}, errBadToken
	}
	raw := string(rawBytes)
	payload, gotSig, ok := strings.Cut(raw, "|")
	if !ok {
		return model.Target{}, errBadToken
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	want := mac.Sum(nil)[:16]
	gotSigBytes, err := base64.RawURLEncoding.DecodeString(gotSig)
	if err != nil || !hmac.Equal(gotSigBytes, want) {
		return model.Target{}, errBadToken
	}
	typ, q, ok := strings.Cut(payload, "/")
	if !ok || q == "" {
		return model.Target{}, errBadToken
	}
	tt, ok := urlTypeToTargetType(typ)
	if !ok {
		return model.Target{}, errBadToken
	}
	return model.Target{Type: tt, Query: q}, nil
}

// urlTypeOf — обратная функция к urlTypeToTargetType. Возвращает строку,
// которую увидит пользователь в URL ("group" / "teacher" / "room").
func urlTypeOf(t model.Target) string {
	if t.Type == model.TypeStudent {
		return "group"
	}
	return string(t.Type)
}
