package mmspdu

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestShortIntegerRoundTrip(t *testing.T) {
	t.Parallel()

	encoded := encodeShortInteger(0x13)
	if !bytes.Equal(encoded, []byte{0x93}) {
		t.Fatalf("unexpected encoded short integer: %x", encoded)
	}
	value, err := decodeShortInteger(encoded)
	if err != nil {
		t.Fatalf("decode short integer: %v", err)
	}
	if value != 0x13 {
		t.Fatalf("unexpected decoded value: %d", value)
	}
}

func TestLongIntegerRoundTrip(t *testing.T) {
	t.Parallel()

	encoded := encodeLongInteger(0x10203)
	if !bytes.Equal(encoded, []byte{0x03, 0x01, 0x02, 0x03}) {
		t.Fatalf("unexpected encoded long integer: %x", encoded)
	}
	value, err := decodeLongInteger(encoded)
	if err != nil {
		t.Fatalf("decode long integer: %v", err)
	}
	if value != 0x10203 {
		t.Fatalf("unexpected decoded value: %d", value)
	}
}

func TestUintvarRoundTrip(t *testing.T) {
	t.Parallel()

	encoded := encodeUintvar(300)
	if !bytes.Equal(encoded, []byte{0x82, 0x2c}) {
		t.Fatalf("unexpected encoded uintvar: %x", encoded)
	}
	value, consumed, err := decodeUintvar(encoded)
	if err != nil {
		t.Fatalf("decode uintvar: %v", err)
	}
	if consumed != len(encoded) || value != 300 {
		t.Fatalf("unexpected uintvar decode: value=%d consumed=%d", value, consumed)
	}
}

func TestTextAndQuotedStringRoundTrip(t *testing.T) {
	t.Parallel()

	text := encodeTextString("hello")
	decodedText, err := decodeTextString(text)
	if err != nil {
		t.Fatalf("decode text string: %v", err)
	}
	if decodedText != "hello" {
		t.Fatalf("unexpected text string: %q", decodedText)
	}

	quoted := encodeQuotedString("world")
	decodedQuoted, err := decodeQuotedString(quoted)
	if err != nil {
		t.Fatalf("decode quoted string: %v", err)
	}
	if decodedQuoted != "world" {
		t.Fatalf("unexpected quoted string: %q", decodedQuoted)
	}
}

func TestAddressNormalization(t *testing.T) {
	t.Parallel()

	if got := normalizeAddress("+12025550100"); got != "+12025550100/TYPE=PLMN" {
		t.Fatalf("unexpected normalized msisdn: %q", got)
	}
	if got := normalizeAddress("user@example.com"); got != "user@example.com/TYPE=EMAIL" {
		t.Fatalf("unexpected normalized email: %q", got)
	}
	if got := normalizeAddress("3342012832"); got != "+3342012832/TYPE=PLMN" {
		t.Fatalf("unexpected normalized msisdn without prefix: %q", got)
	}
}

func TestDecodeInsertAddressToken(t *testing.T) {
	t.Parallel()

	headers := map[byte]HeaderValue{
		FieldMessageType:   NewTokenValue(FieldMessageType, MsgTypeSendReq),
		FieldTransactionID: NewTextValue(FieldTransactionID, "txn-insert"),
		FieldMMSVersion:    NewShortIntegerValue(FieldMMSVersion, 0x12),
		FieldTo:            NewAddressValue(FieldTo, "+12025550101"),
		FieldFrom: {
			Field: FieldFrom,
			Kind:  FieldKindAddress,
			Raw:   []byte{0x01, 0x81},
		},
	}

	raw, err := EncodeHeaders(headers)
	if err != nil {
		t.Fatalf("encode headers: %v", err)
	}
	decoded, err := Decode(raw)
	if err != nil {
		t.Fatalf("decode pdu: %v", err)
	}

	value, ok := decoded.Headers[FieldFrom]
	if !ok {
		t.Fatal("expected from header")
	}
	if !value.IsInsertAddress() {
		t.Fatalf("expected insert-address token, got %#v", value.Raw)
	}
	if _, err := value.Text(); !errors.Is(err, errInsertAddress) {
		t.Fatalf("expected insert-address error, got %v", err)
	}
}

