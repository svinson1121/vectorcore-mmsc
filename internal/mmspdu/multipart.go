package mmspdu

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

func EncodeMultipart(body *MultipartBody) ([]byte, error) {
	if body == nil {
		return nil, nil
	}

	var out []byte
	out = append(out, encodeUintvar(uint64(len(body.Parts)))...)
	for _, part := range body.Parts {
		headerBlock, err := encodePartHeaders(part)
		if err != nil {
			return nil, err
		}
		out = append(out, encodeUintvar(uint64(len(headerBlock)))...)
		out = append(out, encodeUintvar(uint64(len(part.Data)))...)
		out = append(out, headerBlock...)
		out = append(out, part.Data...)
	}
	return out, nil
}

func DecodeMultipart(data []byte) (*MultipartBody, error) {
	if len(data) == 0 {
		return &MultipartBody{}, nil
	}
	reader := newByteReader(data)
	count, consumed, err := decodeUintvar(data)
	if err != nil {
		return nil, err
	}
	reader.pos += consumed

	body := &MultipartBody{Parts: make([]Part, 0, count)}
	for i := uint64(0); i < count; i++ {
		headerLen, err := readUintvarFromReader(reader)
		if err != nil {
			return nil, err
		}
		dataLen, err := readUintvarFromReader(reader)
		if err != nil {
			return nil, err
		}

		headerBlock, err := reader.ReadN(int(headerLen))
		if err != nil {
			return nil, err
		}
		payload, err := reader.ReadN(int(dataLen))
		if err != nil {
			return nil, err
		}

		part, err := decodePartHeaders(headerBlock)
		if err != nil {
			return nil, err
		}
		part.Data = append([]byte(nil), payload...)
		body.Parts = append(body.Parts, part)
	}
	return body, nil
}

func encodePartHeaders(part Part) ([]byte, error) {
	var out []byte
	ct := encodeContentTypeValue(ContentTypeValue{
		MediaType: part.ContentType,
		Params:    part.ContentTypeParams,
	})
	out = append(out, ct...)

	normalizedHeaders := normalizeHeaderMap(part.Headers)
	keys := make([]string, 0, len(normalizedHeaders))
	for key := range normalizedHeaders {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		val := normalizedHeaders[key]
		switch key {
		case "content-id":
			// Encode as well-known token 0xC0 + quoted-string value so UEs can
			// match it against the multipart/related "start" parameter.
			out = append(out, 0xC0)
			out = append(out, encodeQuotedString(ensureAngleBrackets(val))...)
		case "content-location":
			// Encode as well-known token 0x8E + text string.
			out = append(out, 0x8E)
			out = append(out, encodeTextString(val)...)
		default:
			out = append(out, encodeTextString(key)...)
			out = append(out, encodeTextString(val)...)
		}
	}
	return out, nil
}

// ensureAngleBrackets wraps a content-id value in angle brackets if not
// already present. RFC 2045 requires Content-IDs to be formatted as
// msg-id = "<" id-left "@" id-right ">", but many implementations omit the
// brackets — iOS requires them for start-parameter matching.
func ensureAngleBrackets(cid string) string {
	if len(cid) >= 2 && cid[0] == '<' && cid[len(cid)-1] == '>' {
		return cid
	}
	return "<" + cid + ">"
}

func decodePartHeaders(data []byte) (Part, error) {
	if len(data) == 0 {
		return Part{}, fmt.Errorf("mmspdu: missing part headers")
	}

	ctValue, consumed, err := decodePartContentTypeValue(data)
	if err != nil {
		return Part{}, err
	}

	reader := newByteReader(data[consumed:])
	headers := make(map[string]string)
	for reader.Remaining() > 0 {
		if reader.data[reader.pos]&0x80 != 0 {
			key, value, err := decodeWellKnownPartHeader(reader)
			if err != nil {
				return Part{}, err
			}
			if key != "" {
				headers[key] = value
			}
			continue
		}

		keyRaw, err := reader.ReadUntilNull()
		if err != nil {
			return Part{}, err
		}
		valueRaw, err := reader.ReadUntilNull()
		if err != nil {
			return Part{}, err
		}
		key, err := decodeTextString(keyRaw)
		if err != nil {
			return Part{}, err
		}
		value, err := decodeTextString(valueRaw)
		if err != nil {
			return Part{}, err
		}
		headers[strings.ToLower(key)] = value
	}

	return Part{
		Headers:           headers,
		ContentType:       ctValue.MediaType,
		ContentTypeParams: ctValue.Params,
	}, nil
}

func decodePartContentTypeValue(data []byte) (ContentTypeValue, int, error) {
	if len(data) == 0 {
		return ContentTypeValue{}, 0, errShortData
	}
	if isTextStringStart(data[0]) {
		reader := newByteReader(data)
		mediaRaw, err := reader.ReadUntilNull()
		if err != nil {
			return ContentTypeValue{}, 0, err
		}
		mediaType, err := decodeTextString(mediaRaw)
		if err != nil {
			return ContentTypeValue{}, 0, err
		}
		return ContentTypeValue{MediaType: mediaType}, reader.pos, nil
	}

	// Well-known short-integer token (bit 7 set): single byte, no length prefix.
	if data[0]&0x80 != 0 {
		name, err := decodeWellKnownMediaType(data[0])
		if err != nil {
			return ContentTypeValue{}, 0, err
		}
		return ContentTypeValue{MediaType: name}, 1, nil
	}

	length, lengthBytes, err := decodeValueLength(data)
	if err != nil {
		return ContentTypeValue{}, 0, err
	}
	if len(data) < lengthBytes+length {
		return ContentTypeValue{}, 0, errShortData
	}

	value, err := decodePartContentTypePayload(data[lengthBytes : lengthBytes+length])
	if err != nil {
		return ContentTypeValue{}, 0, err
	}
	return value, lengthBytes + length, nil
}

