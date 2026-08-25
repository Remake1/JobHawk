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

	"jobhawk/internal/ashby"
	"jobhawk/internal/greenhouse"
	"jobhawk/internal/jobs"
	"jobhawk/internal/searchqueries"
	"jobhawk/internal/subscribers"
	"jobhawk/internal/workday"
)

type queryStore interface {
	SaveAshby(context.Context, string, ashby.Filters) (searchqueries.AshbyQuery, error)
	SaveGreenhouse(context.Context, string, greenhouse.Filters) (searchqueries.GreenhouseQuery, error)
	SaveWorkday(context.Context, string, workday.Filters) (searchqueries.WorkdayQuery, error)
	Get(context.Context, string) (searchqueries.Query, error)
	GetByID(context.Context, int64) (searchqueries.Query, error)
	List(context.Context) ([]searchqueries.Query, error)
	Update(context.Context, int64, searchqueries.EditableFilters) (searchqueries.Query, error)
	Delete(context.Context, int64) (bool, error)
}

type ashbySearcher interface {
	Search(context.Context, ashby.Filters) ([]jobs.Job, error)
}

type greenhouseSearcher interface {
	Search(context.Context, greenhouse.Filters) ([]jobs.Job, error)
}

type workdaySearcher interface {
	Search(context.Context, workday.Filters) ([]jobs.Job, error)
}

type Bot struct {
	api                *telego.Bot
	logger             *slog.Logger
	allowedChatID      int64
	queryStore         queryStore
	ashbySearcher      ashbySearcher
	greenhouseSearcher greenhouseSearcher
	workdaySearcher    workdaySearcher
	subscribers        *subscribers.Store
	creationMu         sync.Mutex
	creation           *creationSession
	editMu             sync.Mutex
	edit               *editSession
}

func New(
	token string,
	allowedChatID int64,
	queryStore queryStore,
	greenhouseSearcher greenhouseSearcher,
	logger *slog.Logger,
) (*Bot, error) {
	return NewWithProviders(token, allowedChatID, queryStore, greenhouseSearcher, workday.NewClient(nil), ashby.NewClient(nil), logger)
}

func NewWithWorkday(
	token string,
	allowedChatID int64,
	queryStore queryStore,
	greenhouseSearcher greenhouseSearcher,
	workdaySearcher workdaySearcher,
	logger *slog.Logger,
) (*Bot, error) {
	return NewWithProviders(token, allowedChatID, queryStore, greenhouseSearcher, workdaySearcher, ashby.NewClient(nil), logger)
}

