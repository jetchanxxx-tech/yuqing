package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuqing/platform/internal/pkg/id"
)

// PGQueueOptions controls polling, leases and the maximum number of deliveries.
// Zero values use production defaults; test deployments can use shorter intervals.
type PGQueueOptions struct {
	PollInterval  time.Duration
	LeaseDuration time.Duration
	RetryDelay    time.Duration
	MaxAttempts   int
}

// PGQueue is a durable, at-least-once Queue. Handlers must be idempotent:
// a crash after business work but before ACK can replay the same message.
type PGQueue struct {
	pool   *pgxpool.Pool
	config PGQueueOptions
	mu     sync.Mutex
	closed bool
	subs   map[string]context.CancelFunc
	errors chan error
}

var _ Queue = (*PGQueue)(nil)

func NewPGQueue(pool *pgxpool.Pool, options PGQueueOptions) *PGQueue {
	if pool == nil {
		panic("queue: PostgreSQL pool required")
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 100 * time.Millisecond
	}
	if options.LeaseDuration < 3*time.Millisecond {
		options.LeaseDuration = 30 * time.Second
	}
	if options.RetryDelay <= 0 {
		options.RetryDelay = time.Second
	}
	if options.MaxAttempts <= 0 {
		options.MaxAttempts = 5
	}
	return &PGQueue{pool: pool, config: options, subs: make(map[string]context.CancelFunc), errors: make(chan error, 64)}
}

// UsesPool prevents a caller from committing messages into a different DB
// than the one used by the queue consumers.
func (q *PGQueue) UsesPool(pool *pgxpool.Pool) bool { return q.pool == pool }

// Errors reports asynchronous handler, claim, heartbeat and ACK/NACK errors.
// Failed deliveries additionally remain queryable in queue_messages.last_error.
func (q *PGQueue) Errors() <-chan error { return q.errors }

func (q *PGQueue) Publish(ctx context.Context, topic string, body []byte) error {
	if err := q.canPublish(topic); err != nil {
		return err
	}
	if body == nil {
		body = []byte{}
	}
	_, err := q.pool.Exec(ctx, `INSERT INTO queue_messages (id, topic, body, max_attempts) VALUES ($1, $2, $3, $4)`, id.New(), topic, body, q.config.MaxAttempts)
	return err
}

// PublishTx participates in the caller's PostgreSQL transaction, allowing
// business state and the pending message to commit or roll back together.
func (q *PGQueue) PublishTx(ctx context.Context, tx pgx.Tx, topic string, body []byte) error {
	if err := q.canPublish(topic); err != nil {
		return err
	}
	if tx == nil {
		return errors.New("queue: transaction required")
	}
	if body == nil {
		body = []byte{}
	}
	_, err := tx.Exec(ctx, `INSERT INTO queue_messages (id, topic, body, max_attempts) VALUES ($1, $2, $3, $4)`, id.New(), topic, body, q.config.MaxAttempts)
	return err
}

func (q *PGQueue) canPublish(topic string) error {
	if topic == "" {
		return errors.New("queue: topic required")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return errors.New("queue: closed")
	}
	return nil
}

