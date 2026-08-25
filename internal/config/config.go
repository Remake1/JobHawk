package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/joho/godotenv"
)

type Config struct {
	TelegramBotToken string
	TelegramChatID   int64
	DatabaseURL      string
	LogLevel         slog.Level
	DailyRunHour     int
	DailyTimezone    *time.Location
}

func Load() (Config, error) {
	// Load local development configuration without replacing values that were
	// explicitly exported by the shell or injected by the runtime.
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("load .env: %w", err)
	}

	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if token == "" {
		return Config{}, errors.New("TELEGRAM_BOT_TOKEN is required")
	}
	chatID, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID")), 10, 64)
	if err != nil || chatID == 0 {
		return Config{}, errors.New("TELEGRAM_CHAT_ID must be a non-zero integer")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}

	level, err := parseLogLevel(os.Getenv("LOG_LEVEL"))
	if err != nil {
		return Config{}, err
	}
	dailyRunHour, err := parseDailyRunHour(os.Getenv("DAILY_RUN_HOUR"))
	if err != nil {
		return Config{}, err
	}
	timezoneName := strings.TrimSpace(os.Getenv("DAILY_TIMEZONE"))
	if timezoneName == "" {
		timezoneName = "America/Chicago"
	}
	dailyTimezone, err := time.LoadLocation(timezoneName)
	if err != nil {
		return Config{}, fmt.Errorf("DAILY_TIMEZONE must be a valid IANA timezone: %w", err)
	}

	return Config{
		TelegramBotToken: token,
		TelegramChatID:   chatID,
		DatabaseURL:      databaseURL,
		LogLevel:         level,
		DailyRunHour:     dailyRunHour,
		DailyTimezone:    dailyTimezone,
	}, nil
}

func parseDailyRunHour(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 9, nil
	}
	hour, err := strconv.Atoi(value)
	if err != nil || hour < 0 || hour > 23 {
		return 0, errors.New("DAILY_RUN_HOUR must be an integer from 0 through 23")
	}
	return hour, nil
}

func parseLogLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, errors.New("LOG_LEVEL must be one of: debug, info, warn, error")
	}
}