func TestDecodeLengthPrefixedAddressValue(t *testing.T) {
	t.Parallel()

	value := HeaderValue{
		Field: FieldTo,
		Kind:  FieldKindAddress,
		Raw:   []byte{0x17, 0x97, '+', '3', '3', '4', '2', '0', '1', '2', '8', '3', '4', '/', 'T', 'Y', 'P', 'E', '=', 'P', 'L', 'M', 'N', 0x00},
	}

	got, err := value.Text()
	if err != nil {
		t.Fatalf("decode address: %v", err)
	}
	if got != "+3342012834/TYPE=PLMN" {
		t.Fatalf("unexpected address: %q", got)
	}
}

func TestHeaderEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()

	ts := time.Unix(1710000000, 0).UTC()
	headers := map[byte]HeaderValue{
		FieldMessageType:     NewTokenValue(FieldMessageType, MsgTypeSendReq),
		FieldTransactionID:   NewTextValue(FieldTransactionID, "txn-1"),
		FieldMMSVersion:      NewShortIntegerValue(FieldMMSVersion, 0x13),
		FieldFrom:            NewFromValue("+12025550100"),
		FieldContentLocation: NewTextValue(FieldContentLocation, "http://mmsc.example.net/m/1"),
		FieldExpiry:          NewDateValue(FieldExpiry, ts),
	}

	encoded, err := EncodeHeaders(headers)
	if err != nil {
		t.Fatalf("encode headers: %v", err)
	}

	decoded, err := DecodeHeaders(encoded)
	if err != nil {
		t.Fatalf("decode headers: %v", err)
	}

	if got, _ := decoded[FieldMessageType].Token(); got != MsgTypeSendReq {
		t.Fatalf("unexpected message type: %x", got)
	}
	if got, _ := decoded[FieldTransactionID].Text(); got != "txn-1" {
		t.Fatalf("unexpected transaction id: %q", got)
	}
	if got, _ := decoded[FieldFrom].Text(); got != "+12025550100/TYPE=PLMN" {
		t.Fatalf("unexpected from address: %q", got)
	}
	if got, _ := decoded[FieldExpiry].Time(); !got.Equal(ts) {
		t.Fatalf("unexpected expiry: %s", got)
	}
}

func TestPDUEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()

	pdu := &PDU{
		MessageType:   MsgTypeNotificationInd,
		TransactionID: "notify-123",
		MMSVersion:    "1.3",
		Headers: map[byte]HeaderValue{
			FieldContentLocation: NewTextValue(FieldContentLocation, "http://mmsc.example.net/retrieve/123"),
			FieldFrom:            NewFromValue("+12025550100"),
		},
	}

	encoded, err := Encode(pdu)
	if err != nil {
		t.Fatalf("encode pdu: %v", err)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode pdu: %v", err)
	}

	if decoded.MessageType != MsgTypeNotificationInd {
		t.Fatalf("unexpected message type: %x", decoded.MessageType)
	}
	if decoded.TransactionID != "notify-123" {
		t.Fatalf("unexpected transaction id: %q", decoded.TransactionID)
	}
	if decoded.MMSVersion != "1.3" {
		t.Fatalf("unexpected mms version: %q", decoded.MMSVersion)
	}
	if got, _ := decoded.Headers[FieldContentLocation].Text(); got != "http://mmsc.example.net/retrieve/123" {
		t.Fatalf("unexpected content location: %q", got)
	}
}

