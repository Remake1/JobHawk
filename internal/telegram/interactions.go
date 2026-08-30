package telegram

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"jobhawk/internal/ashby"
	"jobhawk/internal/greenhouse"
	"jobhawk/internal/searchqueries"
	"jobhawk/internal/textsearch"
	"jobhawk/internal/workday"
)

type creationStep uint8

const (
	creationSource creationStep = iota + 1
	creationName
	creationBoard
	creationLocation
	creationTitleWords
	creationRender
	creationReview
)

type creationDraft struct {
	Name       string
	SourceType searchqueries.SourceType
	Ashby      ashby.Filters
	Greenhouse greenhouse.Filters
	Text       textsearch.Filters
	Workday    workday.Filters
}

type creationSession struct {
	step  creationStep
	draft creationDraft
}

func (b *Bot) beginCreationSession() creationSession {
	b.clearEditSession()
	b.clearHourlySession()
	b.creationMu.Lock()
	defer b.creationMu.Unlock()
	b.creation = &creationSession{step: creationSource}
	return *b.creation
}

func (b *Bot) beginProviderCreation(source searchqueries.SourceType) creationSession {
	b.clearEditSession()
	b.clearHourlySession()
	b.creationMu.Lock()
	defer b.creationMu.Unlock()
	b.creation = &creationSession{step: creationName, draft: creationDraft{SourceType: source}}
	return *b.creation
}

func (b *Bot) hasCreationSession() bool {
	b.creationMu.Lock()
	defer b.creationMu.Unlock()
	return b.creation != nil
}

func (b *Bot) creationSessionSnapshot() (creationSession, bool) {
	b.creationMu.Lock()
	defer b.creationMu.Unlock()
	if b.creation == nil {
		return creationSession{}, false
	}
	return *b.creation, true
}

func (b *Bot) updateCreationSession(expected creationStep, update func(*creationSession) error) (creationSession, error) {
	b.creationMu.Lock()
	defer b.creationMu.Unlock()
	if b.creation == nil || b.creation.step != expected {
		return creationSession{}, errors.New("this creation step has expired")
	}
	if err := update(b.creation); err != nil {
		return *b.creation, err
	}
	return *b.creation, nil
}

func (b *Bot) clearCreationSession() {
	b.creationMu.Lock()
	defer b.creationMu.Unlock()
	b.creation = nil
}