func (q *PGQueue) Subscribe(ctx context.Context, topic string, handler Handler) error {
	if topic == "" || handler == nil {
		return errors.New("queue: topic and handler required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return errors.New("queue: closed")
	}
	if cancel := q.subs[topic]; cancel != nil {
		cancel()
	}
	subCtx, cancel := context.WithCancel(ctx)
	q.subs[topic] = cancel
	go q.dispatch(subCtx, topic, handler)
	return nil
}

func (q *PGQueue) dispatch(ctx context.Context, topic string, handler Handler) {
	ticker := time.NewTicker(q.config.PollInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if _, err := q.pool.Exec(ctx, `UPDATE queue_messages SET status = 'failed', locked_by = NULL, locked_until = NULL,
			last_error = CASE WHEN last_error = '' THEN 'lease expired after final attempt' ELSE last_error END, updated_at = now()
			WHERE topic = $1 AND status = 'processing' AND locked_until < now() AND attempts >= max_attempts`, topic); err != nil && ctx.Err() == nil {
			q.report(fmt.Errorf("queue: expire final attempts for %s: %w", topic, err))
		}
		message, token, err := q.claim(ctx, topic)
		if err != nil && ctx.Err() == nil {
			q.report(fmt.Errorf("queue: claim %s: %w", topic, err))
		}
		if message != nil {
			if ctx.Err() != nil {
				return
			}
			q.deliver(ctx, *message, token, handler)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (q *PGQueue) claim(ctx context.Context, topic string) (*Message, string, error) {
	token := id.New()
	var message Message
	err := q.pool.QueryRow(ctx, `WITH picked AS (
		SELECT id FROM queue_messages WHERE topic = $1 AND attempts < max_attempts AND (
			(status = 'pending' AND available_at <= now()) OR (status = 'processing' AND locked_until < now()))
		ORDER BY available_at, created_at, id LIMIT 1 FOR UPDATE SKIP LOCKED
	) UPDATE queue_messages AS m SET status = 'processing', attempts = m.attempts + 1,
		locked_by = $2, locked_until = now() + $3::bigint * interval '1 microsecond', updated_at = now()
		FROM picked WHERE m.id = picked.id RETURNING m.id, m.body`, topic, token, q.config.LeaseDuration.Microseconds()).Scan(&message.MessageID, &message.Body)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	message.Topic = topic
	return &message, token, nil
}

func (q *PGQueue) deliver(ctx context.Context, message Message, token string, handler Handler) {
	handlerCtx, cancel := context.WithCancel(ctx)
	heartbeatDone := make(chan struct{})
	leaseErrors := make(chan error, 1)
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(q.config.LeaseDuration / 3)
		defer ticker.Stop()
		for {
			select {
			case <-handlerCtx.Done():
				return
			case <-ticker.C:
				result, err := q.pool.Exec(handlerCtx, `UPDATE queue_messages SET locked_until = now() + $3::bigint * interval '1 microsecond', updated_at = now()
					WHERE id = $1 AND locked_by = $2 AND status = 'processing' AND locked_until > now()`, message.MessageID, token, q.config.LeaseDuration.Microseconds())
				if handlerCtx.Err() != nil {
					return
				}
				if err != nil || result.RowsAffected() != 1 {
					if err == nil {
						err = errors.New("lease lost")
					}
					leaseErr := fmt.Errorf("queue: lease renewal for %s: %w", message.MessageID, err)
					leaseErrors <- leaseErr
					q.report(leaseErr)
					cancel()
					return
				}
			}
		}
	}()
	err := func() (err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("handler panic: %v", recovered)
			}
		}()
		return handler(handlerCtx, message)
	}()
	cancel()
	<-heartbeatDone
	select {
	case leaseErr := <-leaseErrors:
		err = leaseErr
	default:
	}
	if err != nil {
		q.report(fmt.Errorf("queue: handler %s: %w", message.MessageID, err))
	}
	if ctx.Err() != nil {
		return
	}
	writeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err != nil {
		result, writeErr := q.pool.Exec(writeCtx, `UPDATE queue_messages SET
			status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'pending' END,
			available_at = now() + $3::bigint * interval '1 microsecond',
			locked_by = NULL, locked_until = NULL, last_error = $4, updated_at = now()
			WHERE id = $1 AND locked_by = $2 AND status = 'processing'`, message.MessageID, token, q.config.RetryDelay.Microseconds(), err.Error())
		if writeErr == nil && result.RowsAffected() != 1 {
			writeErr = errors.New("lease lost")
		}
		if writeErr != nil {
			q.report(fmt.Errorf("queue: NACK %s: %w", message.MessageID, writeErr))
		}
		return
	}
	result, writeErr := q.pool.Exec(writeCtx, `UPDATE queue_messages SET status = 'completed', locked_by = NULL, locked_until = NULL,
		updated_at = now(), completed_at = now() WHERE id = $1 AND locked_by = $2 AND status = 'processing'`, message.MessageID, token)
	if writeErr == nil && result.RowsAffected() != 1 {
		writeErr = errors.New("lease lost")
	}
	if writeErr != nil {
		q.report(fmt.Errorf("queue: ACK %s: %w", message.MessageID, writeErr))
	}
}

func (q *PGQueue) report(err error) {
	slog.Error("postgres queue", "error", err)
	select {
	case q.errors <- err:
	default:
	}
}

func (q *PGQueue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil
	}
	q.closed = true
	for _, cancel := range q.subs {
		cancel()
	}
	return nil
}
