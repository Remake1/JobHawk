package telegram

import (
	"context"
	"errors"

	"github.com/mymmrac/telego"
	"github.com/mymmrac/telego/telegoutil"

	"jobhawk/internal/searchqueries"
)

type editField uint8

const (
	editNoField editField = iota
	editLocation
	editTags
)

type editSession struct {
	field editField
	query searchqueries.Query
}

func (b *Bot) beginEditSession(query searchqueries.Query, field editField) editSession {
	b.clearCreationSession()
	b.clearHourlySession()
	b.editMu.Lock()
	defer b.editMu.Unlock()
	b.edit = &editSession{field: field, query: query}
	return *b.edit
}

func (b *Bot) hasEditInputSession() bool {
	b.editMu.Lock()
	defer b.editMu.Unlock()
	return b.edit != nil && b.edit.field != editNoField
}

func (b *Bot) editSessionSnapshot() (editSession, bool) {
	b.editMu.Lock()
	defer b.editMu.Unlock()
	if b.edit == nil {
		return editSession{}, false
	}
	return *b.edit, true
}

func (b *Bot) clearEditSession() {
	b.editMu.Lock()
	defer b.editMu.Unlock()
	b.edit = nil
}

func (b *Bot) handleEditInput(ctx context.Context, chatID int64, input string) {
	session, ok := b.editSessionSnapshot()
	if !ok || session.field == editNoField {
		return
	}

	editable, err := editableFilters(session.query)
	if err != nil {
		b.logger.Error("read editable search filters", "query_id", session.query.ID, "error", err)
		b.clearEditSession()
		b.sendScreen(ctx, chatID, queryDetailScreen(session.query, nil))
		return
	}
	switch session.field {
	case editLocation:
		editable.Location = input
	case editTags:
		editable.Tags = parseTitleWords(input)
	default:
		return
	}

	updated, err := b.queryStore.Update(ctx, session.query.ID, editable)
	if err != nil {
		b.sendScreen(ctx, chatID, editPromptScreen(session, err.Error()))
		return
	}
	b.beginEditSession(updated, editNoField)
	b.sendScreen(ctx, chatID, queryEditorScreen(updated))
}

func editableFilters(query searchqueries.Query) (searchqueries.EditableFilters, error) {
	switch query.SourceType {
	case searchqueries.SourceAshby:
		if query.Ashby == nil {
			return searchqueries.EditableFilters{}, errors.New("Ashby query filters are missing")
		}
		return searchqueries.EditableFilters{Location: query.Ashby.Location, Tags: query.Ashby.TitleWords}, nil
	case searchqueries.SourceGreenhouse:
		if query.Greenhouse == nil {
			return searchqueries.EditableFilters{}, errors.New("Greenhouse query filters are missing")
		}
		return searchqueries.EditableFilters{Location: query.Greenhouse.Location, Tags: query.Greenhouse.TitleWords}, nil
	case searchqueries.SourceWorkday:
		if query.Workday == nil {
			return searchqueries.EditableFilters{}, errors.New("Workday query filters are missing")
		}
		return searchqueries.EditableFilters{Location: query.Workday.Location, Tags: query.Workday.TitleWords}, nil
	default:
		return searchqueries.EditableFilters{}, errors.New("unsupported query source")
	}
}

func queryEditorScreen(query searchqueries.Query) screen {
	editable, _ := editableFilters(query)
	keyboard := telegoutil.InlineKeyboard(
		telegoutil.InlineKeyboardRow(
			callbackButton("📍 Edit location", queryCallback("edit_location", query.ID)),
			callbackButton("🏷 Edit tags", queryCallback("edit_tags", query.ID)),
		),
		telegoutil.InlineKeyboardRow(callbackButton("✓ Done", queryCallback("view", query.ID))),
	)
	return formattedScreen(
		keyboard,
		telegoutil.Entity("Edit query").Bold(),
		telegoutil.Entity("\n\n"),
		telegoutil.Entity(query.Name).Code(),
		telegoutil.Entity("\n\nLocation\n"),
		telegoutil.Entity(valueOrAny(editable.Location)).Code(),
		telegoutil.Entity("\n\nTags\n"),
		telegoutil.Entity(wordsOrAny(editable.Tags)).Code(),
		telegoutil.Entity("\n\nThe query name and job board cannot be changed."),
	)
}

func editPromptScreen(session editSession, validationError string) screen {
	editable, _ := editableFilters(session.query)
	var title, instruction, current, clearAction string
	canClear := false
	switch session.field {
	case editLocation:
		title = "Edit location"
		current = valueOrAny(editable.Location)
		clearAction = "clear_location"
		canClear = editable.Location != "" && len(editable.Tags) > 0
		instruction = "Send the new location text. " + sourceName(session.query.SourceType) + " locations use partial matching."
	case editTags:
		title = "Edit tags"
		current = wordsOrAny(editable.Tags)
		clearAction = "clear_tags"
		canClear = len(editable.Tags) > 0 && editable.Location != ""
		instruction = "Send comma-separated tags. Every tag must occur in the job title."
	}

	rows := make([][]telego.InlineKeyboardButton, 0, 2)
	if canClear {
		rows = append(rows, telegoutil.InlineKeyboardRow(callbackButton("Clear", queryCallback(clearAction, session.query.ID))))
	}
	rows = append(rows, telegoutil.InlineKeyboardRow(callbackButton("← Back", queryCallback("edit", session.query.ID))))
	parts := []telegoutil.MessageEntityCollection{
		telegoutil.Entity(title).Bold(),
		telegoutil.Entity("\n\n" + instruction),
		telegoutil.Entity("\n\nCurrent value\n"),
		telegoutil.Entity(current).Code(),
	}
	if validationError != "" {
		parts = append(parts, telegoutil.Entity("\n\n⚠ "+validationError).Bold())
	}
	return formattedScreen(telegoutil.InlineKeyboard(rows...), parts...)
}
