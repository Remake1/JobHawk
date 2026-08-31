package daily

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"jobhawk/internal/ashby"
	"jobhawk/internal/database/db"
	"jobhawk/internal/greenhouse"
	"jobhawk/internal/jobs"
	"jobhawk/internal/jobstore"
	"jobhawk/internal/searchqueries"
	"jobhawk/internal/textsearch"
	"jobhawk/internal/workday"
)

const queryLeaseDuration = 5 * time.Minute

type transactionBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

type Run struct {
	ID            int64
	ScheduledDate time.Time
	RunType       string
	Status        string
	TargetChatID  int64
	QueryCount    int
}

type ClaimedQuery struct {
	ID                int64
	DailyRunID        int64
	LeaseToken        string
	RateLimitAttempts int
	Query             searchqueries.Query
}

type OutboxMessage struct {
	ID         int64
	DailyRunID int64
	ChatID     int64
	Attempts   int
	LeaseToken string
	Report     Report
}

// Store owns the transactions that connect run checkpoints, newly discovered
// jobs, and the notification outbox.
type Store struct {
	db      transactionBeginner
	queries *db.Queries
}

func NewStore(database transactionBeginner, queries *db.Queries) *Store {
	return &Store{db: database, queries: queries}
}

func (s *Store) StartScheduled(ctx context.Context, date time.Time, chatID int64, queries []searchqueries.Query) (Run, error) {
	return s.start(ctx, date, chatID, queries, true)
}

func (s *Store) StartManual(ctx context.Context, date time.Time, chatID int64, queries []searchqueries.Query) (Run, error) {
	return s.start(ctx, date, chatID, queries, false)
}

