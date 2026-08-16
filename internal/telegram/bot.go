package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"jobhawk/internal/greenhouse"
	"jobhawk/internal/jobs"
	"jobhawk/internal/searchqueries"
	"jobhawk/internal/subscribers"
)

type queryStore interface {
	SaveGreenhouse(context.Context, string, greenhouse.Filters) (searchqueries.GreenhouseQuery, error)
	GetGreenhouse(context.Context, string) (searchqueries.GreenhouseQuery, error)
	GetGreenhouseByID(context.Context, int64) (searchqueries.GreenhouseQuery, error)
	ListGreenhouse(context.Context) ([]searchqueries.GreenhouseQuery, error)
	DeleteGreenhouse(context.Context, int64) (bool, error)
}

type greenhouseSearcher interface {
	Search(context.Context, greenhouse.Filters) ([]jobs.Job, error)
}

type Bot struct {
	api                *telego.Bot
	logger             *slog.Logger
	allowedChatID      int64
	queryStore         queryStore
	greenhouseSearcher greenhouseSearcher
	subscribers        *subscribers.Store
	creationMu         sync.Mutex
	creation           *creationSession
}

func New(
	token string,
	allowedChatID int64,
	queryStore queryStore,
	greenhouseSearcher greenhouseSearcher,
	logger *slog.Logger,
) (*Bot, error) {
	api, err := telego.NewBot(token)
	if err != nil {
		return nil, fmt.Errorf("initialize Telegram client: %w", err)
	}

	return &Bot{
		api:                api,
		logger:             logger,
		allowedChatID:      allowedChatID,
		queryStore:         queryStore,
		greenhouseSearcher: greenhouseSearcher,
		subscribers:        subscribers.NewStore(),
	}, nil
}

func (b *Bot) Run(ctx context.Context) error {
	me, err := b.api.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("get bot identity: %w", err)
	}
	b.logger.Info("bot started", "username", me.Username)

	updates, err := b.api.UpdatesViaLongPolling(ctx, &telego.GetUpdatesParams{
		AllowedUpdates: []string{"message", "callback_query"},
	})
	if err != nil {
		return fmt.Errorf("start long polling: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			b.logger.Info("bot stopped")
			return nil
		case update, ok := <-updates:
			if !ok {
				if err := ctx.Err(); err != nil {
					return nil
				}
				return errors.New("Telegram update stream closed unexpectedly")
			}
			if update.CallbackQuery != nil {
				b.handleCallbackQuery(ctx, update.CallbackQuery)
			} else if update.Message != nil {
				b.handleMessage(ctx, update.Message)
			}
		}
	}
}

