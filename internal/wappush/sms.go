package wappush

import "fmt"

const (
	destPortWAPPush = 2948
	sourcePortWSP   = 9200
	// maxSMSPayload is the maximum bytes of WSP payload per SMS segment.
	// 8-bit binary SMS is limited to 140 bytes over-the-air; the UDH
	// (12 bytes: 1 length byte + 11 bytes content) consumes 12 of those,
	// leaving 128 bytes available for WSP data. Using more than 128 causes
	// segments to exceed the OTA limit: the SMSC reports delivery but the
	// UE receives a truncated/corrupt WAP Push and silently discards it.
	maxSMSPayload   = 128
)

func SegmentBinarySMS(push []byte, reference byte) ([][]byte, error) {
	if len(push) == 0 {
		return nil, fmt.Errorf("wappush: empty push payload")
	}

	total := (len(push) + maxSMSPayload - 1) / maxSMSPayload
	if total > 255 {
		return nil, fmt.Errorf("wappush: too many segments")
	}

	segments := make([][]byte, 0, total)
	for i := 0; i < total; i++ {
		start := i * maxSMSPayload
		end := start + maxSMSPayload
		if end > len(push) {
			end = len(push)
		}
		udh := buildUDH(reference, byte(total), byte(i+1))
		segment := append([]byte(nil), udh...)
		segment = append(segment, push[start:end]...)
		segments = append(segments, segment)
	}
	return segments, nil
}

func buildUDH(reference, total, sequence byte) []byte {
	destPort := uint16(destPortWAPPush)
	sourcePort := uint16(sourcePortWSP)
	return []byte{
		0x0B,
		0x05, 0x04, byte(destPort >> 8), byte(destPort), byte(sourcePort >> 8), byte(sourcePort),
		0x00, 0x03, reference, total, sequence,
	}
}
