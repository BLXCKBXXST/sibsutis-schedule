package web

import (
	"testing"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

func TestICSTokenRoundtrip(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	target := model.Target{Type: model.TypeStudent, Query: "ИКС-531"}

	tok := signICSTarget(secret, target)
	if tok == "" {
		t.Fatal("пустой токен")
	}

	got, err := verifyICSToken(secret, tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.Type != target.Type || got.Query != target.Query {
		t.Errorf("roundtrip = %+v, want %+v", got, target)
	}
}

func TestICSTokenRejectsBadSignature(t *testing.T) {
	tok := signICSTarget([]byte("0123456789abcdef0123456789abcdef"),
		model.Target{Type: model.TypeStudent, Query: "ИКС-531"})

	// Другой секрет — другой HMAC.
	if _, err := verifyICSToken([]byte("ffffffffffffffffffffffffffffffff"), tok); err == nil {
		t.Error("ожидалась ошибка при чужом секрете")
	}

	// Битый base64.
	if _, err := verifyICSToken([]byte("any"), "!@#$%"); err == nil {
		t.Error("ожидалась ошибка при битом base64")
	}

	// Подделанный payload без подписи.
	if _, err := verifyICSToken([]byte("any"), "abc"); err == nil {
		t.Error("ожидалась ошибка при отсутствии '|'")
	}
}

func TestICSTokenDifferentTargetsDifferentTokens(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	a := signICSTarget(secret, model.Target{Type: model.TypeStudent, Query: "ИКС-531"})
	b := signICSTarget(secret, model.Target{Type: model.TypeStudent, Query: "ИКС-532"})
	if a == b {
		t.Errorf("разные target дают одинаковый токен")
	}
}