func (b *Bot) handleCreationInput(ctx context.Context, chatID int64, input string) {
	session, ok := b.creationSessionSnapshot()
	if !ok {
		b.sendScreen(ctx, chatID, mainMenuScreen())
		return
	}

	var next creationSession
	var err error
	switch session.step {
	case creationName:
		name := strings.TrimSpace(input)
		if name == "" || len([]rune(name)) > 100 {
			b.sendScreen(ctx, chatID, creationPromptScreen(session, "Name must be between 1 and 100 characters."))
			return
		}
		next, err = b.updateCreationSession(creationName, func(current *creationSession) error {
			current.draft.Name = name
			current.step = creationBoard
			return nil
		})
	case creationBoard:
		var ashbyFilters ashby.Filters
		var greenhouseFilters greenhouse.Filters
		var textFilters textsearch.Filters
		var workdayFilters workday.Filters
		switch session.draft.SourceType {
		case searchqueries.SourceText:
			requestURL, validationErr := textsearch.NormalizeURL(input)
			if validationErr != nil {
				b.sendScreen(ctx, chatID, creationPromptScreen(session, validationErr.Error()))
				return
			}
			textFilters = textsearch.Filters{URL: requestURL}
		case searchqueries.SourceWorkday:
			host, tenant, site, validationErr := workday.ParseJobURL(input)
			if validationErr != nil {
				b.sendScreen(ctx, chatID, creationPromptScreen(session, validationErr.Error()))
				return
			}
			workdayFilters = workday.Filters{Host: host, Tenant: tenant, Site: site}
		case searchqueries.SourceAshby:
			jobBoard, validationErr := ashby.NormalizeJobBoardInput(input)
			if validationErr != nil {
				b.sendScreen(ctx, chatID, creationPromptScreen(session, validationErr.Error()))
				return
			}
			ashbyFilters = ashby.Filters{JobBoard: jobBoard}
		default:
			boardToken, validationErr := greenhouse.NormalizeBoardToken(input)
			if validationErr != nil {
				b.sendScreen(ctx, chatID, creationPromptScreen(session, validationErr.Error()))
				return
			}
			greenhouseFilters = greenhouse.Filters{BoardToken: boardToken}
		}
		next, err = b.updateCreationSession(creationBoard, func(current *creationSession) error {
			current.draft.Ashby = ashbyFilters
			current.draft.Greenhouse = greenhouseFilters
			current.draft.Text = textFilters
			current.draft.Workday = workdayFilters
			current.step = creationLocation
			return nil
		})
	case creationLocation:
		if session.draft.SourceType == searchqueries.SourceText {
			filters := session.draft.Text
			filters.NoJobsText = input
			normalized, validationErr := filters.Normalize()
			if validationErr != nil {
				b.sendScreen(ctx, chatID, creationPromptScreen(session, validationErr.Error()))
				return
			}
			next, err = b.updateCreationSession(creationLocation, func(current *creationSession) error {
				current.draft.Text = normalized
				current.step = creationRender
				return nil
			})
			break
		}
		location := strings.TrimSpace(input)
		if len([]rune(location)) > 200 {
			b.sendScreen(ctx, chatID, creationPromptScreen(session, "Location must be 200 characters or fewer."))
			return
		}
		next, err = b.updateCreationSession(creationLocation, func(current *creationSession) error {
			switch current.draft.SourceType {
			case searchqueries.SourceWorkday:
				current.draft.Workday.Location = location
			case searchqueries.SourceAshby:
				current.draft.Ashby.Location = location
			default:
				current.draft.Greenhouse.Location = location
			}
			current.step = creationTitleWords
			return nil
		})
	case creationTitleWords:
		words := parseTitleWords(input)
		validationErr := normalizeDraftFilters(&session.draft, words)
		if validationErr != nil {
			b.sendScreen(ctx, chatID, creationPromptScreen(session, validationErr.Error()))
			return
		}
		next, err = b.updateCreationSession(creationTitleWords, func(current *creationSession) error {
			switch current.draft.SourceType {
			case searchqueries.SourceWorkday:
				current.draft.Workday = session.draft.Workday
			case searchqueries.SourceAshby:
				current.draft.Ashby = session.draft.Ashby
			default:
				current.draft.Greenhouse = session.draft.Greenhouse
			}
			current.step = creationReview
			return nil
		})
	case creationRender:
		// Rendering is selected with explicit buttons so server-side remains a
		// visible default and a free-form answer cannot be misinterpreted.
		b.sendScreen(ctx, chatID, creationPromptScreen(session, "Choose a rendering option below."))
		return
	case creationReview:
		b.sendScreen(ctx, chatID, creationReviewScreen(session))
		return
	default:
		err = errors.New("unknown creation step")
	}
	if err != nil {
		b.logger.Error("advance creation flow", "error", err)
		b.sendScreen(ctx, chatID, mainMenuScreen())
		return
	}
	if next.step == creationReview {
		b.sendScreen(ctx, chatID, creationReviewScreen(next))
		return
	}
	b.sendScreen(ctx, chatID, creationPromptScreen(next, ""))
}

