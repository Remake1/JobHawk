package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"jobhawk/internal/greenhouse"
)

type creationStep uint8

const (
	creationName creationStep = iota + 1
	creationBoard
	creationLocation
	creationTitleWords
	creationReview
)

type creationDraft struct {
	Name string
	greenhouse.Filters
}

type creationSession struct {
	step  creationStep
	draft creationDraft
}

func (b *Bot) beginCreationSession() creationSession {
	b.creationMu.Lock()
	defer b.creationMu.Unlock()
	b.creation = &creationSession{step: creationName}
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
		boardToken, validationErr := greenhouse.NormalizeBoardToken(input)
		if validationErr != nil {
			b.sendScreen(ctx, chatID, creationPromptScreen(session, validationErr.Error()))
			return
		}
		next, err = b.updateCreationSession(creationBoard, func(current *creationSession) error {
			current.draft.BoardToken = boardToken
			current.step = creationLocation
			return nil
		})
	case creationLocation:
		location := strings.TrimSpace(input)
		if len([]rune(location)) > 200 {
			b.sendScreen(ctx, chatID, creationPromptScreen(session, "Location must be 200 characters or fewer."))
			return
		}
		next, err = b.updateCreationSession(creationLocation, func(current *creationSession) error {
			current.draft.Location = location
			current.step = creationTitleWords
			return nil
		})
	case creationTitleWords:
		words := parseTitleWords(input)
		filters, validationErr := (greenhouse.Filters{
			BoardToken: session.draft.BoardToken,
			Location:   session.draft.Location,
			TitleWords: words,
		}).Normalize()
		if validationErr != nil {
			b.sendScreen(ctx, chatID, creationPromptScreen(session, validationErr.Error()))
			return
		}
		next, err = b.updateCreationSession(creationTitleWords, func(current *creationSession) error {
			current.draft.Filters = filters
			current.step = creationReview
			return nil
		})
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
	switch query.Data {
	case callbackHome:
		b.clearCreationSession()
		next = mainMenuScreen()
	case callbackList:
		b.clearCreationSession()
		queries, err := b.queryStore.ListGreenhouse(ctx)
		if err != nil {
			b.callbackFailure(ctx, query, "Could not load search queries.", err)
			return
		}
		next = queryListScreen(queries)
	case callbackAdd:
		next = creationPromptScreen(b.beginCreationSession(), "")
	case callbackCancel:
		b.clearCreationSession()
		next = mainMenuScreen()
		toast = "Creation cancelled"
	case callbackRestart:
		next = creationPromptScreen(b.beginCreationSession(), "")
	case callbackSkipLoc:
		session, err := b.updateCreationSession(creationLocation, func(current *creationSession) error {
			current.draft.Location = ""
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
			filters, validationErr := (greenhouse.Filters{
				BoardToken: current.draft.BoardToken,
				Location:   current.draft.Location,
			}).Normalize()
			if validationErr != nil {
				return validationErr
			}
			current.draft.Filters = filters
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
		saved, err := b.queryStore.SaveGreenhouse(ctx, session.draft.Name, session.draft.Filters)
		if err != nil {
			b.callbackFailure(ctx, query, "Could not save the search.", err)
			return
		}
		b.clearCreationSession()
		next = queryDetailScreen(saved)
		toast = "Search saved"
	default:
		action, id, ok := parseQueryCallback(query.Data)
		if !ok {
			b.answerCallback(ctx, query.ID, "Unknown action.", true)
			return
		}
		b.clearCreationSession()
		queryRecord, err := b.queryStore.GetGreenhouseByID(ctx, id)
		if err != nil {
			b.callbackFailure(ctx, query, "That search no longer exists.", err)
			return
		}
		switch action {
		case "view":
			next = queryDetailScreen(queryRecord)
		case "run":
			found, searchErr := b.greenhouseSearcher.Search(ctx, queryRecord.Filters)
			if searchErr != nil {
				b.callbackFailure(ctx, query, "Greenhouse search failed.", searchErr)
				return
			}
			next = searchResultsScreen(queryRecord, found)
			toast = fmt.Sprintf("Found %d job(s)", len(found))
		case "delete":
			next = deleteConfirmationScreen(queryRecord)
		case "confirm_delete":
			deleted, deleteErr := b.queryStore.DeleteGreenhouse(ctx, queryRecord.ID)
			if deleteErr != nil {
				b.callbackFailure(ctx, query, "Could not delete the search.", deleteErr)
				return
			}
			queries, listErr := b.queryStore.ListGreenhouse(ctx)
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
	queries, err := b.queryStore.ListGreenhouse(ctx)
	if err != nil {
		b.logger.Error("list search queries", "error", err)
		b.sendText(ctx, chatID, "Could not load search queries.")
		return
	}
	b.sendScreen(ctx, chatID, queryListScreen(queries))
}

func (b *Bot) sendScreen(ctx context.Context, chatID int64, value screen) {
	params := tu.Message(tu.ID(chatID), value.text).WithEntities(value.entities...)
	if value.keyboard != nil {
		params.WithReplyMarkup(value.keyboard)
	}
	if _, err := b.api.SendMessage(ctx, params); err != nil {
		b.logger.Error("send bot screen", "chat_id", chatID, "error", err)
	}
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
