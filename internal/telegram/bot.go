package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

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
	ListGreenhouse(context.Context) ([]searchqueries.GreenhouseQuery, error)
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
		AllowedUpdates: []string{"message"},
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
			if update.Message != nil {
				b.handleMessage(ctx, update.Message)
			}
		}
	}
}

func (b *Bot) handleMessage(ctx context.Context, message *telego.Message) {
	command, args, ok := parseCommand(message.Text)
	if !ok {
		return
	}
	if message.Chat.ID != b.allowedChatID {
		b.logger.Warn("ignored command from unauthorized chat", "chat_id", message.Chat.ID, "command", command)
		return
	}

	var response string
	switch command {
	case "/start", "/subscribe":
		b.subscribers.Add(message.Chat.ID)
		response = "You're subscribed to new job alerts. Use /status to check your subscription or /stop to pause alerts."
	case "/stop", "/unsubscribe":
		b.subscribers.Remove(message.Chat.ID)
		response = "Job alerts are paused. Use /subscribe whenever you want to resume them."
	case "/status":
		if b.subscribers.Contains(message.Chat.ID) {
			response = "Job alerts are active for this chat."
		} else {
			response = "Job alerts are not active. Use /subscribe to enable them."
		}
	case "/help":
		response = greenhouseHelp()
	case "/greenhouse":
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
		response = fmt.Sprintf(
			"Saved Greenhouse search %q.\nBoard: %s\nLocation: %s\nTitle must contain: %s\n\nRun /search %s to check it now.",
			query.Name,
			query.Filters.BoardToken,
			valueOrAny(query.Filters.Location),
			wordsOrAny(query.Filters.TitleWords),
			query.Name,
		)
	case "/queries":
		queries, err := b.queryStore.ListGreenhouse(ctx)
		if err != nil {
			b.logger.Error("list search queries", "error", err)
			response = "Could not list search queries: " + err.Error()
			break
		}
		response = formatQueries(queries)
	case "/search":
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
		response = formatSearchResults(query.Name, found)
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

func greenhouseHelp() string {
	return "Commands:\n/greenhouse - save or update a Greenhouse search\n/queries - list saved searches\n/search <name> - run one saved search now\n/subscribe - enable future alert delivery\n/stop - pause future alert delivery\n/status - show alert delivery status\n/help - show this message\n\n" + greenhouseUsage()
}

func formatQueries(queries []searchqueries.GreenhouseQuery) string {
	if len(queries) == 0 {
		return "No Greenhouse searches are saved.\n\n" + greenhouseUsage()
	}
	var builder strings.Builder
	builder.WriteString("Saved Greenhouse searches:")
	for _, query := range queries {
		fmt.Fprintf(&builder, "\n\n%s\n%s · %s\nTitle: %s", query.Name, query.Filters.BoardToken, valueOrAny(query.Filters.Location), wordsOrAny(query.Filters.TitleWords))
	}
	return builder.String()
}

func formatSearchResults(name string, found []jobs.Job) string {
	if len(found) == 0 {
		return fmt.Sprintf("Search %q found no matching open jobs.", name)
	}

	const maxResults = 10
	var builder strings.Builder
	fmt.Fprintf(&builder, "Search %q found %d matching open job(s):", name, len(found))
	for i, job := range found {
		if i == maxResults {
			fmt.Fprintf(&builder, "\n\n…and %d more.", len(found)-maxResults)
			break
		}
		fmt.Fprintf(&builder, "\n\n%s\n%s\n%s", job.Title, job.Location, job.URL)
	}
	result := builder.String()
	runes := []rune(result)
	if len(runes) > 4096 {
		return string(runes[:4093]) + "..."
	}
	return result
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
