package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", " token ")
	t.Setenv("TELEGRAM_CHAT_ID", "42")
	t.Setenv("DATABASE_URL", "postgres://localhost/jobhawk")
	t.Setenv("LOG_LEVEL", "debug")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.TelegramBotToken != "token" {
		t.Errorf("TelegramBotToken = %q", cfg.TelegramBotToken)
	}
	if cfg.TelegramChatID != 42 || cfg.DatabaseURL != "postgres://localhost/jobhawk" {
		t.Errorf("Load() = %+v", cfg)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v", cfg.LogLevel)
	}
}

func TestLoadRequiresToken(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_CHAT_ID", "42")
	t.Setenv("DATABASE_URL", "postgres://localhost/jobhawk")

	if _, err := Load(); err == nil {
		t.Fatal("Load() expected an error")
	}
}

func TestLoadReadsDotEnv(t *testing.T) {
	dir := t.TempDir()
	dotEnv := []byte("TELEGRAM_BOT_TOKEN=from-dotenv\nTELEGRAM_CHAT_ID=42\nDATABASE_URL=postgres://localhost/jobhawk\nLOG_LEVEL=warn\n")
	if err := os.WriteFile(filepath.Join(dir, ".env"), dotEnv, 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDir) })

	restoreEnv(t, "TELEGRAM_BOT_TOKEN")
	restoreEnv(t, "TELEGRAM_CHAT_ID")
	restoreEnv(t, "DATABASE_URL")
	restoreEnv(t, "LOG_LEVEL")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.TelegramBotToken != "from-dotenv" || cfg.TelegramChatID != 42 || cfg.DatabaseURL != "postgres://localhost/jobhawk" || cfg.LogLevel != slog.LevelWarn {
		t.Fatalf("Load() = %+v", cfg)
	}
}

func restoreEnv(t *testing.T, key string) {
	t.Helper()
	value, exists := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if exists {
			_ = os.Setenv(key, value)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}
