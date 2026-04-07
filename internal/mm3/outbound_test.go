package mm3

import (
	"context"
	"strings"
	"testing"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

func TestEncodeEnvelopeNormalizesNumericSender(t *testing.T) {
	t.Parallel()

	msg := &message.Message{
		ID:      "msg-1",
		From:    "+12025550100/TYPE=PLMN",
		To:      []string{"user@example.net"},
		Subject: "Hello",
		Parts: []message.Part{
			{ContentType: "text/plain", Data: []byte("hello")},
		},
	}
	body, from, err := EncodeEnvelope(msg, db.MM3Relay{DefaultSenderDomain: "mmsc.example.net"}, "ignored.example.net")
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	if from != "+12025550100@mmsc.example.net" {
		t.Fatalf("unexpected sender: %q", from)
	}
	if !strings.Contains(string(body), "From: +12025550100@mmsc.example.net\r\n") {
		t.Fatalf("unexpected body: %s", string(body))
	}
}

func TestOutboundRequiresConfiguredRelay(t *testing.T) {
	t.Parallel()

	runtimeStore := config.NewRuntimeStore()
	sender := NewOutbound(runtimeStore, "mmsc.example.net")
	err := sender.Send(context.Background(), &message.Message{
		From:  "+12025550100",
		To:    []string{"user@example.net"},
		Parts: []message.Part{{ContentType: "text/plain", Data: []byte("hello")}},
	})
	if err == nil || !strings.Contains(err.Error(), "relay is not configured") {
		t.Fatalf("expected relay configuration error, got %v", err)
	}
}
