package wappush

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestNotificationFixtureWrapAndSegment(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "test", "pdus", "m-notification-ind.bin")
	pdu, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read notification fixture: %v", err)
	}

	push := WrapMMSPDU(pdu)
	unwrapped, err := UnwrapMMSPDU(push)
	if err != nil {
		t.Fatalf("unwrap push: %v", err)
	}
	if !bytes.Equal(unwrapped, pdu) {
		t.Fatal("notification fixture did not survive wap push wrap/unwrap")
	}

	segments, err := SegmentBinarySMS(push, 0x42)
	if err != nil {
		t.Fatalf("segment push: %v", err)
	}
	if len(segments) < 1 {
		t.Fatal("expected at least one segment")
	}
	if segments[0][3] != 0x0B || segments[0][4] != 0x84 {
		t.Fatalf("unexpected destination port bytes: %x", segments[0][3:5])
	}
	var reassembled []byte
	for i, segment := range segments {
		if len(segment) < 12 {
			t.Fatalf("segment %d too short", i)
		}
		if segment[9] != 0x42 {
			t.Fatalf("segment %d wrong concat reference: %x", i, segment[9])
		}
		reassembled = append(reassembled, segment[12:]...)
	}
	if !bytes.Equal(reassembled, push) {
		t.Fatal("segmented notification push did not reassemble to original push payload")
	}
}
