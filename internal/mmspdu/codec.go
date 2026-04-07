package mmspdu

import (
	"fmt"
	"sort"
)

type PDU struct {
	MessageType   uint8
	TransactionID string
	MMSVersion    string
	Headers       map[byte]HeaderValue
	HeaderOrder   []byte
	Body          *MultipartBody
}

type MultipartBody struct {
	Parts []Part
}

type Part struct {
	Headers           map[string]string
	ContentType       string
	ContentTypeParams map[string]string
	Data              []byte
}

func Decode(data []byte) (*PDU, error) {
	headers, headerOrder, body, err := decodePDUSections(data)
	if err != nil {
		return nil, err
	}

	pdu := &PDU{Headers: headers, HeaderOrder: headerOrder, Body: body}
	if value, ok := headers[FieldMessageType]; ok {
		token, err := value.Token()
		if err != nil {
			return nil, fmt.Errorf("decode message type: %w", err)
		}
		pdu.MessageType = token
	}
	if value, ok := headers[FieldTransactionID]; ok {
		text, err := value.Text()
		if err != nil {
			return nil, fmt.Errorf("decode transaction id: %w", err)
		}
		pdu.TransactionID = text
	}
	if value, ok := headers[FieldMMSVersion]; ok {
		version, err := value.Integer()
		if err != nil {
			return nil, fmt.Errorf("decode mms version: %w", err)
		}
		pdu.MMSVersion = decodeVersionByte(byte(version))
	}
	return pdu, nil
}

func Encode(pdu *PDU) ([]byte, error) {
	if pdu == nil {
		return nil, fmt.Errorf("mmspdu: nil pdu")
	}
	headers := make(map[byte]HeaderValue, len(pdu.Headers)+3)
	for k, v := range pdu.Headers {
		headers[k] = v
	}
	if pdu.MessageType != 0 {
		headers[FieldMessageType] = NewTokenValue(FieldMessageType, pdu.MessageType)
	}
	if pdu.TransactionID != "" {
		headers[FieldTransactionID] = NewTextValue(FieldTransactionID, pdu.TransactionID)
	}
	if pdu.MMSVersion != "" {
		headers[FieldMMSVersion] = NewShortIntegerValue(FieldMMSVersion, encodeVersionString(pdu.MMSVersion))
	}

	if pdu.Body == nil {
		return encodeOrderedHeaders(headers, pdu.HeaderOrder)
	}

	if _, ok := headers[FieldContentType]; !ok {
		headers[FieldContentType] = NewContentTypeValue("multipart/related", nil)
	}
	return encodePDUWithBody(headers, pdu.HeaderOrder, pdu.Body)
}

func encodeVersionString(value string) byte {
	switch value {
	case "1.0":
		return 0x10
	case "1.1":
		return 0x11
	case "1.2":
		return 0x12
	case "1.3":
		return 0x13
	default:
		return 0x13
	}
}

func decodeVersionByte(value byte) string {
	switch value {
	case 0x10:
		return "1.0"
	case 0x11:
		return "1.1"
	case 0x12:
		return "1.2"
	case 0x13:
		return "1.3"
	default:
		return ""
	}
}

func encodePDUWithBody(headers map[byte]HeaderValue, headerOrder []byte, body *MultipartBody) ([]byte, error) {
	bodyData, err := EncodeMultipart(body)
	if err != nil {
		return nil, err
	}

	ctValue, ok := headers[FieldContentType]
	if !ok {
		return nil, fmt.Errorf("mmspdu: body present without content type")
	}

	headerCopy := make(map[byte]HeaderValue, len(headers)-1)
	for field, value := range headers {
		if field == FieldContentType {
			continue
		}
		headerCopy[field] = value
	}

	prefix, err := encodeOrderedHeaders(headerCopy, headerOrder)
	if err != nil {
		return nil, err
	}
	out := append([]byte(nil), prefix...)
	out = append(out, encodeHeaderName(FieldContentType)...)
	out = append(out, ctValue.Raw...)
	out = append(out, bodyData...)
	return out, nil
}

func encodeOrderedHeaders(headers map[byte]HeaderValue, originalOrder []byte) ([]byte, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	if len(originalOrder) != 0 {
		return encodeHeadersBySequence(headers, originalOrder)
	}
	order := canonicalHeaderOrder(headers)
	return encodeHeadersBySequence(headers, order)
}

func canonicalHeaderOrder(headers map[byte]HeaderValue) []byte {
	order := make([]byte, 0, len(headers))
	seen := make(map[byte]struct{}, len(headers))
	for _, field := range []byte{FieldMessageType, FieldTransactionID, FieldMMSVersion} {
		if _, ok := headers[field]; ok {
			order = append(order, field)
			seen[field] = struct{}{}
		}
	}
	for field := range headers {
		if _, ok := seen[field]; ok {
			continue
		}
		order = append(order, field)
	}
	if len(order) > len(seen) {
		sort.Slice(order[len(seen):], func(i, j int) bool {
			return order[len(seen)+i] < order[len(seen)+j]
		})
	}
	return order
}

func encodeHeadersBySequence(headers map[byte]HeaderValue, order []byte) ([]byte, error) {
	var out []byte
	seen := make(map[byte]struct{}, len(headers))
	for _, field := range order {
		value, ok := headers[field]
		if !ok {
			continue
		}
		if value.Field == 0 {
			value.Field = field
		}
		if len(value.Raw) == 0 {
			return nil, fmt.Errorf("mmspdu: header %x has empty value", field)
		}
		out = append(out, encodeHeaderName(field)...)
		out = append(out, value.Raw...)
		seen[field] = struct{}{}
	}
	if len(seen) == len(headers) {
		return out, nil
	}

	remaining := make([]int, 0, len(headers)-len(seen))
	for field := range headers {
		if _, ok := seen[field]; ok {
			continue
		}
		remaining = append(remaining, int(field))
	}
	sort.Ints(remaining)
	for _, key := range remaining {
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

func decodePDUSections(data []byte) (map[byte]HeaderValue, []byte, *MultipartBody, error) {
	reader := newByteReader(data)
	headers := make(map[byte]HeaderValue)
	order := make([]byte, 0, 8)

	for reader.Remaining() > 0 {
		field, err := decodeHeaderName(reader)
		if err != nil {
			return nil, nil, nil, err
		}
		kind := kindForField(field)
		raw, err := decodeHeaderValue(reader, field, kind)
		if err != nil {
			return nil, nil, nil, err
		}
		headers[field] = HeaderValue{Field: field, Kind: kind, Raw: raw}
		order = append(order, field)

		if field == FieldContentType && reader.Remaining() > 0 {
			body, err := DecodeMultipart(reader.data[reader.pos:])
			if err == nil {
				return headers, order, body, nil
			}
			return nil, nil, nil, fmt.Errorf("decode multipart body: %w", err)
		}
	}
	return headers, order, nil, nil
}
