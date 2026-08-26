package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"jobhawk/internal/ashby"
	"jobhawk/internal/config"
	"jobhawk/internal/daily"
	"jobhawk/internal/database/db"
	"jobhawk/internal/greenhouse"
	"jobhawk/internal/hourly"
	"jobhawk/internal/jobstore"
	"jobhawk/internal/searchqueries"
	telegrambot "jobhawk/internal/telegram"
	"jobhawk/internal/workday"
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

	databaseQueries := db.New(pool)
	queryStore := searchqueries.NewStore(databaseQueries)
	hourlyStore := hourly.NewStore(databaseQueries)
	discoveredJobStore := jobstore.NewStore(databaseQueries)
	greenhouseClient := greenhouse.NewClient(nil)
	workdayClient := workday.NewClient(nil)
	ashbyClient := ashby.NewClient(nil)
	bot, err := telegrambot.NewWithProviders(
		cfg.TelegramBotToken,
		cfg.TelegramChatID,
		queryStore,
		greenhouseClient,
		workdayClient,
		ashbyClient,
		logger,
	)
	if err != nil {
		logger.Error("create bot", "error", err)
		os.Exit(1)
	}
	dailyRunner := daily.NewRunner(
		queryStore,
		discoveredJobStore,
		bot,
		ashbyClient,
		greenhouseClient,
		workdayClient,
	)
	bot.SetDailyRunner(dailyRunner)
	bot.SetHourlySubscriptions(hourlyStore, cfg.DailyTimezone)
	scheduler := daily.NewScheduler(dailyRunner, cfg.DailyRunHour, cfg.DailyTimezone, logger)
	hourlyRunner := hourly.NewRunner(
		hourlyStore,
		queryStore,
		bot,
		ashbyClient,
		greenhouseClient,
		workdayClient,
		cfg.DailyTimezone,
	)
	hourlyScheduler := hourly.NewScheduler(hourlyRunner, logger)

	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error { return bot.Run(groupCtx) })
	group.Go(func() error { return scheduler.Run(groupCtx) })
	group.Go(func() error { return hourlyScheduler.Run(groupCtx) })
	if err := group.Wait(); err != nil {
		logger.Error("application stopped with an error", "error", err)
		os.Exit(1)
	}
}

func newLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}
