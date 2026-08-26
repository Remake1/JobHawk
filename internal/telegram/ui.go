package telegram

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"jobhawk/internal/hourly"
	"jobhawk/internal/jobs"
	"jobhawk/internal/searchqueries"
)

const (
	callbackHome       = "m:home"
	callbackList       = "m:list"
	callbackAdd        = "m:add"
	callbackRunDaily   = "m:daily"
	callbackCancel     = "w:cancel"
	callbackSkipLoc    = "w:skiploc"
	callbackSkipTitle  = "w:skiptitle"
	callbackSave       = "w:save"
	callbackRestart    = "w:restart"
	callbackAshby      = "w:ashby"
	callbackGreenhouse = "w:greenhouse"
	callbackWorkday    = "w:workday"
)

type screen struct {
	text     string
	entities []telego.MessageEntity
	keyboard *telego.InlineKeyboardMarkup
}

func formattedScreen(keyboard *telego.InlineKeyboardMarkup, parts ...tu.MessageEntityCollection) screen {
	text, entities := tu.MessageEntities(parts...)
	return screen{text: text, entities: entities, keyboard: keyboard}
}

func callbackButton(text, data string) telego.InlineKeyboardButton {
	return tu.InlineKeyboardButton(text).WithCallbackData(data)
}

func mainMenuScreen() screen {
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(callbackButton("🔎 Search queries", callbackList)),
		tu.InlineKeyboardRow(callbackButton("➕ Add search query", callbackAdd)),
		tu.InlineKeyboardRow(callbackButton("🧪 Run daily report now", callbackRunDaily)),
	)
	return formattedScreen(
		keyboard,
		tu.Entity("JobHawk").Bold(),
		tu.Entity("\n\nCreate and manage job searches, or run the daily report on demand."),
	)
}

func queryListScreen(queries []searchqueries.Query) screen {
	rows := make([][]telego.InlineKeyboardButton, 0, len(queries)+2)
	for _, query := range queries {
		rows = append(rows, tu.InlineKeyboardRow(callbackButton(
			truncateButtonText(queryListLabel(query)),
			queryCallback("view", query.ID),
		)))
	}
	rows = append(rows,
		tu.InlineKeyboardRow(callbackButton("➕ Add search query", callbackAdd)),
		tu.InlineKeyboardRow(callbackButton("← Main menu", callbackHome)),
	)

	if len(queries) == 0 {
		return formattedScreen(
			tu.InlineKeyboard(rows...),
			tu.Entity("Search queries").Bold(),
			tu.Entity("\n\nYou don't have any saved searches yet."),
		)
	}
	return formattedScreen(
		tu.InlineKeyboard(rows...),
		tu.Entity("Search queries").Bold(),
		tu.Entityf("\n\n%d saved. Select one to manage it.", len(queries)),
	)
}

func queryListLabel(query searchqueries.Query) string {
	return sourceEmoji(query.SourceType) + " | " + queryCompany(query) + " | " + strings.TrimSpace(query.Name)
}

func queryCompany(query searchqueries.Query) string {
	switch query.SourceType {
	case searchqueries.SourceAshby:
		if query.Ashby != nil {
			return query.Ashby.JobBoard
		}
	case searchqueries.SourceGreenhouse:
		if query.Greenhouse != nil {
			return query.Greenhouse.BoardToken
		}
	case searchqueries.SourceWorkday:
		if query.Workday != nil {
			return query.Workday.Tenant
		}
	}
	return "unknown"
}

func queryDetailScreen(query searchqueries.Query, subscription *hourly.Subscription) screen {
	hourlyButton := callbackButton("⏱ Create hourly alert", queryCallback("hourly_create", query.ID))
	if subscription != nil {
		hourlyButton = callbackButton("⏱ Delete hourly alert", queryCallback("hourly_delete", query.ID))
	}
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			callbackButton("▶ Run query", queryCallback("run", query.ID)),
			callbackButton("✏️ Edit query", queryCallback("edit", query.ID)),
		),
		tu.InlineKeyboardRow(hourlyButton),
		tu.InlineKeyboardRow(callbackButton("🗑 Delete", queryCallback("delete", query.ID))),
		tu.InlineKeyboardRow(callbackButton("← Search queries", callbackList)),
	)
	parts := querySummaryParts(query)
	if subscription != nil {
		parts = append(parts,
			tu.Entity("\n\nHourly alert\n"),
			tu.Entity(subscription.SearchDate.Format("2006-01-02")+" every "+strconv.Itoa(subscription.IntervalMinutes)+" minutes").Code(),
		)
	}
	return formattedScreen(keyboard, parts...)
}