func (b *Bot) handleCallbackQuery(ctx context.Context, query *telego.CallbackQuery) {
	if query.From.ID != b.allowedChatID {
		b.logger.Warn("ignored callback from unauthorized user", "user_id", query.From.ID)
		return
	}
	if query.Message == nil || !query.Message.IsAccessible() {
		b.answerCallback(ctx, query.ID, "This menu is no longer available.", true)
		return
	}
	message := query.Message.Message()
	if message == nil || message.Chat.ID != b.allowedChatID {
		b.answerCallback(ctx, query.ID, "This menu is not available in this chat.", true)
		return
	}

	var next screen
	var toast string
	b.clearEditSession()
	callbackData := query.Data
	listPage := 0
	if page, ok := parseQueryListPageCallback(callbackData); ok {
		callbackData = callbackList
		listPage = page
	}
	switch callbackData {
	case callbackHome:
		b.clearCreationSession()
		b.clearHourlySession()
		next = mainMenuScreen()
	case callbackList:
		b.clearCreationSession()
		b.clearHourlySession()
		queries, err := b.queryStore.List(ctx)
		if err != nil {
			b.callbackFailure(ctx, query, "Could not load search queries.", err)
			return
		}
		next = queryListPageScreen(queries, listPage)
	case callbackAdd:
		next = creationPromptScreen(b.beginCreationSession(), "")
	case callbackRunDaily:
		b.clearCreationSession()
		b.clearHourlySession()
		if b.dailyRunner == nil {
			b.answerCallback(ctx, query.ID, "Daily runner is not configured.", true)
			return
		}
		b.answerCallback(ctx, query.ID, "Daily report started", false)
		go func() {
			if err := b.dailyRunner.RunOnce(ctx); err != nil && ctx.Err() == nil {
				b.logger.Error("run daily report on demand", "error", err)
			}
		}()
		return
	case callbackGreenhouse:
		next = creationPromptScreen(b.beginProviderCreation(searchqueries.SourceGreenhouse), "")
	case callbackWorkday:
		next = creationPromptScreen(b.beginProviderCreation(searchqueries.SourceWorkday), "")
	case callbackAshby:
		next = creationPromptScreen(b.beginProviderCreation(searchqueries.SourceAshby), "")
	case callbackText:
		next = creationPromptScreen(b.beginProviderCreation(searchqueries.SourceText), "")
	case callbackCancel:
		b.clearCreationSession()
		b.clearHourlySession()
		next = mainMenuScreen()
		toast = "Creation cancelled"
	case callbackRestart:
		session, ok := b.creationSessionSnapshot()
		if !ok || !isSupportedSource(session.draft.SourceType) {
			next = creationPromptScreen(b.beginCreationSession(), "")
		} else {
			next = creationPromptScreen(b.beginProviderCreation(session.draft.SourceType), "")
		}
	case callbackSkipLoc:
		session, err := b.updateCreationSession(creationLocation, func(current *creationSession) error {
			switch current.draft.SourceType {
			case searchqueries.SourceWorkday:
				current.draft.Workday.Location = ""
			case searchqueries.SourceAshby:
				current.draft.Ashby.Location = ""
			default:
				current.draft.Greenhouse.Location = ""
			}
			current.step = creationTitleWords
			return nil
		})
		if err != nil {
			b.answerCallback(ctx, query.ID, err.Error(), true)
			return
		}
		next = creationPromptScreen(session, "")
	case callbackSkipTitle:
		session, err := b.updateCreationSession(creationTitleWords, func(current *creationSession) error {
			if validationErr := normalizeDraftFilters(&current.draft, nil); validationErr != nil {
				return validationErr
			}
			current.step = creationReview
			return nil
		})
		if err != nil {
			b.answerCallback(ctx, query.ID, err.Error(), true)
			return
		}
		next = creationReviewScreen(session)
	case callbackSSR, callbackCSR:
		clientSideRender := callbackData == callbackCSR
		session, err := b.updateCreationSession(creationRender, func(current *creationSession) error {
			if current.draft.SourceType != searchqueries.SourceText {
				return errors.New("rendering mode only applies to text searches")
			}
			current.draft.Text.ClientSideRender = clientSideRender
			current.step = creationReview
			return nil
		})
		if err != nil {
			b.answerCallback(ctx, query.ID, err.Error(), true)
			return
		}
		next = creationReviewScreen(session)
	case callbackSave:
		session, ok := b.creationSessionSnapshot()
		if !ok || session.step != creationReview {
			b.answerCallback(ctx, query.ID, "This draft has expired. Start a new search.", true)
			return
		}
		var saved searchqueries.Query
		switch session.draft.SourceType {
		case searchqueries.SourceWorkday:
			workdayQuery, err := b.queryStore.SaveWorkday(ctx, session.draft.Name, session.draft.Workday)
			if err != nil {
				b.callbackFailure(ctx, query, "Could not save the search.", err)
				return
			}
			saved = queryFromWorkday(workdayQuery)
		case searchqueries.SourceAshby:
			ashbyQuery, err := b.queryStore.SaveAshby(ctx, session.draft.Name, session.draft.Ashby)
			if err != nil {
				b.callbackFailure(ctx, query, "Could not save the search.", err)
				return
			}
			saved = queryFromAshby(ashbyQuery)
		case searchqueries.SourceText:
			textQuery, err := b.queryStore.SaveText(ctx, session.draft.Name, session.draft.Text)
			if err != nil {
				b.callbackFailure(ctx, query, "Could not save the search.", err)
				return
			}
			saved = queryFromText(textQuery)
		default:
			greenhouseQuery, err := b.queryStore.SaveGreenhouse(ctx, session.draft.Name, session.draft.Greenhouse)
			if err != nil {
				b.callbackFailure(ctx, query, "Could not save the search.", err)
				return
			}
			saved = queryFromGreenhouse(greenhouseQuery)
		}
		b.clearCreationSession()
		next = queryDetailScreen(saved, nil)
		toast = "Search saved"
	default:
		action, id, ok := parseQueryCallback(query.Data)
		if !ok {
			b.answerCallback(ctx, query.ID, "Unknown action.", true)
			return
		}
		b.clearCreationSession()
		queryRecord, err := b.queryStore.GetByID(ctx, id)
		if err != nil {
			b.callbackFailure(ctx, query, "That search no longer exists.", err)
			return
		}
		switch action {
		case "view":
			b.clearHourlySession()
			subscription, subscriptionErr := b.subscriptionForQuery(ctx, queryRecord.ID)
			if subscriptionErr != nil {
				b.callbackFailure(ctx, query, "Could not load the hourly alert.", subscriptionErr)
				return
			}
			next = queryDetailScreen(queryRecord, subscription)
		case "hourly_create":
			if b.hourlyStore == nil {
				b.answerCallback(ctx, query.ID, "Hourly alerts are not configured.", true)
				return
			}
			next = hourlyDatePromptScreen(b.beginHourlySession(queryRecord).query, "")
		case "hourly_15", "hourly_30", "hourly_60":
			session, exists := b.hourlySessionSnapshot()
			if !exists || session.query.ID != queryRecord.ID || session.searchDate.IsZero() {
				b.answerCallback(ctx, query.ID, "This hourly alert setup has expired. Start again.", true)
				return
			}
			interval := 15
			if action == "hourly_30" {
				interval = 30
			} else if action == "hourly_60" {
				interval = 60
			}
			now := time.Now()
			subscription, saveErr := b.hourlyStore.Upsert(ctx, queryRecord.ID, session.searchDate, interval, firstHourlyRun(session.searchDate, now, b.hourlyLocation))
			if saveErr != nil {
				b.callbackFailure(ctx, query, "Could not save the hourly alert.", saveErr)
				return
			}
			b.clearHourlySession()
			next = queryDetailScreen(queryRecord, &subscription)
			toast = "Hourly alert created"
		case "hourly_delete":
			if b.hourlyStore == nil {
				b.answerCallback(ctx, query.ID, "Hourly alerts are not configured.", true)
				return
			}
			_, deleteErr := b.hourlyStore.DeleteByQueryID(ctx, queryRecord.ID)
			if deleteErr != nil {
				b.callbackFailure(ctx, query, "Could not delete the hourly alert.", deleteErr)
				return
			}
			b.clearHourlySession()
			next = queryDetailScreen(queryRecord, nil)
			toast = "Hourly alert deleted"
		case "edit":
			session := b.beginEditSession(queryRecord, editNoField)
			next = queryEditorScreen(session.query)
		case "edit_location":
			session := b.beginEditSession(queryRecord, editLocation)
			next = editPromptScreen(session, "")
		case "edit_tags":
			session := b.beginEditSession(queryRecord, editTags)
			next = editPromptScreen(session, "")
		case "clear_location", "clear_tags":
			editable, editableErr := editableFilters(queryRecord)
			if editableErr != nil {
				b.callbackFailure(ctx, query, "Could not edit the search.", editableErr)
				return
			}
			if action == "clear_location" {
				editable.Location = ""
			} else {
				editable.Tags = nil
			}
			updated, updateErr := b.queryStore.Update(ctx, queryRecord.ID, editable)
			if updateErr != nil {
				b.callbackFailure(ctx, query, "Could not clear this filter: "+updateErr.Error(), updateErr)
				return
			}
			b.beginEditSession(updated, editNoField)
			next = queryEditorScreen(updated)
			toast = "Query updated"
		case "run":
			// Telegram callback queries must be acknowledged quickly. Workday can
			// take long enough to make the callback expire, so update the menu and
			// finish the provider request outside the update-processing loop.
			b.answerCallback(ctx, query.ID, "Search started", false)
			if err := b.editScreen(ctx, message, searchLoadingScreen(queryRecord)); err != nil {
				b.logger.Error("show search progress", "query_id", queryRecord.ID, "error", err)
			}
			go b.finishQuerySearch(ctx, message, queryRecord)
			return
		case "delete":
			next = deleteConfirmationScreen(queryRecord)
		case "confirm_delete":
			deleted, deleteErr := b.queryStore.Delete(ctx, queryRecord.ID)
			if deleteErr != nil {
				b.callbackFailure(ctx, query, "Could not delete the search.", deleteErr)
				return
			}
			queries, listErr := b.queryStore.List(ctx)
			if listErr != nil {
				b.callbackFailure(ctx, query, "Search was deleted, but the list could not be refreshed.", listErr)
				return
			}
			next = queryListScreen(queries)
			if deleted {
				toast = "Search deleted"
			} else {
				toast = "Search was already deleted"
			}
		}
	}

	if err := b.editScreen(ctx, message, next); err != nil {
		b.logger.Error("edit bot menu", "error", err, "callback_data", query.Data)
		b.answerCallback(ctx, query.ID, "Could not update the menu.", true)
		return
	}
	b.answerCallback(ctx, query.ID, toast, false)
}