func (b *Bot) handleMessage(ctx context.Context, message *telego.Message) {
	if message.Chat.ID != b.allowedChatID {
		b.logger.Warn("ignored message from unauthorized chat", "chat_id", message.Chat.ID)
		return
	}
	text := strings.TrimSpace(message.Text)
	if text == "" {
		return
	}
	if !strings.HasPrefix(text, "/") && b.hasCreationSession() {
		b.handleCreationInput(ctx, message.Chat.ID, text)
		return
	}

	command, args, ok := parseCommand(text)
	if !ok {
		return
	}

	var response string
	switch command {
	case "/start":
		b.subscribers.Add(message.Chat.ID)
		b.clearCreationSession()
		b.sendScreen(ctx, message.Chat.ID, mainMenuScreen())
		return
	case "/menu", "/help":
		b.clearCreationSession()
		b.sendScreen(ctx, message.Chat.ID, mainMenuScreen())
		return
	case "/subscribe":
		b.subscribers.Add(message.Chat.ID)
		response = "You're subscribed to future job alerts. Use /menu to manage searches."
	case "/stop", "/unsubscribe":
		b.subscribers.Remove(message.Chat.ID)
		response = "Job alerts are paused. Use /subscribe whenever you want to resume them."
	case "/status":
		if b.subscribers.Contains(message.Chat.ID) {
			response = "Job alerts are active for this chat."
		} else {
			response = "Job alerts are not active. Use /subscribe to enable them."
		}
	case "/greenhouse":
		if strings.TrimSpace(args) == "" {
			session := b.beginCreationSession()
			b.sendScreen(ctx, message.Chat.ID, creationPromptScreen(session, ""))
			return
		}
		name, filters, err := parseGreenhouseArgs(args)
		if err != nil {
			response = err.Error() + "\n\n" + greenhouseUsage()
			break
		}
		query, err := b.queryStore.SaveGreenhouse(ctx, name, filters)
		if err != nil {
			b.logger.Error("save Greenhouse query", "name", name, "error", err)
			response = "Could not save the Greenhouse search: " + err.Error()
			break
		}
		b.clearCreationSession()
		b.sendScreen(ctx, message.Chat.ID, queryDetailScreen(query))
		return
	case "/queries":
		b.clearCreationSession()
		b.sendQueryList(ctx, message.Chat.ID)
		return
	case "/search":
		b.clearCreationSession()
		if strings.TrimSpace(args) == "" {
			response = "Query name is required. Usage: /search <name>"
			break
		}
		query, err := b.queryStore.GetGreenhouse(ctx, args)
		if err != nil {
			b.logger.Error("load Greenhouse query", "name", args, "error", err)
			response = "Could not load that Greenhouse search: " + err.Error()
			break
		}
		found, err := b.greenhouseSearcher.Search(ctx, query.Filters)
		if err != nil {
			b.logger.Error("run Greenhouse search", "name", query.Name, "error", err)
			response = "Greenhouse search failed: " + err.Error()
			break
		}
		b.sendScreen(ctx, message.Chat.ID, searchResultsScreen(query, found))
		return
	case "/cancel":
		b.clearCreationSession()
		b.sendScreen(ctx, message.Chat.ID, mainMenuScreen())
		return
	default:
		return
	}

	if _, err := b.api.SendMessage(ctx, tu.Message(tu.ID(message.Chat.ID), response)); err != nil {
		b.logger.Error("send command response", "chat_id", message.Chat.ID, "error", err)
	}
}

func commandFromText(text string) (string, bool) {
	command, _, ok := parseCommand(text)
	return command, ok
}

func parseCommand(text string) (command, args string, ok bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", "", false
	}

	command = strings.SplitN(strings.ToLower(fields[0]), "@", 2)[0]
	args = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), fields[0]))
	return command, args, true
}

func parseGreenhouseArgs(args string) (string, greenhouse.Filters, error) {
	parts := strings.Split(args, "|")
	if len(parts) != 4 {
		return "", greenhouse.Filters{}, errors.New("expected four fields separated by |")
	}
	name := strings.TrimSpace(parts[0])
	words := strings.Split(parts[3], ",")
	if !strings.Contains(parts[3], ",") {
		words = strings.Fields(parts[3])
	}
	filters, err := (greenhouse.Filters{
		BoardToken: strings.TrimSpace(parts[1]),
		Location:   strings.TrimSpace(parts[2]),
		TitleWords: words,
	}).Normalize()
	if name == "" {
		return "", greenhouse.Filters{}, errors.New("query name is required")
	}
	if err != nil {
		return "", greenhouse.Filters{}, err
	}
	return name, filters, nil
}

func greenhouseUsage() string {
	return "Usage:\n/greenhouse <name> | <board token> | <location> | <comma-separated title words>\n\nExample:\n/greenhouse Point72 SWE Internship 2027 | point72 | Warsaw, Poland | 2027, Internship, Software"
}

func valueOrAny(value string) string {
	if value == "" {
		return "any location"
	}
	return value
}

func wordsOrAny(words []string) string {
	if len(words) == 0 {
		return "any title"
	}
	return strings.Join(words, ", ")
}

// Notify sends a normalized job opening to every subscribed chat.
func (b *Bot) Notify(ctx context.Context, job jobs.Job) {
	text := formatJob(job)
	for _, chatID := range b.subscribers.All() {
		if _, err := b.api.SendMessage(ctx, tu.Message(tu.ID(chatID), text)); err != nil {
			b.logger.Error("send job alert", "chat_id", chatID, "job_id", job.ID, "error", err)
		}
	}
}

func formatJob(job jobs.Job) string {
	parts := []string{"New job opening", job.Title + " at " + job.Company}
	if job.Location != "" {
		parts = append(parts, job.Location)
	}
	if job.URL != "" {
		parts = append(parts, job.URL)
	}
	return strings.Join(parts, "\n")
}
