package message

import "time"

// ApplyDefaultExpiry ensures msg.Expiry is set and within the retention
// ceiling. If no explicit expiry was provided it defaults to
// receivedAt+defaultExpiry. The result is then clamped so it never exceeds
// receivedAt+maxRetention. If ReceivedAt is zero, time.Now() is used.
func ApplyDefaultExpiry(msg *Message, defaultExpiry, maxRetention time.Duration) {
	ref := msg.ReceivedAt
	if ref.IsZero() {
		ref = time.Now().UTC()
	}
	ceiling := ref.Add(maxRetention)
	if msg.Expiry == nil {
		t := ref.Add(defaultExpiry)
		msg.Expiry = &t
	}
	if msg.Expiry.After(ceiling) {
		msg.Expiry = &ceiling
	}
}

// Message is the canonical MMS representation used across all interfaces.
type Message struct {
	ID            string
	TransactionID string
	Status        Status
	Direction     Direction

	From string
	To   []string
	CC   []string
	BCC  []string

	Subject     string
	Parts       []Part
	ContentType string

	MMSVersion     string
	MessageClass   MessageClass
	Priority       Priority
	DeliveryReport bool
	ReadReport     bool
	Expiry         *time.Time
	DeliveryTime   *time.Time
	MessageSize    int64

	ContentPath string
	StoreID     string

	Origin     Interface
	OriginHost string

	ReceivedAt time.Time
	UpdatedAt  time.Time
}

func (m Message) Clone() Message {
	cloned := m
	cloned.To = append([]string(nil), m.To...)
	cloned.CC = append([]string(nil), m.CC...)
	cloned.BCC = append([]string(nil), m.BCC...)
	cloned.Parts = append([]Part(nil), m.Parts...)
	return cloned
}