func (b *Bot) sendQueryList(ctx context.Context, chatID int64) {
	queries, err := b.queryStore.List(ctx)
	if err != nil {
		b.logger.Error("list search queries", "error", err)
		b.sendText(ctx, chatID, "Could not load search queries.")
		return
	}
	b.sendScreen(ctx, chatID, queryListScreen(queries))
}

func (b *Bot) sendScreen(ctx context.Context, chatID int64, value screen) *telego.Message {
	params := tu.Message(tu.ID(chatID), value.text).WithEntities(value.entities...)
	if value.keyboard != nil {
		params.WithReplyMarkup(value.keyboard)
	}
	message, err := b.api.SendMessage(ctx, params)
	if err != nil {
		b.logger.Error("send bot screen", "chat_id", chatID, "error", err)
		return nil
	}
	return message
}

func (b *Bot) sendText(ctx context.Context, chatID int64, text string) {
	if _, err := b.api.SendMessage(ctx, tu.Message(tu.ID(chatID), text)); err != nil {
		b.logger.Error("send bot message", "chat_id", chatID, "error", err)
	}
}

func (b *Bot) editScreen(ctx context.Context, message *telego.Message, value screen) error {
	params := tu.EditMessageText(tu.ID(message.Chat.ID), message.MessageID, value.text).
		WithEntities(value.entities...)
	if value.keyboard != nil {
		params.WithReplyMarkup(value.keyboard)
	}
	_, err := b.api.EditMessageText(ctx, params)
	return err
}

