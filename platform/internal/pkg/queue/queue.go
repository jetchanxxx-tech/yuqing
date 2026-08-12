// Package queue provides a pluggable message queue abstraction.
package queue

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// Message represents a queue message.
type Message struct {
	Body      []byte
	Topic     string
	MessageID string
}

// Handler processes a message. Return nil to ACK, error to NACK.
// Note: NACK (requeue) is not yet implemented for the memory driver;
// failed messages are logged and dropped in MVP.
type Handler func(ctx context.Context, msg Message) error

// Queue is the abstract message queue interface.
type Queue interface {
	Publish(ctx context.Context, topic string, body []byte) error
	Subscribe(ctx context.Context, topic string, handler Handler) error
	Close() error
}

// NewMemory creates an in-process queue backed by buffered channels.
func NewMemory() Queue {
	return &memoryQueue{
		topics:  make(map[string]*memoryTopic),
		closed:  false,
	}
}

type memoryTopic struct {
	ch       chan Message
	handler  Handler
	ctx      context.Context
	cancel   context.CancelFunc
}

type memoryQueue struct {
	mu     sync.Mutex
	topics map[string]*memoryTopic
	closed bool
}

func (q *memoryQueue) Publish(ctx context.Context, topic string, body []byte) error {
	msg := Message{Body: body, Topic: topic, MessageID: genMsgID()}

	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return fmt.Errorf("queue: closed")
	}

	t, ok := q.topics[topic]
	if !ok {
		// Pre-create a topic buffer so late subscribers receive it.
		t = &memoryTopic{ch: make(chan Message, 1024)}
		q.topics[topic] = t
	}
	q.mu.Unlock()

	select {
	case t.ch <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return fmt.Errorf("queue: topic %s buffer full", topic)
	}
}

func (q *memoryQueue) Subscribe(ctx context.Context, topic string, handler Handler) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return fmt.Errorf("queue: closed")
	}

	// Cancel any existing subscription for this topic.
	t, ok := q.topics[topic]
	if ok && t.cancel != nil {
		t.cancel()
	}

	if !ok {
		// No prior publish — create a fresh channel.
		t = &memoryTopic{ch: make(chan Message, 1024)}
		q.topics[topic] = t
	}

	subCtx, cancel := context.WithCancel(ctx)
	t.handler = handler
	t.ctx = subCtx
	t.cancel = cancel

	go q.dispatch(t)
	return nil
}

func (q *memoryQueue) dispatch(t *memoryTopic) {
	for {
		select {
		case msg := <-t.ch:
			if t.handler != nil {
				if err := t.handler(t.ctx, msg); err != nil {
					// NACK: message processing failed. In MVP, log and drop.
					// Future: requeue or dead-letter queue.
				}
			}
		case <-t.ctx.Done():
			return
		}
	}
}

func (q *memoryQueue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return nil
	}
	q.closed = true

	for _, t := range q.topics {
		if t.cancel != nil {
			t.cancel()
		}
	}
	q.topics = make(map[string]*memoryTopic)
	return nil
}

var msgIDSeq atomic.Int64

func genMsgID() string {
	n := msgIDSeq.Add(1)
	return fmt.Sprintf("msg_%d", n)
}
