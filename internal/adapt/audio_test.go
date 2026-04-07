package adapt

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

type fakeRunner struct {
	bin   string
	args  []string
	input []byte
	out   []byte
	err   error
}

func (f *fakeRunner) Run(_ context.Context, bin string, args []string, input []byte) ([]byte, error) {
	f.bin = bin
	f.args = append([]string(nil), args...)
	f.input = append([]byte(nil), input...)
	if f.err != nil {
		return nil, f.err
	}
	return append([]byte(nil), f.out...), nil
}

func TestAudioAdapterTranscodesUnsupportedAudio(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{out: []byte("transcoded-audio")}
	adapter := NewAudioAdapter("/usr/bin/ffmpeg")
	adapter.runner = runner

	got, err := adapter.Adapt(context.Background(), []message.Part{{
		ContentType: "audio/ogg",
		Data:        []byte("source-audio"),
	}}, Constraints{
		MaxMessageSizeBytes: 1024,
		AllowedAudioTypes:   []string{"audio/mpeg"},
	})
	if err != nil {
		t.Fatalf("adapt audio: %v", err)
	}
	if len(got) != 1 || got[0].ContentType != "audio/mpeg" || string(got[0].Data) != "transcoded-audio" {
		t.Fatalf("unexpected adapted audio: %#v", got)
	}
	if runner.bin != "/usr/bin/ffmpeg" || len(runner.args) == 0 {
		t.Fatalf("expected ffmpeg invocation, got %#v", runner)
	}
}

func TestAudioAdapterReturnsRunnerFailure(t *testing.T) {
	t.Parallel()

	adapter := NewAudioAdapter("ffmpeg")
	adapter.runner = &fakeRunner{err: errors.New("boom")}

	_, err := adapter.Adapt(context.Background(), []message.Part{{
		ContentType: "audio/ogg",
		Data:        []byte("source-audio"),
	}}, Constraints{
		MaxMessageSizeBytes: 1024,
		AllowedAudioTypes:   []string{"audio/mpeg"},
	})
	if err == nil {
		t.Fatal("expected runner failure")
	}
}

func TestAudioAdapterKeepsAllowedAudio(t *testing.T) {
	t.Parallel()

	adapter := NewAudioAdapter("ffmpeg")
	got, err := adapter.Adapt(context.Background(), []message.Part{{
		ContentType: "audio/mpeg",
		Data:        []byte("source-audio"),
	}}, Constraints{
		MaxMessageSizeBytes: 1024,
		AllowedAudioTypes:   []string{"audio/mpeg"},
	})
	if err != nil {
		t.Fatalf("adapt allowed audio: %v", err)
	}
	if len(got) != 1 || got[0].ContentType != "audio/mpeg" || string(got[0].Data) != "source-audio" {
		t.Fatalf("unexpected adapted audio: %#v", got)
	}
}

type profileRunner struct {
	calls [][]string
}

func (r *profileRunner) Run(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	call := len(r.calls)
	size := 120
	switch {
	case hasArg(args, "32k"), hasArg(args, "5.9k"):
		size = 20
	case hasArg(args, "48k"), hasArg(args, "6.7k"):
		size = 40
	case hasArg(args, "64k"), hasArg(args, "7.95k"):
		size = 70
	default:
		if call > 1 {
			size = 90
		}
	}
	return []byte(strings.Repeat("a", size)), nil
}

func hasArg(args []string, needle string) bool {
	for _, arg := range args {
		if arg == needle {
			return true
		}
	}
	return false
}

func TestAudioAdapterShrinksToFitMessageSize(t *testing.T) {
	t.Parallel()

	runner := &profileRunner{}
	adapter := NewAudioAdapter("ffmpeg")
	adapter.runner = runner

	got, err := adapter.Adapt(context.Background(), []message.Part{{
		ContentType: "audio/mpeg",
		Data:        []byte(strings.Repeat("s", 120)),
	}}, Constraints{
		MaxMessageSizeBytes: 50,
		AllowedAudioTypes:   []string{"audio/mpeg"},
	})
	if err != nil {
		t.Fatalf("adapt audio: %v", err)
	}
	if len(got) != 1 || got[0].Size > 50 {
		t.Fatalf("expected audio to fit limit, got %#v", got)
	}
	if len(runner.calls) < 2 {
		t.Fatalf("expected progressive shrink attempts, got %#v", runner.calls)
	}
}
