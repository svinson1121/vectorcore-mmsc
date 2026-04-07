package message

import "testing"

func TestMessageCloneCopiesSlices(t *testing.T) {
	t.Parallel()

	original := Message{
		To:    []string{"+12025550100"},
		CC:    []string{"+12025550101"},
		BCC:   []string{"+12025550102"},
		Parts: []Part{{ContentType: "text/plain", Data: []byte("hello")}},
	}

	cloned := original.Clone()
	cloned.To[0] = "+12025559999"
	cloned.CC[0] = "+12025558888"
	cloned.BCC[0] = "+12025557777"
	cloned.Parts[0].ContentType = "image/jpeg"

	if original.To[0] != "+12025550100" {
		t.Fatal("expected To slice to be copied")
	}
	if original.CC[0] != "+12025550101" {
		t.Fatal("expected CC slice to be copied")
	}
	if original.BCC[0] != "+12025550102" {
		t.Fatal("expected BCC slice to be copied")
	}
	if original.Parts[0].ContentType != "text/plain" {
		t.Fatal("expected Parts slice to be copied")
	}
}
