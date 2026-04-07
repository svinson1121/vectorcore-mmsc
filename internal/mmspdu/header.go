package mmspdu

import (
	"errors"
	"fmt"
	"sort"
)

func EncodeHeaders(headers map[byte]HeaderValue) ([]byte, error) {
	if len(headers) == 0 {
		return nil, nil
	}

	keys := make([]int, 0, len(headers))
	for field := range headers {
		keys = append(keys, int(field))
	}
	sort.Ints(keys)

	var out []byte
	for _, key := range keys {
		field := byte(key)
		value := headers[field]
		if value.Field == 0 {
			value.Field = field
		}
		if len(value.Raw) == 0 {
			return nil, fmt.Errorf("mmspdu: header %x has empty value", field)
		}
		out = append(out, encodeHeaderName(field)...)
		out = append(out, value.Raw...)
	}
	return out, nil
}

func DecodeHeaders(data []byte) (map[byte]HeaderValue, error) {
	reader := newByteReader(data)
	headers := make(map[byte]HeaderValue)
	for reader.Remaining() > 0 {
		field, err := decodeHeaderName(reader)
		if err != nil {
			return nil, err
		}
		kind := kindForField(field)
		raw, err := decodeHeaderValue(reader, field, kind)
		if err != nil {
			return nil, err
		}
		headers[field] = HeaderValue{
			Field: field,
			Kind:  kind,
			Raw:   raw,
		}
	}
	return headers, nil
}

func encodeHeaderName(field byte) []byte {
	if field <= 0x7F {
		return []byte{0x80 | field}
	}
	return encodeTextString(string([]byte{field}))
}

func decodeHeaderName(r *byteReader) (byte, error) {
	b, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	if b&0x80 != 0 {
		return b & 0x7F, nil
	}
	return 0, errors.New("mmspdu: text header names are not supported yet")
}

func decodeHeaderValue(r *byteReader, field byte, kind FieldKind) ([]byte, error) {
	switch kind {
	case FieldKindText, FieldKindEncodedString:
		return r.ReadUntilNull()
	case FieldKindAddress:
		first, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		if first <= 30 {
			rest, err := r.ReadN(int(first))
			if err != nil {
				return nil, err
			}
			return append([]byte{first}, rest...), nil
		}
		rest, err := r.ReadUntilNullFrom(first)
		if err != nil {
			return nil, err
		}
		return rest, nil
	case FieldKindToken, FieldKindShortInteger:
		return r.ReadN(1)
	case FieldKindLongInteger, FieldKindDate:
		first, err := r.ReadN(1)
		if err != nil {
			return nil, err
		}
		length := int(first[0])
		rest, err := r.ReadN(length)
		if err != nil {
			return nil, err
		}
		return append(first, rest...), nil
	case FieldKindOctets:
		// Content-Type and similar octet values are encoded as one of:
		//   Short-length (0x00-0x1E): one length byte followed by that many bytes.
		//   Length-quote (0x1F) + Uintvar: indicates a longer value.
		//   Well-known token (0x80-0xFF): single-byte short-integer token.
		//   Text string (0x20-0x7E): read until null — handled externally, not here.
		first, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		if first <= 30 {
			rest, err := r.ReadN(int(first))
			if err != nil {
				return nil, err
			}
			return append([]byte{first}, rest...), nil
		}
		if first == 0x1F {
			// Length-quote: uintvar follows, then that many bytes of value.
			length, n, err := decodeUintvar(r.data[r.pos:])
			if err != nil {
				return nil, err
			}
			uintvarRaw := append([]byte(nil), r.data[r.pos:r.pos+n]...)
			r.pos += n
			rest, err := r.ReadN(int(length))
			if err != nil {
				return nil, err
			}
			raw := append([]byte{0x1F}, uintvarRaw...)
			return append(raw, rest...), nil
		}
		// Well-known short-integer token (bit 7 set) or unhandled byte — return as-is.
		return []byte{first}, nil
	default:
		return nil, fmt.Errorf("mmspdu: unsupported field kind %d for field %x", kind, field)
	}
}
