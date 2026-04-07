package mmspdu

import (
	"fmt"
	"sort"
	"strings"
)

type ContentTypeValue struct {
	MediaType string
	Params    map[string]string
}

// wellKnownMediaTypesByToken maps WAP binary well-known short-integer media
// type tokens (bit 7 set) to their MIME type string equivalents.
// wellKnownMediaTypesByToken maps WAP binary well-known short-integer tokens
// to MIME type strings. Token = 0x80 | assigned-number per WAP-WINA.
// Key assignments: multipart/mixed=0x0C→0x8C, multipart/related=0x33→0xB3,
// application/smil=0x38→0xB8. Note: 0x8E=multipart/byteranges (0x0E), NOT mixed.
var wellKnownMediaTypesByToken = map[byte]string{
	0x80: "*/*",
	0x81: "text/*",
	0x82: "text/html",
	0x83: "text/plain",
	0x84: "text/x-hdml",
	0x8C: "multipart/mixed",
	0x8E: "multipart/byteranges",
	0x9D: "image/gif",
	0x9E: "image/jpeg",
	0xA3: "application/vnd.wap.multipart.mixed",
	0xA9: "image/png",
	0xB3: "multipart/related",
	0xB4: "application/vnd.wap.multipart.related",
	0xB8: "application/smil",
}

// wellKnownMediaTypeTokens is the reverse of wellKnownMediaTypesByToken,
// mapping lowercase MIME type string to WAP short-integer token.
var wellKnownMediaTypeTokens = map[string]byte{
	"*/*":                                   0x80,
	"text/*":                                0x81,
	"text/html":                             0x82,
	"text/plain":                            0x83,
	"text/x-hdml":                           0x84,
	"multipart/mixed":                       0x8C,
	"multipart/byteranges":                  0x8E,
	"image/gif":                             0x9D,
	"image/jpeg":                            0x9E,
	"application/vnd.wap.multipart.mixed":   0xA3,
	"image/png":                             0xA9,
	"multipart/related":                     0xB3,
	"application/vnd.wap.multipart.related": 0xB4,
	"application/smil":                      0xB8,
}

// wellKnownContentTypeParamsByToken maps WAP binary parameter tokens (bit 7
// set) to parameter name strings.
var wellKnownContentTypeParamsByToken = map[byte]string{
	0x81: "charset",
	0x85: "name",
	0x89: "type",
	0x8A: "start",
}

// wellKnownContentTypeParamTokens is the reverse of wellKnownContentTypeParamsByToken.
var wellKnownContentTypeParamTokens = map[string]byte{
	"charset": 0x81,
	"name":    0x85,
	"type":    0x89,
	"start":   0x8A,
}

func NewContentTypeValue(mediaType string, params map[string]string) HeaderValue {
	return HeaderValue{
		Field: FieldContentType,
		Kind:  FieldKindOctets,
		Raw:   encodeContentTypeValue(ContentTypeValue{MediaType: mediaType, Params: params}),
	}
}

func (v HeaderValue) ContentType() (ContentTypeValue, error) {
	if v.Field != FieldContentType {
		return ContentTypeValue{}, fmt.Errorf("mmspdu: field %x is not Content-Type", v.Field)
	}
	return decodeContentTypeValue(v.Raw)
}

// encodeContentTypeValue encodes a WAP binary content-type value.
//
// Without parameters: uses Constrained-media form — a single well-known token
// byte (bit 7 set) or a null-terminated text string with no length prefix.
// This is the most compact and universally compatible form.
//
// With parameters: uses Content-general-form (Value-length + Media-type +
// params), using well-known tokens for known media types and param names.
// Payloads ≤30 bytes use the short-length prefix; longer payloads use
// Length-quote (0x1F + uintvar).
func encodeContentTypeValue(value ContentTypeValue) []byte {
	mediaType := strings.ToLower(strings.TrimSpace(value.MediaType))
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}

	// No parameters: Constrained-media form (no length wrapper).
	if len(value.Params) == 0 {
		if token, ok := wellKnownMediaTypeTokens[mediaType]; ok {
			return []byte{token}
		}
		return encodeTextString(value.MediaType)
	}

	// With parameters: Content-general-form.
	// Media type: well-known token if available, else text string.
	var payload []byte
	if token, ok := wellKnownMediaTypeTokens[mediaType]; ok {
		payload = append(payload, token)
	} else {
		payload = append(payload, encodeTextString(value.MediaType)...)
	}

	keys := make([]string, 0, len(value.Params))
	for k := range value.Params {
		keys = append(keys, strings.ToLower(k))
	}
	sort.Strings(keys)
	for _, key := range keys {
		val := value.Params[key]
		// Parameter name: well-known token if available, else text string.
		if token, ok := wellKnownContentTypeParamTokens[key]; ok {
			payload = append(payload, token)
		} else {
			payload = append(payload, encodeTextString(key)...)
		}
		// Parameter value: for "type" use media-type token if available,
		// else text string.
		if key == "type" {
			if token, ok := wellKnownMediaTypeTokens[strings.ToLower(val)]; ok {
				payload = append(payload, token)
				continue
			}
		}
		payload = append(payload, encodeTextString(val)...)
	}

	if len(payload) <= 30 {
		return append([]byte{byte(len(payload))}, payload...)
	}
	// Length-quote (0x1F) followed by Uintvar-integer for lengths > 30.
	out := []byte{0x1F}
	out = append(out, encodeUintvar(uint64(len(payload)))...)
	return append(out, payload...)
}

