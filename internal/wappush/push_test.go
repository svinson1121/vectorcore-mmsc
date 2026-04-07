package wappush

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestWrapAndUnwrapMMSPDU(t *testing.T) {
	t.Parallel()

	original := []byte{0x8c, 0x82, 0x98, 0x31}
	push := WrapMMSPDU(original)

	// Byte 0: Transaction-ID, byte 1: PDU-Type (Push).
	if push[0] != wspTransactionID {
		t.Fatalf("expected transaction id 0x%02x, got 0x%02x", wspTransactionID, push[0])
	}
	if push[1] != wspPushPDU {
		t.Fatalf("expected push pdu type 0x%02x, got 0x%02x", wspPushPDU, push[1])
	}

	// Byte 2: Headers-Length must be consistent with the header block.
	headersLen := int(push[2])
	mmsOffset := 3 + headersLen
	if len(push) < mmsOffset {
		t.Fatalf("push too short for declared headers_len=%d", headersLen)
	}

	// Content-Type must be the well-known token 0xBE (application/vnd.wap.mms-message).
	// Real UE WAP push dispatchers only recognise this token; verbose text form is ignored.
	if push[3] != 0xBE {
		t.Fatalf("expected content-type well-known token 0xBE, got 0x%02x", push[3])
	}

	// Content-Length header must be present and encode len(original).
	headers := push[3:mmsOffset]
	clIdx := bytes.IndexByte(headers, wspHeaderContLen)
	if clIdx < 0 {
		t.Fatal("content-length header (0x8D) not found in wsp headers")
	}
	clRaw := headers[clIdx+1:]
	var contentLen int
	if clRaw[0]&0x80 != 0 {
		// short-integer
		contentLen = int(clRaw[0] & 0x7F)
	} else {
		// long-integer: clRaw[0] is the byte count
		n := int(clRaw[0])
		buf := make([]byte, 8)
		copy(buf[8-n:], clRaw[1:1+n])
		contentLen = int(binary.BigEndian.Uint64(buf))
	}
	if contentLen != len(original) {
		t.Fatalf("content-length %d != payload length %d", contentLen, len(original))
	}

	// MMS PDU after the header block must equal original.
	unwrapped, err := UnwrapMMSPDU(push)
	if err != nil {
		t.Fatalf("unwrap push: %v", err)
	}
	if !bytes.Equal(unwrapped, original) {
		t.Fatalf("unexpected unwrapped payload: %x", unwrapped)
	}
}

func TestSegmentBinarySMS(t *testing.T) {
	t.Parallel()

	push := bytes.Repeat([]byte{0xAB}, 300)
	segments, err := SegmentBinarySMS(push, 0x42)
	if err != nil {
		t.Fatalf("segment binary sms: %v", err)
	}
	if len(segments) != 3 {
		t.Fatalf("unexpected segment count: %d", len(segments))
	}
	for i, segment := range segments {
		if len(segment) < 12 {
			t.Fatalf("segment %d too short", i)
		}
		if segment[0] != 0x0B || segment[1] != 0x05 || segment[2] != 0x04 {
			t.Fatalf("segment %d missing port addressing UDH: %x", i, segment[:3])
		}
		if segment[3] != 0x0B || segment[4] != 0x84 || segment[5] != 0x23 || segment[6] != 0xF0 {
			t.Fatalf("segment %d wrong port values: %x", i, segment[3:7])
		}
		if segment[7] != 0x00 || segment[8] != 0x03 {
			t.Fatalf("segment %d missing concatenation UDH: %x", i, segment[7:9])
		}
		if segment[9] != 0x42 {
			t.Fatalf("segment %d wrong reference: %x", i, segment[9])
		}
		if segment[10] != 0x03 || segment[11] != byte(i+1) {
			t.Fatalf("segment %d wrong concat values: %x", i, segment[10:12])
		}
	}
}
