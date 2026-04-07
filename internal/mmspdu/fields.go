package mmspdu

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

var (
	errShortData      = errors.New("mmspdu: short data")
	errInvalidInteger = errors.New("mmspdu: invalid integer encoding")
	errInvalidUintvar = errors.New("mmspdu: invalid uintvar encoding")
	errInsertAddress  = errors.New("mmspdu: insert-address-token")
)

type HeaderValue struct {
	Field byte
	Kind  FieldKind
	Raw   []byte
}

func NewTextValue(field byte, value string) HeaderValue {
	return HeaderValue{Field: field, Kind: FieldKindText, Raw: encodeTextString(value)}
}

func NewEncodedStringValue(field byte, value string) HeaderValue {
	return HeaderValue{Field: field, Kind: FieldKindEncodedString, Raw: encodeEncodedString(value)}
}

func NewTokenValue(field, value byte) HeaderValue {
	return HeaderValue{Field: field, Kind: FieldKindToken, Raw: []byte{value}}
}

func NewShortIntegerValue(field byte, value uint8) HeaderValue {
	return HeaderValue{Field: field, Kind: FieldKindShortInteger, Raw: encodeShortInteger(value)}
}

func NewLongIntegerValue(field byte, value uint64) HeaderValue {
	return HeaderValue{Field: field, Kind: FieldKindLongInteger, Raw: encodeLongInteger(value)}
}

func NewDateValue(field byte, value time.Time) HeaderValue {
	return HeaderValue{Field: field, Kind: FieldKindDate, Raw: encodeLongInteger(uint64(value.UTC().Unix()))}
}

func NewRelativeDateValue(field byte, seconds uint64) HeaderValue {
	payload := append([]byte{0x81}, encodeLongInteger(seconds)...)
	return HeaderValue{Field: field, Kind: FieldKindDate, Raw: append([]byte{byte(len(payload))}, payload...)}
}

func NewFromValue(value string) HeaderValue {
	if value == "#insert" {
		return HeaderValue{Field: FieldFrom, Kind: FieldKindAddress, Raw: []byte{0x01, 0x81}}
	}
	addr := encodeTextString(normalizeAddress(value))
	payload := append([]byte{0x80}, addr...)
	return HeaderValue{Field: FieldFrom, Kind: FieldKindAddress, Raw: append([]byte{byte(len(payload))}, payload...)}
}

func NewAddressValue(field byte, value string) HeaderValue {
	return HeaderValue{Field: field, Kind: FieldKindAddress, Raw: encodeTextString(normalizeAddress(value))}
}

func (v HeaderValue) Bytes() []byte {
	return append([]byte(nil), v.Raw...)
}

func (v HeaderValue) Text() (string, error) {
	switch v.Kind {
	case FieldKindText:
		return decodeTextString(v.Raw)
	case FieldKindAddress:
		return decodeAddressString(v.Raw)
	case FieldKindEncodedString:
		return decodeEncodedString(v.Raw)
	default:
		return "", fmt.Errorf("mmspdu: field %x is not textual", v.Field)
	}
}

func (v HeaderValue) IsInsertAddress() bool {
	return v.Kind == FieldKindAddress && isInsertAddressToken(v.Raw)
}

func (v HeaderValue) Token() (byte, error) {
	if len(v.Raw) != 1 {
		return 0, fmt.Errorf("mmspdu: token field %x has %d bytes", v.Field, len(v.Raw))
	}
	return v.Raw[0], nil
}

func (v HeaderValue) Integer() (uint64, error) {
	switch v.Kind {
	case FieldKindShortInteger:
		return decodeShortInteger(v.Raw)
	case FieldKindLongInteger, FieldKindDate:
		if len(v.Raw) >= 3 && v.Raw[0] <= 30 {
			length := int(v.Raw[0])
			if len(v.Raw) == length+1 {
				if v.Raw[1] == 0x80 || v.Raw[1] == 0x81 {
					return decodeLongInteger(v.Raw[2:])
				}
			}
		}
		return decodeLongInteger(v.Raw)
	default:
		return 0, fmt.Errorf("mmspdu: field %x is not integer-like", v.Field)
	}
}

func (v HeaderValue) Time() (time.Time, error) {
	if v.Kind == FieldKindDate && len(v.Raw) >= 3 && v.Raw[0] <= 30 {
		length := int(v.Raw[0])
		if len(v.Raw) == length+1 {
			switch v.Raw[1] {
			case 0x80:
				seconds, err := decodeLongInteger(v.Raw[2:])
				if err != nil {
					return time.Time{}, err
				}
				return time.Unix(int64(seconds), 0).UTC(), nil
			case 0x81:
				seconds, err := decodeLongInteger(v.Raw[2:])
				if err != nil {
					return time.Time{}, err
				}
				return time.Now().UTC().Add(time.Duration(seconds) * time.Second), nil
			}
		}
	}
	seconds, err := v.Integer()
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(int64(seconds), 0).UTC(), nil
}

func encodeShortInteger(value uint8) []byte {
	if value > 0x7F {
		panic("mmspdu: short integer out of range")
	}
	return []byte{0x80 | value}
}

func decodeShortInteger(data []byte) (uint64, error) {
	if len(data) != 1 {
		return 0, errInvalidInteger
	}
	if data[0]&0x80 == 0 {
		return 0, errInvalidInteger
	}
	return uint64(data[0] & 0x7F), nil
}