func (b *Bot) finishQuerySearch(ctx context.Context, message *telego.Message, query searchqueries.Query) {
	found, err := b.runQuery(ctx, query)
	result := searchResultsScreen(query, found)
	if err != nil {
		b.logger.Error("run search", "name", query.Name, "source", query.SourceType, "error", err)
		result = searchErrorScreen(query)
	}
	if err := b.editScreen(ctx, message, result); err != nil {
		b.logger.Error("show completed search", "query_id", query.ID, "error", err)
		b.sendScreen(ctx, message.Chat.ID, result)
	}
}

func (b *Bot) answerCallback(ctx context.Context, id, text string, alert bool) {
	params := tu.CallbackQuery(id)
	if text != "" {
		params.WithText(text)
	}
	if alert {
		params.WithShowAlert()
	}
	if err := b.api.AnswerCallbackQuery(ctx, params); err != nil {
		b.logger.Error("answer callback query", "error", err)
	}
}

func (b *Bot) callbackFailure(ctx context.Context, query *telego.CallbackQuery, message string, err error) {
	b.logger.Error("handle bot button", "callback_data", query.Data, "error", err)
	b.answerCallback(ctx, query.ID, message, true)
}

func parseTitleWords(input string) []string {
	if strings.Contains(input, ",") {
		return strings.Split(input, ",")
	}
	return strings.Fields(input)
}