func TestNotificationIndContentLocationIsLastHeader(t *testing.T) {
	t.Parallel()

	// OMA MMS 1.2/1.3 ENC §7.3 requires X-Mms-Content-Location to be the last
	// field in m-notification-ind. Strict UE parsers (e.g. iOS) reject the PDU
	// if mandatory fields appear after Content-Location.
	pdu := NewNotificationInd("txn-order", "http://mmsc.example.net/mms/retrieve?id=uuid%40host")
	pdu.MMSVersion = "1.2"
	pdu.Headers[FieldFrom] = NewFromValue("+12025550100")
	pdu.Headers[FieldMessageClass] = NewTokenValue(FieldMessageClass, MessageClassPersonal)
	pdu.Headers[FieldMessageSize] = NewLongIntegerValue(FieldMessageSize, 41068)
	pdu.Headers[FieldExpiry] = NewRelativeDateValue(FieldExpiry, 604800)

	encoded, err := Encode(pdu)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(decoded.HeaderOrder) == 0 {
		t.Fatal("no headers decoded")
	}
	if last := decoded.HeaderOrder[len(decoded.HeaderOrder)-1]; last != FieldContentLocation {
		t.Fatalf("X-Mms-Content-Location must be last header; got last=0x%02x, order=%v", last, decoded.HeaderOrder)
	}
}

func TestFromValueEncodesAddressPresentWrapper(t *testing.T) {
	t.Parallel()

	value := NewFromValue("+12025550100")
	want := []byte{0x18, 0x80, '+', '1', '2', '0', '2', '5', '5', '5', '0', '1', '0', '0', '/', 'T', 'Y', 'P', 'E', '=', 'P', 'L', 'M', 'N', 0x00}
	if !bytes.Equal(value.Raw, want) {
		t.Fatalf("unexpected encoded from value: %x", value.Raw)
	}
	if got, err := value.Text(); err != nil || got != "+12025550100/TYPE=PLMN" {
		t.Fatalf("unexpected decoded from value: %q err=%v", got, err)
	}
}

func TestRelativeDateValueDecodesToFutureTime(t *testing.T) {
	t.Parallel()

	value := NewRelativeDateValue(FieldExpiry, 3600)
	got, err := value.Time()
	if err != nil {
		t.Fatalf("decode relative date: %v", err)
	}
	if got.Before(time.Now().UTC().Add(59*time.Minute)) || got.After(time.Now().UTC().Add(61*time.Minute)) {
		t.Fatalf("unexpected relative expiry time: %s", got)
	}
}

func TestSendConfEncodesWithLeadingProtocolHeaders(t *testing.T) {
	t.Parallel()

	raw, err := Encode(NewSendConf("txn-send-conf", "msg-send-conf", ResponseStatusOk))
	if err != nil {
		t.Fatalf("encode send conf: %v", err)
	}
	if len(raw) < 8 {
		t.Fatalf("encoded send conf too short: %x", raw)
	}
	if got := raw[0]; got != 0x8c {
		t.Fatalf("expected first header to be message-type, got %x", got)
	}
	if got := raw[2]; got != 0x98 {
		t.Fatalf("expected second header to be transaction-id, got %x", got)
	}
	if decoded, err := Decode(raw); err != nil {
		t.Fatalf("decode send conf: %v", err)
	} else if decoded.MessageType != MsgTypeSendConf {
		t.Fatalf("unexpected decoded message type: %x", decoded.MessageType)
	}
}

func TestContentTypeValueRoundTrip(t *testing.T) {
	t.Parallel()

	raw := encodeContentTypeValue(ContentTypeValue{
		MediaType: "multipart/related",
		Params: map[string]string{
			"start": "<smil>",
			"type":  "application/smil",
		},
	})

	value, err := decodeContentTypeValue(raw)
	if err != nil {
		t.Fatalf("decode content type: %v", err)
	}
	if value.MediaType != "multipart/related" {
		t.Fatalf("unexpected media type: %q", value.MediaType)
	}
	if value.Params["start"] != "<smil>" || value.Params["type"] != "application/smil" {
		t.Fatalf("unexpected params: %#v", value.Params)
	}
}

