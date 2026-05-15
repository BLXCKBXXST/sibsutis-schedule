package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadTargetFromGroup(t *testing.T) {
	cfg, err := Load(writeConfig(t, "login=a\npassword=b\ngroup=ИБ-211\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DefaultTarget == nil {
		t.Fatal("DefaultTarget = nil, ожидался target по group")
	}
	if cfg.DefaultTarget.Type != model.TypeStudent || cfg.DefaultTarget.Query != "ИБ-211" {
		t.Errorf("DefaultTarget = %+v", *cfg.DefaultTarget)
	}
}

func TestLoadTargetFromTeacher(t *testing.T) {
	cfg, err := Load(writeConfig(t, "login=a\npassword=b\nteacher=Иванов И.И.\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DefaultTarget == nil || cfg.DefaultTarget.Type != model.TypeTeacher {
		t.Errorf("DefaultTarget = %+v", cfg.DefaultTarget)
	}
}

func TestLoadNoTarget(t *testing.T) {
	cfg, err := Load(writeConfig(t, "login=a\npassword=b\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DefaultTarget != nil {
		t.Errorf("DefaultTarget = %+v, ожидался nil", *cfg.DefaultTarget)
	}
}

func TestLoadMultipleTargetsError(t *testing.T) {
	_, err := Load(writeConfig(t, "login=a\npassword=b\ngroup=ИБ-211\nroom=1-101\n"))
	if err == nil {
		t.Error("ожидалась ошибка при одновременно заданных group и room")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.txt")
	content := "# комментарий\n" +
		"login = student123 \n" +
		"password=secret=with=equals\n" +
		"\n" +
		"schedule_url=https://example.com/sched/\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Login != "student123" {
		t.Errorf("Login = %q, want %q", cfg.Login, "student123")
	}
	if cfg.Password != "secret=with=equals" {
		t.Errorf("Password = %q, want %q", cfg.Password, "secret=with=equals")
	}
	if cfg.ScheduleURL != "https://example.com/sched/" {
		t.Errorf("ScheduleURL = %q", cfg.ScheduleURL)
	}
	if cfg.AuthURL != DefaultAuthURL {
		t.Errorf("AuthURL = %q, want default %q", cfg.AuthURL, DefaultAuthURL)
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.txt")
	if err := os.WriteFile(path, []byte("login=a\npassword=b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ScheduleURL != DefaultScheduleURL {
		t.Errorf("ScheduleURL = %q, want default", cfg.ScheduleURL)
	}
}

func TestLoadMissingCredentials(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.txt")
	if err := os.WriteFile(path, []byte("login=onlylogin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("ожидалась ошибка из-за отсутствия password")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "нет-такого.txt")); err == nil {
		t.Error("ожидалась ошибка для несуществующего файла")
	}
}

func TestParseFileBadLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.txt")
	if err := os.WriteFile(path, []byte("login=a\nмусорная строка\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("ожидалась ошибка для строки без '='")
	}
}
