package telegram

import (
	"context"
	"errors"
	"strings"
	"time"

	"jobhawk/internal/hourly"
	"jobhawk/internal/searchqueries"
)

type hourlySession struct {
	query      searchqueries.Query
	searchDate time.Time
}

func (b *Bot) beginHourlySession(query searchqueries.Query) hourlySession {
	b.clearCreationSession()
	b.clearEditSession()
	b.hourlyMu.Lock()
	defer b.hourlyMu.Unlock()
	b.hourly = &hourlySession{query: query}
	return *b.hourly
}

func (b *Bot) hasHourlySession() bool {
	b.hourlyMu.Lock()
	defer b.hourlyMu.Unlock()
	return b.hourly != nil
}

func (b *Bot) hourlySessionSnapshot() (hourlySession, bool) {
	b.hourlyMu.Lock()
	defer b.hourlyMu.Unlock()
	if b.hourly == nil {
		return hourlySession{}, false
	}
	return *b.hourly, true
}

func (b *Bot) setHourlyDate(searchDate time.Time) (hourlySession, error) {
	b.hourlyMu.Lock()
	defer b.hourlyMu.Unlock()
	if b.hourly == nil {
		return hourlySession{}, errors.New("this hourly alert setup has expired")
	}
	b.hourly.searchDate = searchDate
	return *b.hourly, nil
}

func (b *Bot) clearHourlySession() {
	b.hourlyMu.Lock()
	defer b.hourlyMu.Unlock()
	b.hourly = nil
}

func (b *Bot) handleHourlyDateInput(ctx context.Context, chatID int64, input string) {
	session, ok := b.hourlySessionSnapshot()
	if !ok {
		b.sendScreen(ctx, chatID, mainMenuScreen())
		return
	}
	searchDate, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(input), b.hourlyLocation)
	if err != nil {
		b.sendScreen(ctx, chatID, hourlyDatePromptScreen(session.query, "Use a valid date such as 2026-08-25."))
		return
	}
	today := localDate(time.Now(), b.hourlyLocation)
	if searchDate.Before(today) {
		b.sendScreen(ctx, chatID, hourlyDatePromptScreen(session.query, "The date cannot be in the past."))
		return
	}
	updated, err := b.setHourlyDate(searchDate)
	if err != nil {
		b.sendScreen(ctx, chatID, queryDetailScreen(session.query, nil))
		return
	}
	b.sendScreen(ctx, chatID, hourlyIntervalScreen(updated))
}

func localDate(value time.Time, location *time.Location) time.Time {
	local := value.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
}

func firstHourlyRun(searchDate, now time.Time, location *time.Location) time.Time {
	if searchDate.Equal(localDate(now, location)) {
		return now
	}
	return time.Date(searchDate.Year(), searchDate.Month(), searchDate.Day(), 0, 0, 0, 0, location)
}

func (b *Bot) subscriptionForQuery(ctx context.Context, queryID int64) (*hourly.Subscription, error) {
	if b.hourlyStore == nil {
		return nil, nil
	}
	subscription, exists, err := b.hourlyStore.GetByQueryID(ctx, queryID)
	if err != nil || !exists {
		return nil, err
	}
	return &subscription, nil
}