func normalizeDraftFilters(draft *creationDraft, titleWords []string) error {
	switch draft.SourceType {
	case searchqueries.SourceWorkday:
		filters := draft.Workday
		filters.TitleWords = titleWords
		normalized, err := filters.Normalize()
		if err != nil {
			return err
		}
		draft.Workday = normalized
		return nil
	case searchqueries.SourceAshby:
		filters := draft.Ashby
		filters.TitleWords = titleWords
		normalized, err := filters.Normalize()
		if err != nil {
			return err
		}
		draft.Ashby = normalized
		return nil
	}
	filters := draft.Greenhouse
	filters.TitleWords = titleWords
	normalized, err := filters.Normalize()
	if err != nil {
		return err
	}
	draft.Greenhouse = normalized
	return nil
}

func queryFromDraft(draft creationDraft) searchqueries.Query {
	query := searchqueries.Query{Name: draft.Name, SourceType: draft.SourceType}
	switch draft.SourceType {
	case searchqueries.SourceWorkday:
		filters := draft.Workday
		query.Workday = &filters
	case searchqueries.SourceAshby:
		filters := draft.Ashby
		query.Ashby = &filters
	case searchqueries.SourceText:
		filters := draft.Text
		query.Text = &filters
	default:
		filters := draft.Greenhouse
		query.Greenhouse = &filters
	}
	return query
}

func queryFromAshby(query searchqueries.AshbyQuery) searchqueries.Query {
	filters := query.Filters
	return searchqueries.Query{
		ID: query.ID, Name: query.Name, SourceType: searchqueries.SourceAshby,
		Ashby: &filters, Enabled: query.Enabled, CreatedAt: query.CreatedAt, UpdatedAt: query.UpdatedAt,
	}
}

func isSupportedSource(source searchqueries.SourceType) bool {
	return source == searchqueries.SourceAshby || source == searchqueries.SourceGreenhouse || source == searchqueries.SourceText || source == searchqueries.SourceWorkday
}

func queryFromGreenhouse(query searchqueries.GreenhouseQuery) searchqueries.Query {
	filters := query.Filters
	return searchqueries.Query{
		ID: query.ID, Name: query.Name, SourceType: searchqueries.SourceGreenhouse,
		Greenhouse: &filters, Enabled: query.Enabled, CreatedAt: query.CreatedAt, UpdatedAt: query.UpdatedAt,
	}
}

func queryFromWorkday(query searchqueries.WorkdayQuery) searchqueries.Query {
	filters := query.Filters
	return searchqueries.Query{
		ID: query.ID, Name: query.Name, SourceType: searchqueries.SourceWorkday,
		Workday: &filters, Enabled: query.Enabled, CreatedAt: query.CreatedAt, UpdatedAt: query.UpdatedAt,
	}
}

func queryFromText(query searchqueries.TextQuery) searchqueries.Query {
	filters := query.Filters
	return searchqueries.Query{
		ID: query.ID, Name: query.Name, SourceType: searchqueries.SourceText,
		Text: &filters, Enabled: query.Enabled, CreatedAt: query.CreatedAt, UpdatedAt: query.UpdatedAt,
	}
}