// decodeContentTypeValue decodes a WAP binary content-type value.
// Handles three forms:
//
//   - Single well-known short-integer token (bit 7 set): one byte.
//   - Short-length prefix (0x00–0x1E): length byte + payload.
//   - Length-quote (0x1F) + Uintvar: standard WAP form for long payloads.
//
// Within the length-form payload both well-known media-type tokens and
// null-terminated text strings are recognised for the media type, and both
// well-known and text parameter names are recognised.
func decodeContentTypeValue(data []byte) (ContentTypeValue, error) {
	if len(data) == 0 {
		return ContentTypeValue{}, errShortData
	}

	// Constrained-media: single well-known short-integer token (bit 7 set).
	if data[0]&0x80 != 0 {
		if name, ok := wellKnownMediaTypesByToken[data[0]]; ok {
			return ContentTypeValue{MediaType: name}, nil
		}
		return ContentTypeValue{}, fmt.Errorf("mmspdu: unknown content-type token 0x%02x", data[0])
	}

	length, consumed, err := decodeValueLength(data)
	if err != nil {
		return ContentTypeValue{}, err
	}
	if len(data) < consumed+length {
		return ContentTypeValue{}, errShortData
	}

	payload := data[consumed : consumed+length]
	reader := newByteReader(payload)

	// Media type: well-known token or null-terminated text string.
	mediaType, err := readPayloadMediaType(reader)
	if err != nil {
		return ContentTypeValue{}, err
	}

	// Parameters: well-known token or text-string name, text-string value.
	params := make(map[string]string)
	for reader.Remaining() > 0 {
		var paramName string
		if reader.data[reader.pos]&0x80 != 0 {
			b, _ := reader.ReadByte()
			paramName = wellKnownContentTypeParamsByToken[b]
			if paramName == "" {
				// Unknown token: skip the value and continue.
				if err := skipTextValue(reader); err != nil {
					return ContentTypeValue{}, err
				}
				continue
			}
		} else {
			keyRaw, err := reader.ReadUntilNull()
			if err != nil {
				return ContentTypeValue{}, err
			}
			paramName, err = decodeTextString(keyRaw)
			if err != nil {
				return ContentTypeValue{}, err
			}
			paramName = strings.ToLower(paramName)
		}
		// Parameter value: may be a well-known token (bit 7 set, e.g. media
		// type token for "type" param) or a null-terminated text string.
		var paramValue string
		if reader.Remaining() > 0 && reader.data[reader.pos]&0x80 != 0 {
			b, _ := reader.ReadByte()
			if name, ok := wellKnownMediaTypesByToken[b]; ok {
				paramValue = name
			} else {
				// Unknown token — skip and omit the parameter.
				continue
			}
		} else {
			valueRaw, err := reader.ReadUntilNull()
			if err != nil {
				return ContentTypeValue{}, err
			}
			paramValue, err = decodeTextString(valueRaw)
			if err != nil {
				return ContentTypeValue{}, err
			}
		}
		params[paramName] = paramValue
	}

	return ContentTypeValue{MediaType: mediaType, Params: params}, nil
}

// readPayloadMediaType reads the media type from within a Content-General-Form
// payload, handling both well-known token (bit 7 set) and text-string forms.
func readPayloadMediaType(reader *byteReader) (string, error) {
	if reader.Remaining() == 0 {
		return "", errShortData
	}
	if reader.data[reader.pos]&0x80 != 0 {
		b, _ := reader.ReadByte()
		if name, ok := wellKnownMediaTypesByToken[b]; ok {
			return name, nil
		}
		return "", fmt.Errorf("mmspdu: unknown content-type token 0x%02x", b)
	}
	raw, err := reader.ReadUntilNull()
	if err != nil {
		return "", err
	}
	return decodeTextString(raw)
}

// skipTextValue advances reader past a null-terminated text value.
func skipTextValue(reader *byteReader) error {
	_, err := reader.ReadUntilNull()
	return err
}

func decodeValueLength(data []byte) (length int, consumed int, err error) {
	if len(data) == 0 {
		return 0, 0, errShortData
	}
	if data[0] <= 30 {
		return int(data[0]), 1, nil
	}
	if data[0] == 0x1F {
		// Length-quote: 0x1F followed by a Uintvar-integer.
		if len(data) < 2 {
			return 0, 0, errShortData
		}
		value, n, err := decodeUintvar(data[1:])
		if err != nil {
			return 0, 0, err
		}
		return int(value), 1 + n, nil
	}
	return 0, 0, fmt.Errorf("mmspdu: unexpected value-length byte 0x%02x", data[0])
}