func (s *Store) start(ctx context.Context, date time.Time, chatID int64, searchQueries []searchqueries.Query, scheduled bool) (Run, error) {
	if chatID == 0 {
		return Run{}, errors.New("daily run target chat ID is required")
	}
	if len(searchQueries) > math.MaxInt32 {
		return Run{}, errors.New("too many search queries in daily run")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Run{}, fmt.Errorf("begin daily run transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := s.queries.WithTx(tx)

	var row db.DailyRun
	created := true
	if scheduled {
		key := date.Format("2006-01-02")
		row, err = qtx.InsertScheduledDailyRun(ctx, db.InsertScheduledDailyRunParams{
			ScheduledDate: pgDate(date),
			ScheduleKey:   pgtype.Text{String: key, Valid: true},
			TargetChatID:  chatID,
			QueryCount:    int32(len(searchQueries)),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			created = false
			row, err = qtx.GetDailyRunByScheduleKey(ctx, pgtype.Text{String: key, Valid: true})
		}
	} else {
		row, err = qtx.InsertManualDailyRun(ctx, db.InsertManualDailyRunParams{
			ScheduledDate: pgDate(date), TargetChatID: chatID, QueryCount: int32(len(searchQueries)),
		})
	}
	if err != nil {
		return Run{}, fmt.Errorf("create daily run: %w", err)
	}
	if created {
		for _, query := range searchQueries {
			filters, encodeErr := encodeQueryFilters(query)
			if encodeErr != nil {
				return Run{}, fmt.Errorf("snapshot query %q: %w", query.Name, encodeErr)
			}
			if err := qtx.InsertQueryRun(ctx, db.InsertQueryRunParams{
				DailyRunID: row.ID, SearchQueryID: query.ID, QueryName: query.Name,
				SourceType: string(query.SourceType), Filters: filters,
			}); err != nil {
				return Run{}, fmt.Errorf("snapshot query %q: %w", query.Name, err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, fmt.Errorf("commit daily run: %w", err)
	}
	return decodeRun(row)
}

func (s *Store) ListIncomplete(ctx context.Context) ([]Run, error) {
	rows, err := s.queries.ListIncompleteDailyRuns(ctx)
	if err != nil {
		return nil, fmt.Errorf("list incomplete daily runs: %w", err)
	}
	runs := make([]Run, 0, len(rows))
	for _, row := range rows {
		run, err := decodeRun(row)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func (s *Store) ClaimNext(ctx context.Context, runID int64) (ClaimedQuery, bool, error) {
	token, err := leaseToken()
	if err != nil {
		return ClaimedQuery{}, false, err
	}
	row, err := s.queries.ClaimNextQueryRun(ctx, db.ClaimNextQueryRunParams{
		RunID:        runID,
		LeaseToken:   pgtype.Text{String: token, Valid: true},
		LeaseSeconds: int64(queryLeaseDuration / time.Second),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ClaimedQuery{}, false, nil
	}
	if err != nil {
		return ClaimedQuery{}, false, fmt.Errorf("claim daily query: %w", err)
	}
	query, err := decodeQueryRun(row)
	if err != nil {
		_, failErr := s.queries.MarkQueryRunFailed(ctx, db.MarkQueryRunFailedParams{
			ID: row.ID, LeaseToken: pgtype.Text{String: token, Valid: true}, ErrorText: err.Error(),
		})
		return ClaimedQuery{}, false, errors.Join(fmt.Errorf("decode daily query snapshot: %w", err), failErr)
	}
	return ClaimedQuery{
		ID: row.ID, DailyRunID: row.DailyRunID, LeaseToken: token,
		RateLimitAttempts: int(row.RateLimitAttempts), Query: query,
	}, true, nil
}

func (s *Store) RenewLease(ctx context.Context, claimed ClaimedQuery) error {
	rows, err := s.queries.RenewQueryRunLease(ctx, db.RenewQueryRunLeaseParams{
		ID:           claimed.ID,
		LeaseToken:   pgtype.Text{String: claimed.LeaseToken, Valid: true},
		LeaseSeconds: int64(queryLeaseDuration / time.Second),
	})
	if err != nil {
		return fmt.Errorf("renew daily query lease: %w", err)
	}
	if rows != 1 {
		return errors.New("daily query lease was lost")
	}
	return nil
}

func (s *Store) CompleteQuery(ctx context.Context, claimed ClaimedQuery, discovered []jobstore.Discovered) error {
	if len(discovered) > math.MaxInt32 {
		return errors.New("too many jobs returned by daily query")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin query completion transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := s.queries.WithTx(tx)
	txJobs := jobstore.NewStore(qtx)
	newCount := 0
	for _, candidate := range discovered {
		stored, err := txJobs.UpsertWithID(ctx, candidate)
		if err != nil {
			return err
		}
		if stored.IsNew {
			newCount++
			if err := qtx.InsertDailyRunJob(ctx, db.InsertDailyRunJobParams{
				DailyRunID: claimed.DailyRunID, JobID: stored.DatabaseID,
			}); err != nil {
				return fmt.Errorf("attribute new job to daily run: %w", err)
			}
		}
	}
	rows, err := qtx.MarkQueryRunSucceeded(ctx, db.MarkQueryRunSucceededParams{
		ID: claimed.ID, LeaseToken: pgtype.Text{String: claimed.LeaseToken, Valid: true},
		JobsFound: int32(len(discovered)), NewJobs: int32(newCount),
	})
	if err != nil {
		return fmt.Errorf("complete daily query: %w", err)
	}
	if rows != 1 {
		return errors.New("complete daily query: lease was lost")
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit daily query: %w", err)
	}
	return nil
}

func (s *Store) FailQuery(ctx context.Context, claimed ClaimedQuery, queryErr error) error {
	message := "unknown query failure"
	if queryErr != nil {
		message = queryErr.Error()
	}
	rows, err := s.queries.MarkQueryRunFailed(ctx, db.MarkQueryRunFailedParams{
		ID: claimed.ID, LeaseToken: pgtype.Text{String: claimed.LeaseToken, Valid: true}, ErrorText: message,
	})
	if err != nil {
		return fmt.Errorf("fail daily query: %w", err)
	}
	if rows != 1 {
		return errors.New("fail daily query: lease was lost")
	}
	return nil
}

func (s *Store) RetryRateLimitedQuery(ctx context.Context, claimed ClaimedQuery, next time.Time, queryErr error) error {
	message := "provider rate limit"
	if queryErr != nil {
		message = queryErr.Error()
	}
	rows, err := s.queries.RetryRateLimitedQueryRun(ctx, db.RetryRateLimitedQueryRunParams{
		ID: claimed.ID, LeaseToken: pgtype.Text{String: claimed.LeaseToken, Valid: true},
		NextAttemptAt: pgTimestamptz(next), ErrorText: message,
	})
	if err != nil {
		return fmt.Errorf("schedule rate-limited query retry: %w", err)
	}
	if rows != 1 {
		return errors.New("schedule rate-limited query retry: lease was lost")
	}
	return nil
}

// Finalize atomically marks a run complete and creates its one outbox row.
func (s *Store) Finalize(ctx context.Context, run Run) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin daily run finalization: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := s.queries.WithTx(tx)
	unfinished, err := qtx.CountUnfinishedQueryRuns(ctx, run.ID)
	if err != nil {
		return false, fmt.Errorf("count unfinished daily queries: %w", err)
	}
	if unfinished != 0 {
		return false, nil
	}
	report, err := loadReport(ctx, qtx, run)
	if err != nil {
		return false, err
	}
	payload, err := marshalReport(report)
	if err != nil {
		return false, err
	}
	if err := qtx.InsertNotificationOutbox(ctx, db.InsertNotificationOutboxParams{
		DailyRunID: run.ID, ChatID: run.TargetChatID, Payload: payload,
	}); err != nil {
		return false, fmt.Errorf("enqueue daily digest: %w", err)
	}
	rows, err := qtx.MarkDailyRunCompleted(ctx, db.MarkDailyRunCompletedParams{
		ID: run.ID, FailureCount: int32(len(report.Failures)),
	})
	if err != nil {
		return false, fmt.Errorf("complete daily run: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit daily run finalization: %w", err)
	}
	return rows == 1, nil
}

func (s *Store) ClaimOutbox(ctx context.Context, leaseDuration time.Duration) (OutboxMessage, bool, error) {
	token, err := leaseToken()
	if err != nil {
		return OutboxMessage{}, false, err
	}
	row, err := s.queries.ClaimNotificationOutbox(ctx, db.ClaimNotificationOutboxParams{
		LeaseToken:   pgtype.Text{String: token, Valid: true},
		LeaseSeconds: int64(leaseDuration / time.Second),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return OutboxMessage{}, false, nil
	}
	if err != nil {
		return OutboxMessage{}, false, fmt.Errorf("claim notification outbox: %w", err)
	}
	report, err := unmarshalReport(row.Payload)
	if err != nil {
		_, failErr := s.queries.FailNotificationOutbox(ctx, db.FailNotificationOutboxParams{
			ID: row.ID, LeaseToken: pgtype.Text{String: token, Valid: true}, LastError: err.Error(),
		})
		return OutboxMessage{}, false, errors.Join(err, failErr)
	}
	return OutboxMessage{
		ID: row.ID, DailyRunID: row.DailyRunID, ChatID: row.ChatID,
		Attempts: int(row.Attempts), LeaseToken: token, Report: report,
	}, true, nil
}

func (s *Store) MarkOutboxSent(ctx context.Context, message OutboxMessage) error {
	rows, err := s.queries.MarkNotificationOutboxSent(ctx, db.MarkNotificationOutboxSentParams{
		ID: message.ID, LeaseToken: pgtype.Text{String: message.LeaseToken, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("mark notification sent: %w", err)
	}
	if rows != 1 {
		return errors.New("mark notification sent: lease was lost")
	}
	return nil
}

func (s *Store) RetryOutbox(ctx context.Context, message OutboxMessage, next time.Time, sendErr error) error {
	errorText := "notification delivery failed"
	if sendErr != nil {
		errorText = sendErr.Error()
	}
	rows, err := s.queries.RetryNotificationOutbox(ctx, db.RetryNotificationOutboxParams{
		ID: message.ID, LeaseToken: pgtype.Text{String: message.LeaseToken, Valid: true},
		NextAttemptAt: pgTimestamptz(next), LastError: errorText,
	})
	if err != nil {
		return fmt.Errorf("schedule notification retry: %w", err)
	}
	if rows != 1 {
		return errors.New("schedule notification retry: lease was lost")
	}
	return nil
}

func encodeQueryFilters(query searchqueries.Query) (json.RawMessage, error) {
	var value any
	switch query.SourceType {
	case searchqueries.SourceAshby:
		if query.Ashby == nil {
			return nil, errors.New("Ashby filters are missing")
		}
		value = query.Ashby
	case searchqueries.SourceGreenhouse:
		if query.Greenhouse == nil {
			return nil, errors.New("Greenhouse filters are missing")
		}
		value = query.Greenhouse
	case searchqueries.SourceWorkday:
		if query.Workday == nil {
			return nil, errors.New("Workday filters are missing")
		}
		value = query.Workday
	case searchqueries.SourceText:
		if query.Text == nil {
			return nil, errors.New("text search filters are missing")
		}
		value = query.Text
	default:
		return nil, fmt.Errorf("unsupported source %q", query.SourceType)
	}
	return json.Marshal(value)
}

func decodeQueryRun(row db.QueryRun) (searchqueries.Query, error) {
	query := searchqueries.Query{ID: row.SearchQueryID, Name: row.QueryName, SourceType: searchqueries.SourceType(row.SourceType)}
	switch query.SourceType {
	case searchqueries.SourceAshby:
		query.Ashby = new(ashby.Filters)
		if err := json.Unmarshal(row.Filters, query.Ashby); err != nil {
			return searchqueries.Query{}, err
		}
	case searchqueries.SourceGreenhouse:
		query.Greenhouse = new(greenhouse.Filters)
		if err := json.Unmarshal(row.Filters, query.Greenhouse); err != nil {
			return searchqueries.Query{}, err
		}
	case searchqueries.SourceWorkday:
		query.Workday = new(workday.Filters)
		if err := json.Unmarshal(row.Filters, query.Workday); err != nil {
			return searchqueries.Query{}, err
		}
	case searchqueries.SourceText:
		query.Text = new(textsearch.Filters)
		if err := json.Unmarshal(row.Filters, query.Text); err != nil {
			return searchqueries.Query{}, err
		}
	default:
		return searchqueries.Query{}, fmt.Errorf("query run %d has unsupported source %q", row.ID, row.SourceType)
	}
	return query, nil
}

func decodeRun(row db.DailyRun) (Run, error) {
	if !row.ScheduledDate.Valid {
		return Run{}, fmt.Errorf("daily run %d has an invalid scheduled date", row.ID)
	}
	return Run{ID: row.ID, ScheduledDate: row.ScheduledDate.Time, RunType: row.RunType,
		Status: row.Status, TargetChatID: row.TargetChatID, QueryCount: int(row.QueryCount)}, nil
}

func loadReport(ctx context.Context, queries *db.Queries, run Run) (Report, error) {
	jobRows, err := queries.ListDailyRunJobs(ctx, run.ID)
	if err != nil {
		return Report{}, fmt.Errorf("load new jobs for daily digest: %w", err)
	}
	failedRows, err := queries.ListFailedQueryRuns(ctx, run.ID)
	if err != nil {
		return Report{}, fmt.Errorf("load failures for daily digest: %w", err)
	}
	report := Report{QueryCount: run.QueryCount, NewJobs: make([]jobs.Job, 0, len(jobRows)), Failures: make([]QueryFailure, 0, len(failedRows))}
	for _, row := range jobRows {
		job := jobs.Job{ID: row.ExternalID, Title: row.Title, Company: row.Company, Location: row.Location, URL: row.Url}
		if row.PostedAt.Valid {
			job.PostedAt = row.PostedAt.Time
		}
		report.NewJobs = append(report.NewJobs, job)
	}
	for _, row := range failedRows {
		report.Failures = append(report.Failures, QueryFailure{QueryName: row.QueryName, Error: errors.New(row.ErrorText)})
	}
	return report, nil
}

type reportPayload struct {
	QueryCount int              `json:"query_count"`
	NewJobs    []jobs.Job       `json:"new_jobs"`
	Failures   []failurePayload `json:"failures"`
}

type failurePayload struct {
	QueryName string `json:"query_name"`
	Error     string `json:"error"`
}

func marshalReport(report Report) (json.RawMessage, error) {
	payload := reportPayload{QueryCount: report.QueryCount, NewJobs: report.NewJobs, Failures: make([]failurePayload, 0, len(report.Failures))}
	for _, failure := range report.Failures {
		message := ""
		if failure.Error != nil {
			message = failure.Error.Error()
		}
		payload.Failures = append(payload.Failures, failurePayload{QueryName: failure.QueryName, Error: message})
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode daily digest payload: %w", err)
	}
	return encoded, nil
}

func unmarshalReport(encoded json.RawMessage) (Report, error) {
	var payload reportPayload
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return Report{}, fmt.Errorf("decode daily digest payload: %w", err)
	}
	report := Report{QueryCount: payload.QueryCount, NewJobs: payload.NewJobs, Failures: make([]QueryFailure, 0, len(payload.Failures))}
	for _, failure := range payload.Failures {
		report.Failures = append(report.Failures, QueryFailure{QueryName: failure.QueryName, Error: errors.New(failure.Error)})
	}
	return report, nil
}

func pgDate(value time.Time) pgtype.Date {
	return pgtype.Date{Time: time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
}

func pgTimestamptz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func leaseToken() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate lease token: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}