func TestMultipartRoundTrip(t *testing.T) {
	t.Parallel()

	body := &MultipartBody{
		Parts: []Part{
			{
				ContentType: "application/smil",
				Headers: map[string]string{
					"Content-ID": "<smil>",
				},
				Data: []byte("<smil></smil>"),
			},
			{
				ContentType: "image/jpeg",
				Headers: map[string]string{
					"Content-ID":       "<img1>",
					"Content-Location": "image1.jpg",
				},
				Data: []byte{0xFF, 0xD8, 0xFF, 0xE0},
			},
		},
	}

	encoded, err := EncodeMultipart(body)
	if err != nil {
		t.Fatalf("encode multipart: %v", err)
	}
	decoded, err := DecodeMultipart(encoded)
	if err != nil {
		t.Fatalf("decode multipart: %v", err)
	}

	if len(decoded.Parts) != 2 {
		t.Fatalf("unexpected part count: %d", len(decoded.Parts))
	}
	if decoded.Parts[0].ContentType != "application/smil" {
		t.Fatalf("unexpected first part content type: %q", decoded.Parts[0].ContentType)
	}
	if decoded.Parts[0].Headers["content-id"] != "<smil>" {
		t.Fatalf("unexpected first part headers: %#v", decoded.Parts[0].Headers)
	}
	if !bytes.Equal(decoded.Parts[1].Data, []byte{0xFF, 0xD8, 0xFF, 0xE0}) {
		t.Fatalf("unexpected second part data: %x", decoded.Parts[1].Data)
	}
}

func TestDecodeMultipartAcceptsWSPStylePartHeaders(t *testing.T) {
	t.Parallel()

	firstHeaders, err := hex.DecodeString("6170706c69636174696f6e2f736d696c00c022302e736d696c00")
	if err != nil {
		t.Fatalf("decode first part headers: %v", err)
	}
	secondHeaders, err := hex.DecodeString("0f9e85494d475f303031382e4a504700ae0f8186494d475f303031382e4a504700c02230008e494d475f303031382e4a504700")
	if err != nil {
		t.Fatalf("decode second part headers: %v", err)
	}

	body := []byte{0x02, byte(len(firstHeaders)), 0x03}
	body = append(body, firstHeaders...)
	body = append(body, []byte("one")...)
	body = append(body, byte(len(secondHeaders)), 0x03)
	body = append(body, secondHeaders...)
	body = append(body, []byte("two")...)

	decoded, err := DecodeMultipart(body)
	if err != nil {
		t.Fatalf("decode multipart: %v", err)
	}
	if len(decoded.Parts) != 2 {
		t.Fatalf("unexpected part count: %d", len(decoded.Parts))
	}
	if decoded.Parts[0].ContentType != "application/smil" {
		t.Fatalf("unexpected first part content type: %q", decoded.Parts[0].ContentType)
	}
	if decoded.Parts[0].Headers["content-id"] != "0.smil" {
		t.Fatalf("unexpected first part headers: %#v", decoded.Parts[0].Headers)
	}
	if decoded.Parts[1].ContentType != "image/jpeg" {
		t.Fatalf("unexpected second part content type: %q", decoded.Parts[1].ContentType)
	}
	if decoded.Parts[1].Headers["content-location"] != "IMG_0018.JPG" {
		t.Fatalf("unexpected second part headers: %#v", decoded.Parts[1].Headers)
	}
	if decoded.Parts[1].ContentTypeParams["name"] != "IMG_0018.JPG" {
		t.Fatalf("unexpected second part params: %#v", decoded.Parts[1].ContentTypeParams)
	}
}

func TestSendReqWithPartsRoundTrip(t *testing.T) {
	t.Parallel()

	pdu := NewSendReqWithParts("txn-send", "+12025550100", []string{"+12025550101"}, []Part{
		{
			ContentType: "text/plain",
			Headers: map[string]string{
				"Content-ID": "<text1>",
			},
			Data: []byte("hello"),
		},
	})

	encoded, err := Encode(pdu)
	if err != nil {
		t.Fatalf("encode send req: %v", err)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode send req: %v", err)
	}
	if decoded.MessageType != MsgTypeSendReq {
		t.Fatalf("unexpected message type: %x", decoded.MessageType)
	}
	if decoded.Body == nil || len(decoded.Body.Parts) != 1 {
		t.Fatalf("expected multipart body, got %#v", decoded.Body)
	}
	if string(decoded.Body.Parts[0].Data) != "hello" {
		t.Fatalf("unexpected part payload: %q", string(decoded.Body.Parts[0].Data))
	}
}

