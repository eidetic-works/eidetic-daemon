package ccr

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type WakeEvent struct {
	Event     string `json:"event"`
	AgentRole string `json:"agent_role"`
	Trigger   string `json:"trigger"`
	Payload   any    `json:"payload"`
	FiredAt   string `json:"fired_at"`
}

type Subscription struct {
	ID        string
	Role      string
	Ch        chan WakeEvent
	LastHeart time.Time
}

type Broker struct {
	mu           sync.RWMutex
	subs         map[string]*Subscription
	heartbeatTTL time.Duration
}

func NewBroker(heartbeatTTL time.Duration) *Broker {
	b := &Broker{
		subs:         make(map[string]*Subscription),
		heartbeatTTL: heartbeatTTL,
	}
	go b.janitor()
	return b
}

func (b *Broker) Subscribe(role string) *Subscription {
	idBytes := make([]byte, 16)
	rand.Read(idBytes)
	id := hex.EncodeToString(idBytes)

	sub := &Subscription{
		ID:        id,
		Role:      role,
		Ch:        make(chan WakeEvent, 10),
		LastHeart: time.Now(),
	}

	b.mu.Lock()
	b.subs[id] = sub
	b.mu.Unlock()

	return sub
}

func (b *Broker) Unsubscribe(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if sub, ok := b.subs[id]; ok {
		close(sub.Ch)
		delete(b.subs, id)
	}
}

func (b *Broker) Heartbeat(id string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if sub, ok := b.subs[id]; ok {
		sub.LastHeart = time.Now()
		return true
	}
	return false
}

func (b *Broker) WakeActive(role string, trigger string, payload any) {
	event := WakeEvent{
		Event:     "wake_active",
		AgentRole: role,
		Trigger:   trigger,
		Payload:   payload,
		FiredAt:   time.Now().UTC().Format(time.RFC3339),
	}

	// Trigger macOS push notification
	go Notify("Eidetic CCR Wake", "Role: "+role+" - "+trigger)

	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, sub := range b.subs {
		if sub.Role == role {
			select {
			case sub.Ch <- event:
			default:
				// Channel full, drop or handle?
			}
		}
	}
}

func (b *Broker) janitor() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		b.mu.Lock()
		for id, sub := range b.subs {
			// 3x heartbeat interval as per spec
			if now.Sub(sub.LastHeart) > b.heartbeatTTL*3 {
				close(sub.Ch)
				delete(b.subs, id)
			}
		}
		b.mu.Unlock()
	}
}
