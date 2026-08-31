package daily

import (
	"context"
	"log/slog"
	"time"
)

const (
	outboxPollInterval = 30 * time.Second
	outboxLease        = 2 * time.Minute
	outboxSendTimeout  = 30 * time.Second
)

type outboxStore interface {
	ClaimOutbox(context.Context, time.Duration) (OutboxMessage, bool, error)
	MarkOutboxSent(context.Context, OutboxMessage) error
	RetryOutbox(context.Context, OutboxMessage, time.Time, error) error
}

type digestSender interface {
	SendDailyDigest(context.Context, int64, Report) error
}

// Dispatcher provides at-least-once Telegram delivery. A crash after Telegram
// accepts a message but before MarkOutboxSent commits can produce a duplicate;
// Telegram does not expose an idempotency key that can close that final gap.
type Dispatcher struct {
	store  outboxStore
	sender digestSender
	logger *slog.Logger
	now    func() time.Time
	tick   time.Duration
}

func NewDispatcher(store outboxStore, sender digestSender, logger *slog.Logger) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Dispatcher{store: store, sender: sender, logger: logger, now: time.Now, tick: outboxPollInterval}
}

func (d *Dispatcher) Run(ctx context.Context) error {
	d.drain(ctx)
	ticker := time.NewTicker(d.tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			d.drain(ctx)
		}
	}
}

func (d *Dispatcher) drain(ctx context.Context) {
	for ctx.Err() == nil {
		message, ok, err := d.store.ClaimOutbox(ctx, outboxLease)
		if err != nil {
			d.logger.Error("claim daily digest outbox", "error", err)
			return
		}
		if !ok {
			return
		}
		sendCtx, cancel := context.WithTimeout(ctx, outboxSendTimeout)
		err = d.sender.SendDailyDigest(sendCtx, message.ChatID, message.Report)
		cancel()
		if err == nil {
			if markErr := d.store.MarkOutboxSent(ctx, message); markErr != nil {
				d.logger.Error("mark daily digest delivered", "outbox_id", message.ID, "error", markErr)
				return
			}
			continue
		}

		next := d.now().UTC().Add(notificationRetryDelay(message.Attempts))
		if retryErr := d.store.RetryOutbox(ctx, message, next, err); retryErr != nil {
			d.logger.Error("schedule daily digest retry", "outbox_id", message.ID, "error", retryErr)
			return
		}
		d.logger.Warn("daily digest delivery failed; retry scheduled",
			"outbox_id", message.ID, "attempt", message.Attempts, "next_attempt", next, "error", err)
	}
}

func notificationRetryDelay(attempt int) time.Duration {
	switch attempt {
	case 0, 1:
		return time.Minute
	case 2:
		return 5 * time.Minute
	case 3:
		return 15 * time.Minute
	case 4:
		return time.Hour
	default:
		return 6 * time.Hour
	}
}
