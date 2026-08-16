package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"jobhawk/internal/config"
	"jobhawk/internal/database/db"
	"jobhawk/internal/greenhouse"
	"jobhawk/internal/searchqueries"
	telegrambot "jobhawk/internal/telegram"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	logger := newLogger(cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("configure database pool", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		logger.Error("connect to database", "error", err)
		os.Exit(1)
	}

	queryStore := searchqueries.NewStore(db.New(pool))
	greenhouseClient := greenhouse.NewClient(nil)
	bot, err := telegrambot.New(
		cfg.TelegramBotToken,
		cfg.TelegramChatID,
		queryStore,
		greenhouseClient,
		logger,
	)
	if err != nil {
		logger.Error("create bot", "error", err)
		os.Exit(1)
	}

	if err := bot.Run(ctx); err != nil {
		logger.Error("bot stopped with an error", "error", err)
		os.Exit(1)
	}
}

func newLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}
