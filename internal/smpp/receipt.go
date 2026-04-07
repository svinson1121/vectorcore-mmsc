package smpp

import (
	"fmt"
	"strings"
	"time"
)

type DeliveryReceipt struct {
	ID          string
	Sub         string
	Dlvrd       string
	SubmitDate  string
	DoneDate    string
	Stat        string
	Err         string
	Text        string
	SubmittedAt *time.Time
	DoneAt      *time.Time
}

func ParseDeliveryReceipt(text string) (*DeliveryReceipt, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("smpp: empty delivery receipt")
	}

	fields := map[string]string{}
	for _, token := range strings.Fields(text) {
		key, value, ok := strings.Cut(token, ":")
		if !ok {
			continue
		}
		fields[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}

	receipt := &DeliveryReceipt{
		ID:         fields["id"],
		Sub:        fields["sub"],
		Dlvrd:      fields["dlvrd"],
		SubmitDate: fields["submit"],
		DoneDate:   fields["done"],
		Stat:       fields["stat"],
		Err:        fields["err"],
		Text:       fields["text"],
	}
	if receipt.ID == "" && receipt.Stat == "" {
		return nil, fmt.Errorf("smpp: unsupported delivery receipt format")
	}
	if ts, err := parseReceiptTime(receipt.SubmitDate); err == nil {
		receipt.SubmittedAt = ts
	}
	if ts, err := parseReceiptTime(receipt.DoneDate); err == nil {
		receipt.DoneAt = ts
	}
	return receipt, nil
}

func parseReceiptTime(value string) (*time.Time, error) {
	if len(value) < 10 {
		return nil, fmt.Errorf("invalid receipt time")
	}
	parsed, err := time.Parse("0601021504", value[:10])
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
