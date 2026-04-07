package adapt

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

func TestVideoAdapterTranscodesUnsupportedVideo(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{out: []byte("transcoded-video")}
	adapter := NewVideoAdapter("/usr/bin/ffmpeg")
	adapter.runner = runner

	got, err := adapter.Adapt(context.Background(), []message.Part{{
		ContentType: "video/quicktime",
		Data:        []byte("source-video"),
	}}, Constraints{
		MaxMessageSizeBytes: 4096,
		AllowedVideoTypes:   []string{"video/mp4"},
	})
	if err != nil {
		t.Fatalf("adapt video: %v", err)
	}
	if len(got) != 1 || got[0].ContentType != "video/mp4" || string(got[0].Data) != "transcoded-video" {
		t.Fatalf("unexpected adapted video: %#v", got)
	}
	if runner.bin != "/usr/bin/ffmpeg" || len(runner.args) == 0 {
		t.Fatalf("expected ffmpeg invocation, got %#v", runner)
	}
}

func TestVideoAdapterReturnsRunnerFailure(t *testing.T) {
	t.Parallel()

	adapter := NewVideoAdapter("ffmpeg")
	adapter.runner = &fakeRunner{err: errors.New("boom")}

	_, err := adapter.Adapt(context.Background(), []message.Part{{
		ContentType: "video/quicktime",
		Data:        []byte("source-video"),
	}}, Constraints{
		MaxMessageSizeBytes: 4096,
		AllowedVideoTypes:   []string{"video/mp4"},
	})
	if err == nil {
		t.Fatal("expected runner failure")
	}
}

func TestVideoAdapterKeepsAllowedVideo(t *testing.T) {
	t.Parallel()

	adapter := NewVideoAdapter("ffmpeg")
	got, err := adapter.Adapt(context.Background(), []message.Part{{
		ContentType: "video/mp4",
		Data:        []byte("source-video"),
	}}, Constraints{
		MaxMessageSizeBytes: 4096,
		AllowedVideoTypes:   []string{"video/mp4"},
	})
	if err != nil {
		t.Fatalf("adapt allowed video: %v", err)
	}
	if len(got) != 1 || got[0].ContentType != "video/mp4" || string(got[0].Data) != "source-video" {
		t.Fatalf("unexpected adapted video: %#v", got)
	}
}

type videoProfileRunner struct {
	calls [][]string
}

func (r *videoProfileRunner) Run(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	size := 220
	switch {
	case hasVideoArg(args, "scale=240:180"), hasVideoArg(args, "scale=176:144"):
		size = 40
	case hasVideoArg(args, "scale=320:240"):
		size = 80
	case hasVideoArg(args, "scale=426:320"):
		size = 140
	}
	return []byte(strings.Repeat("v", size)), nil
}

func hasVideoArg(args []string, needle string) bool {
	for _, arg := range args {
		if arg == needle {
			return true
		}
	}
	return false
}

func TestVideoAdapterShrinksToFitMessageSize(t *testing.T) {
	t.Parallel()

	runner := &videoProfileRunner{}
	adapter := NewVideoAdapter("ffmpeg")
	adapter.runner = runner

	got, err := adapter.Adapt(context.Background(), []message.Part{{
		ContentType: "video/mp4",
		Data:        []byte(strings.Repeat("s", 220)),
	}}, Constraints{
		MaxMessageSizeBytes: 60,
		AllowedVideoTypes:   []string{"video/mp4"},
	})
	if err != nil {
		t.Fatalf("adapt video: %v", err)
	}
	if len(got) != 1 || got[0].Size > 60 {
		t.Fatalf("expected video to fit limit, got %#v", got)
	}
	if len(runner.calls) < 3 {
		t.Fatalf("expected progressive shrink attempts, got %#v", runner.calls)
	}
}
