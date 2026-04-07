package wappush

import "fmt"

const (
	wspTransactionID = 0x01
	wspPushPDU       = 0x06

	// WSP well-known header tokens (bit 7 set).
	wspHeaderPushFlag = 0xB4 // Push-Flag
	wspHeaderAppID    = 0xAF // X-Wap-Application-ID
	wspHeaderContLen  = 0x8D // Content-Length

	// WSP header values.
	wspPushFlagTrusted  = 0x82 // content trusted
	wspHeaderAppIDMMSUA = 0x84 // x-wap-application:mms.ua

)

// WrapMMSPDU encodes an MMS PDU inside a WSP Push PDU suitable for delivery
// over WAP Push SMS. The format matches OMA MMS 1.3 / WAP-230-WSP:
//
//	Transaction-ID | PDU-Type | Headers-Length | Content-Type | Push-Flag | App-ID | Content-Length | MMS-PDU
//
// Headers-Length and Content-Length are calculated from the actual payload
// size so the UE can correctly locate the MMS PDU within the push frame.
func WrapMMSPDU(mmsPDU []byte) []byte {
	// Content-Type: well-known token 0xBE = application/vnd.wap.mms-message.
	// UE WAP push dispatchers (iOS, Android, carrier stacks) only recognise the
	// well-known short-integer token; verbose length-quote + text form is not
	// handled by real devices and causes the push to be silently dropped.
	headers := make([]byte, 0, 1+2+2+4)
	headers = append(headers, 0xBE)
	// Push-Flag: content trusted.
	headers = append(headers, wspHeaderPushFlag, wspPushFlagTrusted)
	// X-Wap-Application-ID: x-wap-application:mms.ua.
	headers = append(headers, wspHeaderAppID, wspHeaderAppIDMMSUA)
	// Content-Length: actual byte length of the MMS PDU.
	headers = append(headers, wspHeaderContLen)
	headers = append(headers, wspEncodeInteger(len(mmsPDU))...)

	out := make([]byte, 0, 3+len(headers)+len(mmsPDU))
	out = append(out, wspTransactionID, wspPushPDU, byte(len(headers)))
	out = append(out, headers...)
	out = append(out, mmsPDU...)
	return out
}

// wspEncodeInteger encodes a non-negative integer as a WSP Integer-value.
// Values 0 - 127 use a short-integer (single byte, bit 7 set).
// Larger values use a long-integer (1-byte length prefix + big-endian bytes).
func wspEncodeInteger(v int) []byte {
	if v <= 127 {
		return []byte{byte(v) | 0x80}
	}
	switch {
	case v <= 0xFF:
		return []byte{0x01, byte(v)}
	case v <= 0xFFFF:
		return []byte{0x02, byte(v >> 8), byte(v)}
	case v <= 0xFFFFFF:
		return []byte{0x03, byte(v >> 16), byte(v >> 8), byte(v)}
	default:
		return []byte{0x04, byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
	}
}

// UnwrapMMSPDU extracts the MMS PDU from a WSP Push PDU.
// It reads the Headers-Length from byte[2] and skips past the header block
// to find the MMS PDU, without requiring specific header bytes at fixed offsets.
func UnwrapMMSPDU(push []byte) ([]byte, error) {
	if len(push) < 3 {
		return nil, fmt.Errorf("wappush: short push payload")
	}
	if push[1] != wspPushPDU {
		return nil, fmt.Errorf("wappush: unexpected pdu type %x", push[1])
	}
	headersLen := int(push[2])
	mmsOffset := 3 + headersLen
	if len(push) < mmsOffset {
		return nil, fmt.Errorf("wappush: push payload truncated (headers_len=%d, buf=%d)", headersLen, len(push))
	}
	return append([]byte(nil), push[mmsOffset:]...), nil
}