func NewWithProviders(
	token string,
	allowedChatID int64,
	queryStore queryStore,
	greenhouseSearcher greenhouseSearcher,
	workdaySearcher workdaySearcher,
	ashbySearcher ashbySearcher,
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
		ashbySearcher:      ashbySearcher,
		greenhouseSearcher: greenhouseSearcher,
		workdaySearcher:    workdaySearcher,
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
	if !strings.HasPrefix(text, "/") && b.hasEditInputSession() {
		b.handleEditInput(ctx, message.Chat.ID, text)
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
		b.clearEditSession()
		b.sendScreen(ctx, message.Chat.ID, mainMenuScreen())
		return
	case "/menu", "/help":
		b.clearCreationSession()
		b.clearEditSession()
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
		b.clearEditSession()
		if strings.TrimSpace(args) == "" {
			session := b.beginProviderCreation(searchqueries.SourceGreenhouse)
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
		b.sendScreen(ctx, message.Chat.ID, queryDetailScreen(queryFromGreenhouse(query)))
		return
	case "/ashby", "/ashbyhq":
		b.clearEditSession()
		if strings.TrimSpace(args) == "" {
			session := b.beginProviderCreation(searchqueries.SourceAshby)
			b.sendScreen(ctx, message.Chat.ID, creationPromptScreen(session, ""))
			return
		}
		name, filters, err := parseAshbyArgs(args)
		if err != nil {
			response = err.Error() + "\n\n" + ashbyUsage()
			break
		}
		saved, err := b.queryStore.SaveAshby(ctx, name, filters)
		if err != nil {
			b.logger.Error("save Ashby query", "name", name, "error", err)
			response = "Could not save the Ashby search: " + err.Error()
			break
		}
		b.clearCreationSession()
		b.sendScreen(ctx, message.Chat.ID, queryDetailScreen(queryFromAshby(saved)))
		return
	case "/workday":
		b.clearEditSession()
		if strings.TrimSpace(args) == "" {
			session := b.beginProviderCreation(searchqueries.SourceWorkday)
			b.sendScreen(ctx, message.Chat.ID, creationPromptScreen(session, ""))
			return
		}
		name, filters, err := parseWorkdayArgs(args)
		if err != nil {
			response = err.Error() + "\n\n" + workdayUsage()
			break
		}
		saved, err := b.queryStore.SaveWorkday(ctx, name, filters)
		if err != nil {
			b.logger.Error("save Workday query", "name", name, "error", err)
			response = "Could not save the Workday search: " + err.Error()
			break
		}
		b.clearCreationSession()
		b.sendScreen(ctx, message.Chat.ID, queryDetailScreen(queryFromWorkday(saved)))
		return
	case "/queries":
		b.clearCreationSession()
		b.clearEditSession()
		b.sendQueryList(ctx, message.Chat.ID)
		return
	case "/search":
		b.clearCreationSession()
		b.clearEditSession()
		if strings.TrimSpace(args) == "" {
			response = "Query name is required. Usage: /search <name>"
			break
		}
		query, err := b.queryStore.Get(ctx, args)
		if err != nil {
			b.logger.Error("load search query", "name", args, "error", err)
			response = "Could not load that search: " + err.Error()
			break
		}
		loadingMessage := b.sendScreen(ctx, message.Chat.ID, searchLoadingScreen(query))
		if loadingMessage == nil {
			return
		}
		go b.finishQuerySearch(ctx, loadingMessage, query)
		return
	case "/cancel":
		b.clearCreationSession()
		b.clearEditSession()
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

func parseAshbyArgs(args string) (string, ashby.Filters, error) {
	parts := strings.Split(args, "|")
	if len(parts) != 4 {
		return "", ashby.Filters{}, errors.New("expected four fields separated by |")
	}
	name := strings.TrimSpace(parts[0])
	if name == "" {
		return "", ashby.Filters{}, errors.New("query name is required")
	}
	jobBoard, err := ashby.NormalizeJobBoardInput(parts[1])
	if err != nil {
		return "", ashby.Filters{}, err
	}
	filters, err := (ashby.Filters{
		JobBoard:   jobBoard,
		Location:   strings.TrimSpace(parts[2]),
		TitleWords: parseTitleWords(parts[3]),
	}).Normalize()
	if err != nil {
		return "", ashby.Filters{}, err
	}
	return name, filters, nil
}

func ashbyUsage() string {
	return "Usage:\n/ashby <name> | <Ashby job URL or board name> | <location> | <comma-separated title words>\n\nExample:\n/ashby Snowflake Software | https://jobs.ashbyhq.com/snowflake/fc1923c1-b151-4458-a792-40d58331a5be | Warsaw, Poland | Software, Engineer"
}

func parseWorkdayArgs(args string) (string, workday.Filters, error) {
	parts := strings.Split(args, "|")
	if len(parts) != 4 {
		return "", workday.Filters{}, errors.New("expected four fields separated by |")
	}
	name := strings.TrimSpace(parts[0])
	words := parseTitleWords(parts[3])
	if name == "" {
		return "", workday.Filters{}, errors.New("query name is required")
	}
	filters, err := workday.FiltersFromJobURL(parts[1], parts[2], words)
	if err != nil {
		return "", workday.Filters{}, err
	}
	return name, filters, nil
}

func workdayUsage() string {
	return "Usage:\n/workday <name> | <Workday job URL> | <partial location> | <comma-separated title words>\n\nExample:\n/workday State Street Working Student | https://statestreet.wd1.myworkdayjobs.com/Global/job/Munich-Germany/Working-Student_R-795614-1/apply | Poland | Working, Student"
}

func (b *Bot) runQuery(ctx context.Context, query searchqueries.Query) ([]jobs.Job, error) {
	switch query.SourceType {
	case searchqueries.SourceAshby:
		if query.Ashby == nil {
			return nil, errors.New("Ashby filters are missing")
		}
		return b.ashbySearcher.Search(ctx, *query.Ashby)
	case searchqueries.SourceGreenhouse:
		if query.Greenhouse == nil {
			return nil, errors.New("Greenhouse filters are missing")
		}
		return b.greenhouseSearcher.Search(ctx, *query.Greenhouse)
	case searchqueries.SourceWorkday:
		if query.Workday == nil {
			return nil, errors.New("Workday filters are missing")
		}
		return b.workdaySearcher.Search(ctx, *query.Workday)
	default:
		return nil, fmt.Errorf("unsupported source %q", query.SourceType)
	}
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