func encodeLongInteger(value uint64) []byte {
	if value == 0 {
		return []byte{1, 0}
	}
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], value)
	first := 0
	for first < len(raw) && raw[first] == 0 {
		first++
	}
	out := raw[first:]
	return append([]byte{byte(len(out))}, out...)
}

func decodeLongInteger(data []byte) (uint64, error) {
	if len(data) == 0 {
		return 0, errShortData
	}
	length := int(data[0])
	if length == 0 || length > 8 || len(data) != length+1 {
		return 0, errInvalidInteger
	}
	var padded [8]byte
	copy(padded[8-length:], data[1:])
	return binary.BigEndian.Uint64(padded[:]), nil
}

func encodeUintvar(value uint64) []byte {
	if value == 0 {
		return []byte{0}
	}
	var parts [10]byte
	i := len(parts)
	for value > 0 {
		i--
		parts[i] = byte(value & 0x7F)
		value >>= 7
	}
	out := append([]byte(nil), parts[i:]...)
	for j := 0; j < len(out)-1; j++ {
		out[j] |= 0x80
	}
	return out
}

func decodeUintvar(data []byte) (uint64, int, error) {
	var value uint64
	for i, b := range data {
		if i == 5 {
			return 0, 0, errInvalidUintvar
		}
		value = (value << 7) | uint64(b&0x7F)
		if b&0x80 == 0 {
			return value, i + 1, nil
		}
	}
	return 0, 0, errShortData
}

func encodeTextString(value string) []byte {
	return append([]byte(value), 0x00)
}

func decodeTextString(data []byte) (string, error) {
	if len(data) == 0 || data[len(data)-1] != 0x00 {
		return "", errShortData
	}
	return string(data[:len(data)-1]), nil
}

func encodeQuotedString(value string) []byte {
	out := []byte{0x22}
	out = append(out, []byte(value)...)
	out = append(out, 0x00)
	return out
}

func decodeQuotedString(data []byte) (string, error) {
	if len(data) < 2 || data[0] != 0x22 || data[len(data)-1] != 0x00 {
		return "", errShortData
	}
	return string(data[1 : len(data)-1]), nil
}

func encodeEncodedString(value string) []byte {
	return encodeTextString(value)
}

func decodeEncodedString(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errShortData
	}
	if data[0] <= 30 {
		if len(data) < int(data[0])+1 {
			return "", errShortData
		}
		return decodeEncodedStringPayload(data[1 : 1+int(data[0])])
	}
	return decodeEncodedStringPayload(data)
}

func decodeAddressString(data []byte) (string, error) {
	if isInsertAddressToken(data) {
		return "", errInsertAddress
	}
	if len(data) == 0 {
		return "", errShortData
	}
	if data[0] <= 30 {
		if len(data) < int(data[0])+1 {
			return "", errShortData
		}
		payload := data[1 : 1+int(data[0])]
		if isInsertAddressToken(payload) {
			return "", errInsertAddress
		}
		return decodeEncodedStringPayload(payload)
	}
	return decodeEncodedStringPayload(data)
}

func isInsertAddressToken(data []byte) bool {
	return len(data) == 2 && data[0] == 0x01 && data[1] == 0x81
}

func decodeEncodedStringPayload(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errShortData
	}
	if data[0]&0x80 != 0 {
		if len(data) < 2 {
			return "", errShortData
		}
		return decodeTextString(data[1:])
	}
	if text, err := decodeTextString(data); err == nil {
		return text, nil
	}
	if idx := bytes.IndexByte(data, 0x00); idx >= 0 {
		if idx+1 >= len(data) {
			return "", errShortData
		}
		return decodeTextString(data[idx+1:])
	}
	return decodeTextString(data)
}

func normalizeAddress(value string) string {
	if strings.Contains(strings.ToUpper(value), "/TYPE=") {
		return value
	}
	if strings.Contains(value, "@") {
		return value + "/TYPE=EMAIL"
	}
	if !strings.HasPrefix(value, "+") {
		value = "+" + value
	}
	return value + "/TYPE=PLMN"
}

type byteReader struct {
	data []byte
	pos  int
}

func newByteReader(data []byte) *byteReader {
	return &byteReader{data: data}
}

func (r *byteReader) Remaining() int {
	return len(r.data) - r.pos
}

func (r *byteReader) ReadByte() (byte, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	b := r.data[r.pos]
	r.pos++
	return b, nil
}

func (r *byteReader) ReadN(n int) ([]byte, error) {
	if n < 0 || r.pos+n > len(r.data) {
		return nil, io.EOF
	}
	out := r.data[r.pos : r.pos+n]
	r.pos += n
	return out, nil
}

func (r *byteReader) ReadUntilNull() ([]byte, error) {
	return r.ReadUntilNullFrom()
}

func (r *byteReader) ReadUntilNullFrom(prefix ...byte) ([]byte, error) {
	idx := bytes.IndexByte(r.data[r.pos:], 0x00)
	if idx < 0 {
		return nil, io.EOF
	}
	out := append([]byte(nil), prefix...)
	out = append(out, r.data[r.pos:r.pos+idx+1]...)
	r.pos += idx + 1
	return out, nil
}
