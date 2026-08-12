// Package queue provides a pluggable message queue abstraction.
package queue

import (
	"context"
	"fmt"
	"sync"
)

// Message represents a queue message.
type Message struct {
	Body      []byte
	Topic     string
	MessageID string
}

// Handler processes a message. Return nil to ACK, error to NACK.
type Handler func(ctx context.Context, msg Message) error

// Queue is the abstract message queue interface.
type Queue interface {
	Publish(ctx context.Context, topic string, body []byte) error
	Subscribe(ctx context.Context, topic string, handler Handler) error
	Close() error
}

// NewMemory creates an in-process queue backed by buffered channels.
// Messages published before a subscriber are buffered (up to 1024 per topic).
func NewMemory() Queue {
	return &memoryQueue{
		topics: make(map[string]*memoryTopic),
		closed: false,
	}
}

type memoryTopic struct {
	ch      chan Message
	handler Handler
	ctx     context.Context
}

type memoryQueue struct {
	mu     sync.RWMutex
	topics map[string]*memoryTopic
	closed bool
}

func (q *memoryQueue) Publish(ctx context.Context, topic string, body []byte) error {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.closed {
		return fmt.Errorf("queue: closed")
	}

	msg := Message{Body: body, Topic: topic, MessageID: genMsgID()}

	t, ok := q.topics[topic]
	if !ok {
		// Buffer for late subscribers.
		t = &memoryTopic{ch: make(chan Message, 1024)}
		q.topics[topic] = t
	}

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

	t, ok := q.topics[topic]
	if !ok {
		t = &memoryTopic{ch: make(chan Message, 1024)}
		q.topics[topic] = t
	}
	t.handler = handler
	t.ctx = ctx

	go q.dispatch(t)
	return nil
}

func (q *memoryQueue) dispatch(t *memoryTopic) {
	for {
		select {
		case msg := <-t.ch:
			if t.handler != nil {
				_ = t.handler(t.ctx, msg)
			}
		case <-t.ctx.Done():
			return
		}
	}
}

func (q *memoryQueue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	for _, t := range q.topics {
		if t.ctx != nil {
			// context cancellation stops dispatchers.
		}
	}
	return nil
}

var msgIDCounter int
var msgIDMu sync.Mutex

func genMsgID() string {
	msgIDMu.Lock()
	defer msgIDMu.Unlock()
	msgIDCounter++
	return fmt.Sprintf("msg_%d", msgIDCounter)
}
