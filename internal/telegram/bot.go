package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"jobhawk/internal/ashby"
	"jobhawk/internal/daily"
	"jobhawk/internal/greenhouse"
	"jobhawk/internal/hourly"
	"jobhawk/internal/jobs"
	"jobhawk/internal/searchqueries"
	"jobhawk/internal/subscribers"
	"jobhawk/internal/textsearch"
	"jobhawk/internal/workday"
)

type queryStore interface {
	SaveAshby(context.Context, string, ashby.Filters) (searchqueries.AshbyQuery, error)
	SaveGreenhouse(context.Context, string, greenhouse.Filters) (searchqueries.GreenhouseQuery, error)
	SaveText(context.Context, string, textsearch.Filters) (searchqueries.TextQuery, error)
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

type textSearcher interface {
	Search(context.Context, textsearch.Filters) ([]jobs.Job, error)
}

type dailyJobRunner interface {
	RunOnce(context.Context) error
}

type hourlySubscriptionStore interface {
	Upsert(context.Context, int64, time.Time, int, time.Time) (hourly.Subscription, error)
	GetByQueryID(context.Context, int64) (hourly.Subscription, bool, error)
	DeleteByQueryID(context.Context, int64) (bool, error)
}

type Bot struct {
	api                *telego.Bot
	logger             *slog.Logger
	allowedChatID      int64
	queryStore         queryStore
	ashbySearcher      ashbySearcher
	greenhouseSearcher greenhouseSearcher
	textSearcher       textSearcher
	workdaySearcher    workdaySearcher
	dailyRunner        dailyJobRunner
	hourlyStore        hourlySubscriptionStore
	hourlyLocation     *time.Location
	subscribers        *subscribers.Store
	creationMu         sync.Mutex
	creation           *creationSession
	editMu             sync.Mutex
	edit               *editSession
	hourlyMu           sync.Mutex
	hourly             *hourlySession
}

// SetDailyRunner connects the on-demand debug action to the same runner used
// by the scheduler. It is configured during startup before Bot.Run begins.
func (b *Bot) SetDailyRunner(runner dailyJobRunner) {
	b.dailyRunner = runner
}

// SetHourlySubscriptions connects the query-detail controls to the durable
// schedule store. The daily timezone is also used for date-scoped alerts.
func (b *Bot) SetHourlySubscriptions(store hourlySubscriptionStore, location *time.Location) {
	b.hourlyStore = store
	if location == nil {
		location = time.Local
	}
	b.hourlyLocation = location
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
	textSearchers ...textSearcher,
) (*Bot, error) {
	api, err := telego.NewBot(token)
	if err != nil {
		return nil, fmt.Errorf("initialize Telegram client: %w", err)
	}
	subscriberStore := subscribers.NewStore()
	// This is a single-user bot, so daily delivery is active by default after
	// every restart. The existing subscribe commands can still pause/resume it.
	subscriberStore.Add(allowedChatID)
	text := textSearcher(textsearch.NewClient(nil))
	if len(textSearchers) > 0 && textSearchers[0] != nil {
		text = textSearchers[0]
	}

	return &Bot{
		api:                api,
		logger:             logger,
		allowedChatID:      allowedChatID,
		queryStore:         queryStore,
		ashbySearcher:      ashbySearcher,
		greenhouseSearcher: greenhouseSearcher,
		textSearcher:       text,
		workdaySearcher:    workdaySearcher,
		hourlyLocation:     time.Local,
		subscribers:        subscriberStore,
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
	if !strings.HasPrefix(text, "/") && b.hasHourlySession() {
		b.handleHourlyDateInput(ctx, message.Chat.ID, text)
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
	// Any explicit command abandons a partially entered hourly date.
	b.clearHourlySession()

	var response string
	switch command {
	case "/start":
		b.subscribers.Add(message.Chat.ID)
		b.clearCreationSession()
		b.clearEditSession()
		b.clearHourlySession()
		b.sendScreen(ctx, message.Chat.ID, mainMenuScreen())
		return
	case "/menu", "/help":
		b.clearCreationSession()
		b.clearEditSession()
		b.clearHourlySession()
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
		b.sendScreen(ctx, message.Chat.ID, queryDetailScreen(queryFromGreenhouse(query), nil))
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
		b.sendScreen(ctx, message.Chat.ID, queryDetailScreen(queryFromAshby(saved), nil))
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
		b.sendScreen(ctx, message.Chat.ID, queryDetailScreen(queryFromWorkday(saved), nil))
		return
	case "/text":
		b.clearEditSession()
		if strings.TrimSpace(args) == "" {
			session := b.beginProviderCreation(searchqueries.SourceText)
			b.sendScreen(ctx, message.Chat.ID, creationPromptScreen(session, ""))
			return
		}
		name, filters, err := parseTextArgs(args)
		if err != nil {
			response = err.Error() + "\n\n" + textUsage()
			break
		}
		saved, err := b.queryStore.SaveText(ctx, name, filters)
		if err != nil {
			b.logger.Error("save text search query", "name", name, "error", err)
			response = "Could not save the text search: " + err.Error()
			break
		}
		b.clearCreationSession()
		b.sendScreen(ctx, message.Chat.ID, queryDetailScreen(queryFromText(saved), nil))
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
		b.clearHourlySession()
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

func parseTextArgs(args string) (string, textsearch.Filters, error) {
	parts := strings.SplitN(args, "|", 3)
	if len(parts) != 3 {
		return "", textsearch.Filters{}, errors.New("expected three fields separated by |")
	}
	name := strings.TrimSpace(parts[0])
	if name == "" {
		return "", textsearch.Filters{}, errors.New("query name is required")
	}
	filters, err := (textsearch.Filters{URL: parts[1], NoJobsText: parts[2]}).Normalize()
	if err != nil {
		return "", textsearch.Filters{}, err
	}
	return name, filters, nil
}

func textUsage() string {
	return "Usage:\n/text <name> | <filtered job board URL> | <text shown when no jobs are found>\n\nExample:\n/text Google internships | https://www.google.com/about/careers/applications/jobs/results?location=Poland | Search again or try updating your filters"
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
	case searchqueries.SourceText:
		if query.Text == nil {
			return nil, errors.New("text search filters are missing")
		}
		return b.textSearcher.Search(ctx, *query.Text)
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

// NotifyDailyDigest sends exactly one aggregate report to each subscribed
// chat. Job processing is deliberately completed before this method is called.
func (b *Bot) NotifyDailyDigest(ctx context.Context, report daily.Report) error {
	text := formatDailyDigest(report)
	var sendErrors []error
	for _, chatID := range b.subscribers.All() {
		if _, err := b.api.SendMessage(ctx, tu.Message(tu.ID(chatID), text)); err != nil {
			b.logger.Error("send daily job digest", "chat_id", chatID, "error", err)
			sendErrors = append(sendErrors, fmt.Errorf("send daily digest to chat %d: %w", chatID, err))
		}
	}
	return errors.Join(sendErrors...)
}

// NotifyHourlyResults sends one query-scoped alert only when a scheduled search
// returned matches. Empty result sets are intentionally silent.
func (b *Bot) NotifyHourlyResults(ctx context.Context, query searchqueries.Query, found []jobs.Job) error {
	if len(found) == 0 {
		return nil
	}
	text := formatHourlyResults(query, found)
	var sendErrors []error
	for _, chatID := range b.subscribers.All() {
		if _, err := b.api.SendMessage(ctx, tu.Message(tu.ID(chatID), text)); err != nil {
			b.logger.Error("send hourly job alert", "chat_id", chatID, "query_id", query.ID, "error", err)
			sendErrors = append(sendErrors, fmt.Errorf("send hourly alert to chat %d: %w", chatID, err))
		}
	}
	return errors.Join(sendErrors...)
}

const maxDigestBytes = 3900

func formatHourlyResults(query searchqueries.Query, found []jobs.Job) string {
	var result strings.Builder
	jobLabel := "jobs"
	if len(found) == 1 {
		jobLabel = "job"
	}
	fmt.Fprintf(&result, "Hourly job alert\n%s\n\n%d matching %s found:\n", query.Name, len(found), jobLabel)
	included := 0
	for i, job := range found {
		entry := fmt.Sprintf("\n%d. %s", i+1, formatDigestJob(job))
		if result.Len()+len(entry)+80 > maxDigestBytes {
			break
		}
		result.WriteString(entry)
		included++
	}
	if omitted := len(found) - included; omitted > 0 {
		fmt.Fprintf(&result, "\n\n... and %d more matching jobs.", omitted)
	}
	return result.String()
}

func formatDailyDigest(report daily.Report) string {
	failureNote := ""
	if len(report.Failures) > 0 {
		failureNote = fmt.Sprintf("\n\nWarning: %d of %d searches failed.", len(report.Failures), report.QueryCount)
	}
	if len(report.NewJobs) == 0 {
		return "Daily job report\n\nNo new jobs." + failureNote
	}

	var result strings.Builder
	fmt.Fprintf(&result, "Daily job report\n\n%d new jobs found:\n", len(report.NewJobs))
	included := 0
	for i, job := range report.NewJobs {
		entry := fmt.Sprintf("\n%d. %s", i+1, formatDigestJob(job))
		// Leave room for the failure note and an omitted-jobs summary.
		if result.Len()+len(entry)+len(failureNote)+80 > maxDigestBytes {
			break
		}
		result.WriteString(entry)
		included++
	}
	if omitted := len(report.NewJobs) - included; omitted > 0 {
		fmt.Fprintf(&result, "\n\n... and %d more new jobs.", omitted)
	}
	result.WriteString(failureNote)
	return result.String()
}

func formatDigestJob(job jobs.Job) string {
	line := strings.TrimSpace(job.Title)
	if company := strings.TrimSpace(job.Company); company != "" {
		line += " at " + company
	}
	parts := []string{line}
	if location := strings.TrimSpace(job.Location); location != "" {
		parts = append(parts, location)
	}
	if jobURL := strings.TrimSpace(job.URL); jobURL != "" {
		parts = append(parts, jobURL)
	}
	return strings.Join(parts, "\n")
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