func hourlyDatePromptScreen(query searchqueries.Query, validationError string) screen {
	parts := []tu.MessageEntityCollection{
		tu.Entity("Create hourly alert").Bold(),
		tu.Entity("\n\n" + query.Name),
		tu.Entity("\n\nSend the date to monitor in YYYY-MM-DD format."),
	}
	if validationError != "" {
		parts = append(parts, tu.Entity("\n\n⚠ "+validationError).Bold())
	}
	return formattedScreen(
		tu.InlineKeyboard(tu.InlineKeyboardRow(callbackButton("Cancel", queryCallback("view", query.ID)))),
		parts...,
	)
}

func hourlyIntervalScreen(session hourlySession) screen {
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			callbackButton("15 min", queryCallback("hourly_15", session.query.ID)),
			callbackButton("30 min", queryCallback("hourly_30", session.query.ID)),
			callbackButton("60 min", queryCallback("hourly_60", session.query.ID)),
		),
		tu.InlineKeyboardRow(callbackButton("Cancel", queryCallback("view", session.query.ID))),
	)
	return formattedScreen(
		keyboard,
		tu.Entity("Create hourly alert").Bold(),
		tu.Entity("\n\nDate\n"),
		tu.Entity(session.searchDate.Format("2006-01-02")).Code(),
		tu.Entity("\n\nChoose how often this query should run."),
	)
}

func deleteConfirmationScreen(query searchqueries.Query) screen {
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(callbackButton("🗑 Yes, delete", queryCallback("confirm_delete", query.ID))),
		tu.InlineKeyboardRow(callbackButton("Cancel", queryCallback("view", query.ID))),
	)
	return formattedScreen(
		keyboard,
		tu.Entity("Delete search query?").Bold(),
		tu.Entity("\n\n"),
		tu.Entity(query.Name).Code(),
		tu.Entity(" will be permanently deleted."),
	)
}

func searchResultsScreen(query searchqueries.Query, found []jobs.Job) screen {
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(callbackButton("↻ Run again", queryCallback("run", query.ID))),
		tu.InlineKeyboardRow(callbackButton("← Query details", queryCallback("view", query.ID))),
	)
	parts := []tu.MessageEntityCollection{
		tu.Entity(query.Name).Bold(),
		tu.Entityf("\n\n%d matching open job(s)", len(found)),
	}
	if len(found) == 0 {
		parts = append(parts, tu.Entity("\n\nNo jobs currently match every filter."))
		return formattedScreen(keyboard, parts...)
	}

	const maxResults = 10
	for i, job := range found {
		if i == maxResults {
			parts = append(parts, tu.Entityf("\n\n…and %d more.", len(found)-maxResults))
			break
		}
		parts = append(parts,
			tu.Entity("\n\n"),
			tu.Entity(truncateDisplayText(job.Title, 160)).Bold(),
			tu.Entity("\n"+truncateDisplayText(job.Location, 100)),
		)
		if job.URL != "" {
			parts = append(parts, tu.Entity("\nOpen job").TextLink(job.URL))
		}
	}
	return formattedScreen(keyboard, parts...)
}

func searchLoadingScreen(query searchqueries.Query) screen {
	return formattedScreen(
		nil,
		tu.Entity(query.Name).Bold(),
		tu.Entity("\n\n⏳ Searching "+sourceLabel(query.SourceType)+"…"),
		tu.Entity("\n\nThis can take a while. This message will update automatically."),
	)
}

func searchErrorScreen(query searchqueries.Query) screen {
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(callbackButton("↻ Try again", queryCallback("run", query.ID))),
		tu.InlineKeyboardRow(callbackButton("← Query details", queryCallback("view", query.ID))),
	)
	return formattedScreen(
		keyboard,
		tu.Entity(query.Name).Bold(),
		tu.Entity("\n\n⚠ "+sourceLabel(query.SourceType)+" search failed."),
		tu.Entity("\n\nPlease try again in a moment."),
	)
}

