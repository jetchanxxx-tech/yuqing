// Package notificationsandbox is imported only by tests. It retains transient
// payloads in process and has no network, persistent store or production route.
package notificationsandbox

import (
	"fmt"
	"sync"
)

type Message struct {
	Recipient, Purpose, Payload string
	Delivered                   bool
}
type Inbox struct {
	mu       sync.Mutex
	messages []Message
}

func (i *Inbox) Accept(to, purpose, payload string) int {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.messages = append(i.messages, Message{Recipient: to, Purpose: purpose, Payload: payload})
	return len(i.messages) - 1
}
func (i *Inbox) Accepted(index int) (Message, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if index < 0 || index >= len(i.messages) {
		return Message{}, fmt.Errorf("sandbox message absent")
	}
	return i.messages[index], nil
}
func (i *Inbox) Deliver(index int) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if index < 0 || index >= len(i.messages) {
		return fmt.Errorf("sandbox message absent")
	}
	i.messages[index].Delivered = true
	return nil
}
func (i *Inbox) Delivered(index int) (Message, error) {
	m, err := i.Accepted(index)
	if err != nil {
		return m, err
	}
	if !m.Delivered {
		return Message{}, fmt.Errorf("sandbox message not delivered")
	}
	return m, nil
}