func decodePartContentTypePayload(data []byte) (ContentTypeValue, error) {
	reader := newByteReader(data)
	mediaType, err := readPartMediaType(reader)
	if err != nil {
		return ContentTypeValue{}, err
	}

	params := make(map[string]string)
	for reader.Remaining() > 0 {
		keyRaw, err := readPartParameterName(reader)
		if err != nil {
			return ContentTypeValue{}, err
		}
		valueRaw, err := readPartHeaderValue(reader)
		if err != nil {
			return ContentTypeValue{}, err
		}
		if keyRaw == "" {
			continue
		}
		value, err := decodePartHeaderTextValue(valueRaw)
		if err != nil {
			return ContentTypeValue{}, err
		}
		params[keyRaw] = value
	}

	return ContentTypeValue{
		MediaType: mediaType,
		Params:    params,
	}, nil
}

func readPartMediaType(reader *byteReader) (string, error) {
	if reader.Remaining() == 0 {
		return "", errShortData
	}
	b := reader.data[reader.pos]
	if b&0x80 != 0 {
		reader.pos++
		return decodeWellKnownMediaType(b)
	}
	raw, err := reader.ReadUntilNull()
	if err != nil {
		return "", err
	}
	return decodeTextString(raw)
}

func readPartParameterName(reader *byteReader) (string, error) {
	if reader.Remaining() == 0 {
		return "", errShortData
	}
	b, err := reader.ReadByte()
	if err != nil {
		return "", err
	}
	if b&0x80 != 0 {
		return decodeWellKnownContentTypeParam(b), nil
	}
	raw, err := reader.ReadUntilNullFrom(b)
	if err != nil {
		return "", err
	}
	value, err := decodeTextString(raw)
	if err != nil {
		return "", err
	}
	return strings.ToLower(value), nil
}

func decodeWellKnownPartHeader(reader *byteReader) (string, string, error) {
	name, err := reader.ReadByte()
	if err != nil {
		return "", "", err
	}
	valueRaw, err := readPartHeaderValue(reader)
	if err != nil {
		return "", "", err
	}

	switch name {
	case 0x8e:
		value, err := decodePartHeaderTextValue(valueRaw)
		if err != nil {
			return "", "", err
		}
		return "content-location", value, nil
	case 0xc0:
		value, err := decodePartHeaderTextValue(valueRaw)
		if err != nil {
			return "", "", err
		}
		return "content-id", value, nil
	default:
		return "", "", nil
	}
}

func readPartHeaderValue(reader *byteReader) ([]byte, error) {
	if reader.Remaining() == 0 {
		return nil, errShortData
	}
	b := reader.data[reader.pos]
	switch {
	case b <= 31:
		length, consumed, err := decodeValueLength(reader.data[reader.pos:])
		if err != nil {
			return nil, err
		}
		reader.pos += consumed
		return reader.ReadN(length)
	case b == 0x22:
		return reader.ReadUntilNull()
	case isTextStringStart(b):
		return reader.ReadUntilNull()
	default:
		return reader.ReadN(1)
	}
}

func decodePartHeaderTextValue(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errShortData
	}
	if text, err := decodeQuotedString(data); err == nil {
		return text, nil
	}
	if text, err := decodeTextString(data); err == nil {
		return text, nil
	}
	if text, err := decodeEncodedStringPayload(data); err == nil {
		return text, nil
	}
	if data[0]&0x80 != 0 {
		return decodeWellKnownMediaType(data[0])
	}
	return "", errShortData
}

func decodeWellKnownMediaType(token byte) (string, error) {
	if name, ok := wellKnownMediaTypesByToken[token]; ok {
		return name, nil
	}
	return "", fmt.Errorf("mmspdu: unsupported media type token %x", token)
}

func decodeWellKnownContentTypeParam(token byte) string {
	return wellKnownContentTypeParamsByToken[token]
}

func isTextStringStart(b byte) bool {
	return b >= 0x20 && b < 0x80
}

func readUintvarFromReader(reader *byteReader) (uint64, error) {
	if reader.Remaining() == 0 {
		return 0, errShortData
	}
	value, consumed, err := decodeUintvar(reader.data[reader.pos:])
	if err != nil {
		return 0, err
	}
	reader.pos += consumed
	return value, nil
}

func normalizeHeaderMap(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		out[strings.ToLower(k)] = v
	}
	return out
}

func equalMultipartBodies(a, b *MultipartBody) bool {
	return bytes.Equal(mustEncodeMultipart(a), mustEncodeMultipart(b))
}

func mustEncodeMultipart(body *MultipartBody) []byte {
	data, _ := EncodeMultipart(body)
	return data
}