func truncateDisplayText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes-1]) + "…"
}

func creationPromptScreen(session creationSession, validationError string) screen {
	var title, instruction string
	rows := make([][]telego.InlineKeyboardButton, 0, 2)
	switch session.step {
	case creationSource:
		title = "Choose a job board"
		instruction = "Select the source for this saved search."
		rows = append(rows,
			tu.InlineKeyboardRow(callbackButton(sourceLabel(searchqueries.SourceGreenhouse), callbackGreenhouse)),
			tu.InlineKeyboardRow(callbackButton(sourceLabel(searchqueries.SourceWorkday), callbackWorkday)),
			tu.InlineKeyboardRow(callbackButton(sourceLabel(searchqueries.SourceAshby), callbackAshby)),
		)
	case creationName:
		title = "Step 1 of 4 — Name"
		instruction = "Give this search a short, recognizable name.\n\nExample: " + creationNameExample(session.draft.SourceType)
	case creationBoard:
		switch session.draft.SourceType {
		case searchqueries.SourceWorkday:
			title = "Step 2 of 4 — Workday job URL"
			instruction = "Paste any public job URL from the Workday site you want to search.\n\nExample: https://statestreet.wd1.myworkdayjobs.com/Global/job/Munich-Germany/Working-Student_R-795614-1/apply"
		case searchqueries.SourceAshby:
			title = "Step 2 of 4 — Ashby job URL"
			instruction = "Paste any public job URL from the Ashby board you want to search, or enter its board name.\n\nExample: https://jobs.ashbyhq.com/snowflake/fc1923c1-b151-4458-a792-40d58331a5be"
		default:
			title = "Step 2 of 4 — Greenhouse board"
			instruction = "Enter the board token from the Greenhouse URL.\n\nExample: point72"
		}
	case creationLocation:
		title = "Step 3 of 4 — Location"
		switch session.draft.SourceType {
		case searchqueries.SourceWorkday:
			instruction = "Enter text that must occur anywhere in the Workday location.\n\nExample: Poland matches Krakow, Poland"
		case searchqueries.SourceGreenhouse:
			instruction = "Enter text that must occur anywhere in the Greenhouse location.\n\nExample: Warsaw matches Warsaw, Poland (Hybrid)"
		case searchqueries.SourceAshby:
			instruction = "Enter text that must occur anywhere in an Ashby location.\n\nExample: Warsaw matches Warsaw, Poland"
		}
		rows = append(rows, tu.InlineKeyboardRow(callbackButton("Skip location", callbackSkipLoc)))
	case creationTitleWords:
		title = "Step 4 of 4 — Title words"
		instruction = "Enter comma-separated words that must all occur in the job title.\n\nExample: 2027, Internship, Software"
		if draftLocation(session.draft) != "" {
			rows = append(rows, tu.InlineKeyboardRow(callbackButton("Skip title words", callbackSkipTitle)))
		}
	}
	rows = append(rows, tu.InlineKeyboardRow(callbackButton("Cancel", callbackCancel)))

	parts := []tu.MessageEntityCollection{
		tu.Entity("New " + sourceLabel(session.draft.SourceType) + " search").Bold(),
		tu.Entity("\n"),
		tu.Entity(title).Italic(),
		tu.Entity("\n\n" + instruction),
	}
	if validationError != "" {
		parts = append(parts, tu.Entity("\n\n⚠ "+validationError).Bold())
	}
	return formattedScreen(tu.InlineKeyboard(rows...), parts...)
}

func draftLocation(draft creationDraft) string {
	switch draft.SourceType {
	case searchqueries.SourceWorkday:
		return draft.Workday.Location
	case searchqueries.SourceAshby:
		return draft.Ashby.Location
	default:
		return draft.Greenhouse.Location
	}
}

func creationReviewScreen(session creationSession) screen {
	query := queryFromDraft(session.draft)
	parts := []tu.MessageEntityCollection{
		tu.Entity("Review new search").Bold(),
		tu.Entity("\n\nName\n"),
		tu.Entity(session.draft.Name).Code(),
	}
	parts = append(parts, querySummaryParts(query)[1:]...)
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(callbackButton("✓ Save search", callbackSave)),
		tu.InlineKeyboardRow(
			callbackButton("↺ Start over", callbackRestart),
			callbackButton("Cancel", callbackCancel),
		),
	)
	return formattedScreen(keyboard, parts...)
}