func TestRetrieveConfRoundTrip(t *testing.T) {
	t.Parallel()

	pdu := NewRetrieveConf("txn-ret", []Part{
		{
			ContentType: "application/smil",
			Headers:     map[string]string{"content-id": "<smil>"},
			Data:        []byte("<smil/>"),
		},
		{
			ContentType: "image/jpeg",
			Headers:     map[string]string{"content-id": "<img1>"},
			Data:        []byte{0xFF, 0xD8, 0xFF, 0xE0},
		},
	})

	encoded, err := Encode(pdu)
	if err != nil {
		t.Fatalf("encode retrieve conf: %v", err)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode retrieve conf: %v", err)
	}
	if decoded.MessageType != MsgTypeRetrieveConf {
		t.Fatalf("unexpected message type: %x", decoded.MessageType)
	}
	// SMIL is stripped on delivery; only the image part should remain.
	if decoded.Body == nil || len(decoded.Body.Parts) != 1 {
		t.Fatalf("expected 1 part (SMIL stripped), got %#v", decoded.Body)
	}
	if decoded.Body.Parts[0].ContentType != "image/jpeg" {
		t.Fatalf("expected image/jpeg part, got %q", decoded.Body.Parts[0].ContentType)
	}
	ct, err := decoded.Headers[FieldContentType].ContentType()
	if err != nil {
		t.Fatalf("decode content-type: %v", err)
	}
	if ct.MediaType != "application/vnd.wap.multipart.mixed" {
		t.Fatalf("expected application/vnd.wap.multipart.mixed, got %q", ct.MediaType)
	}
}

func TestDeliveryIndParsing(t *testing.T) {
	t.Parallel()

	date := time.Unix(1710001234, 0).UTC()
	pdu := &PDU{
		MessageType: MsgTypeDeliveryInd,
		Headers: map[byte]HeaderValue{
			FieldMessageID: NewTextValue(FieldMessageID, "mid-1"),
			FieldTo:        NewAddressValue(FieldTo, "+12025550101"),
			FieldStatus:    NewTokenValue(FieldStatus, 0x80),
			FieldDate:      NewDateValue(FieldDate, date),
		},
	}

	parsed, err := ParseDeliveryInd(pdu)
	if err != nil {
		t.Fatalf("parse delivery ind: %v", err)
	}
	if parsed.MessageID != "mid-1" || parsed.To != "+12025550101/TYPE=PLMN" || parsed.Status != 0x80 {
		t.Fatalf("unexpected parsed delivery ind: %#v", parsed)
	}
	gotDate, err := parsed.Date.Time()
	if err != nil {
		t.Fatalf("delivery date parse: %v", err)
	}
	if !gotDate.Equal(date) {
		t.Fatalf("unexpected date: %s", gotDate)
	}
}

func TestAcknowledgeIndRoundTrip(t *testing.T) {
	t.Parallel()

	pdu := NewAcknowledgeInd("txn-ack")
	encoded, err := Encode(pdu)
	if err != nil {
		t.Fatalf("encode acknowledge ind: %v", err)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode acknowledge ind: %v", err)
	}
	if decoded.MessageType != MsgTypeAcknowledgeInd {
		t.Fatalf("unexpected message type: %x", decoded.MessageType)
	}
	if decoded.TransactionID != "txn-ack" {
		t.Fatalf("unexpected transaction id: %q", decoded.TransactionID)
	}
	if got, err := decoded.Headers[FieldReportAllowed].Token(); err != nil || got != BooleanYes {
		t.Fatalf("unexpected report-allowed token: %x err=%v", got, err)
	}
}

