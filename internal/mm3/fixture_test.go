package mm3

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

func TestEncodeEnvelopeMatchesFixture(t *testing.T) {
	t.Parallel()

	msg := &message.Message{
		ID:      "msg-1",
		From:    "+12025550100/TYPE=PLMN",
		To:      []string{"user@example.net"},
		Subject: "Hello",
		Origin:  message.InterfaceMM1,
		Parts: []message.Part{{
			ContentType: "text/plain",
			Data:        []byte("hello"),
		}},
	}

	body, from, err := EncodeEnvelope(msg, db.MM3Relay{DefaultSenderDomain: "mmsc.example.net"}, "ignored.example.net")
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	if from != "+12025550100@mmsc.example.net" {
		t.Fatalf("unexpected sender: %q", from)
	}

	path := filepath.Join("..", "..", "test", "fixtures", "mm3", "outbound_singlepart.eml")
	fixture, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	expected := strings.ReplaceAll(strings.TrimSuffix(string(fixture), "\n"), "\n", "\r\n")
	if string(body) != expected {
		t.Fatalf("fixture mismatch:\nexpected:\n%s\ngot:\n%s", expected, string(body))
	}
}