func querySummaryParts(query searchqueries.Query) []tu.MessageEntityCollection {
	parts := []tu.MessageEntityCollection{
		tu.Entity(query.Name).Bold(),
		tu.Entity("\n\nSource\n"),
		tu.Entity(sourceLabel(query.SourceType)).Code(),
	}
	switch query.SourceType {
	case searchqueries.SourceAshby:
		if query.Ashby == nil {
			return append(parts, tu.Entity("\n\nInvalid Ashby filters").Bold())
		}
		parts = append(parts,
			tu.Entity("\n\nBoard\n"),
			tu.Entity(query.Ashby.JobBoard).Code(),
			tu.Entity("\n\nLocation\n"),
			tu.Entity(valueOrAny(query.Ashby.Location)).Code(),
			tu.Entity("\n\nTitle contains every word\n"),
			tu.Entity(wordsOrAny(query.Ashby.TitleWords)).Code(),
		)
	case searchqueries.SourceGreenhouse:
		if query.Greenhouse == nil {
			return append(parts, tu.Entity("\n\nInvalid Greenhouse filters").Bold())
		}
		parts = append(parts,
			tu.Entity("\n\nBoard\n"),
			tu.Entity(query.Greenhouse.BoardToken).Code(),
			tu.Entity("\n\nLocation\n"),
			tu.Entity(valueOrAny(query.Greenhouse.Location)).Code(),
			tu.Entity("\n\nTitle contains every word\n"),
			tu.Entity(wordsOrAny(query.Greenhouse.TitleWords)).Code(),
		)
	case searchqueries.SourceWorkday:
		if query.Workday == nil {
			return append(parts, tu.Entity("\n\nInvalid Workday filters").Bold())
		}
		parts = append(parts,
			tu.Entity("\n\nSite\n"),
			tu.Entity(query.Workday.Host+"/"+query.Workday.Site).Code(),
			tu.Entity("\n\nLocation contains\n"),
			tu.Entity(valueOrAny(query.Workday.Location)).Code(),
			tu.Entity("\n\nTitle contains every word\n"),
			tu.Entity(wordsOrAny(query.Workday.TitleWords)).Code(),
		)
	}
	return parts
}

func sourceLabel(source searchqueries.SourceType) string {
	return sourceEmoji(source) + " " + sourceName(source)
}

func sourceEmoji(source searchqueries.SourceType) string {
	switch source {
	case searchqueries.SourceAshby:
		return "🔮"
	case searchqueries.SourceGreenhouse:
		return "🐸"
	case searchqueries.SourceWorkday:
		return "🦋"
	default:
		return "🔎"
	}
}

func sourceName(source searchqueries.SourceType) string {
	switch source {
	case searchqueries.SourceAshby:
		return "AshbyHQ"
	case searchqueries.SourceGreenhouse:
		return "Greenhouse"
	case searchqueries.SourceWorkday:
		return "Workday"
	default:
		return "job board"
	}
}

func creationNameExample(source searchqueries.SourceType) string {
	switch source {
	case searchqueries.SourceWorkday:
		return "State Street Working Student"
	case searchqueries.SourceAshby:
		return "Snowflake Software Engineer"
	default:
		return "Point72 SWE Internship 2027"
	}
}

func queryCallback(action string, id int64) string {
	return "q:" + action + ":" + strconv.FormatInt(id, 10)
}

func truncateButtonText(value string) string {
	const maxRunes = 42
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxRunes-1]) + "…"
}

func parseQueryCallback(data string) (action string, id int64, ok bool) {
	parts := strings.Split(data, ":")
	if len(parts) != 3 || parts[0] != "q" {
		return "", 0, false
	}
	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || id <= 0 {
		return "", 0, false
	}
	switch parts[1] {
	case "view", "run", "edit", "edit_location", "edit_tags", "clear_location", "clear_tags", "hourly_create", "hourly_15", "hourly_30", "hourly_60", "hourly_delete", "delete", "confirm_delete":
		return parts[1], id, true
	default:
		return "", 0, false
	}
}