func TestForwardReqAndConfRoundTrip(t *testing.T) {
	t.Parallel()

	req := NewForwardReq("txn-fwd", []string{"+12025550101"}, []Part{
		{ContentType: "text/plain", Data: []byte("forward me")},
	})
	encodedReq, err := Encode(req)
	if err != nil {
		t.Fatalf("encode forward req: %v", err)
	}
	decodedReq, err := Decode(encodedReq)
	if err != nil {
		t.Fatalf("decode forward req: %v", err)
	}
	if decodedReq.MessageType != MsgTypeForwardReq {
		t.Fatalf("unexpected forward req type: %x", decodedReq.MessageType)
	}
	if decodedReq.Body == nil || len(decodedReq.Body.Parts) != 1 {
		t.Fatalf("expected forward req body, got %#v", decodedReq.Body)
	}

	conf := NewForwardConf("txn-fwd", "mid-fwd", ResponseStatusOk)
	encodedConf, err := Encode(conf)
	if err != nil {
		t.Fatalf("encode forward conf: %v", err)
	}
	decodedConf, err := Decode(encodedConf)
	if err != nil {
		t.Fatalf("decode forward conf: %v", err)
	}
	if decodedConf.MessageType != MsgTypeForwardConf {
		t.Fatalf("unexpected forward conf type: %x", decodedConf.MessageType)
	}
	if got, err := decodedConf.Headers[FieldMessageID].Text(); err != nil || got != "mid-fwd" {
		t.Fatalf("unexpected forward conf message-id: %q err=%v", got, err)
	}
}

func TestReferencePDUsDecodeEncode(t *testing.T) {
	t.Parallel()

	fixtures := []struct {
		name        string
		messageType uint8
		check       func(*testing.T, *PDU)
	}{
		{
			name:        "m-send-req.bin",
			messageType: MsgTypeSendReq,
			check: func(t *testing.T, pdu *PDU) {
				t.Helper()
				if pdu.TransactionID != "txn-send-ref" {
					t.Fatalf("unexpected transaction id: %q", pdu.TransactionID)
				}
				if pdu.Body == nil || len(pdu.Body.Parts) != 1 {
					t.Fatalf("expected send req body, got %#v", pdu.Body)
				}
			},
		},
		{
			name:        "m-notification-ind.bin",
			messageType: MsgTypeNotificationInd,
			check: func(t *testing.T, pdu *PDU) {
				t.Helper()
				if got, _ := pdu.Headers[FieldContentLocation].Text(); got == "" {
					t.Fatal("expected content-location in notification fixture")
				}
			},
		},
		{
			name:        "m-retrieve-conf.bin",
			messageType: MsgTypeRetrieveConf,
			check: func(t *testing.T, pdu *PDU) {
				t.Helper()
				if pdu.Body == nil || len(pdu.Body.Parts) != 1 {
					t.Fatalf("expected retrieve conf body, got %#v", pdu.Body)
				}
			},
		},
		{
			name:        "m-delivery-ind.bin",
			messageType: MsgTypeDeliveryInd,
			check: func(t *testing.T, pdu *PDU) {
				t.Helper()
				parsed, err := ParseDeliveryInd(pdu)
				if err != nil {
					t.Fatalf("parse delivery fixture: %v", err)
				}
				if parsed.MessageID != "mid-ref" {
					t.Fatalf("unexpected delivery fixture message id: %q", parsed.MessageID)
				}
			},
		},
	}

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join("..", "..", "test", "pdus", fixture.name)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture %s: %v", fixture.name, err)
			}

			pdu, err := Decode(raw)
			if err != nil {
				t.Fatalf("decode fixture %s: %v", fixture.name, err)
			}
			if pdu.MessageType != fixture.messageType {
				t.Fatalf("unexpected message type for %s: got %x want %x", fixture.name, pdu.MessageType, fixture.messageType)
			}
			fixture.check(t, pdu)

			encoded, err := Encode(pdu)
			if err != nil {
				t.Fatalf("re-encode fixture %s: %v", fixture.name, err)
			}
			// Re-decode the encoded form and check semantic equivalence. We do
			// not require byte-identical output because the encoder may use
			// well-known tokens while the fixture used text strings; both are
			// valid WAP binary encodings of the same content type.
			pdu2, err := Decode(encoded)
			if err != nil {
				t.Fatalf("decode re-encoded fixture %s: %v", fixture.name, err)
			}
			if pdu2.MessageType != fixture.messageType {
				t.Fatalf("re-encoded fixture %s has wrong message type: got %x want %x", fixture.name, pdu2.MessageType, fixture.messageType)
			}
			fixture.check(t, pdu2)
		})
	}
}
