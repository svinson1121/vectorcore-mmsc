package mmspdu

import (
	"bytes"
	"testing"
)

func FuzzDecode(f *testing.F) {
	seeds := []*PDU{
		NewSendReqWithParts("fuzz-send", "+12025550100", []string{"+12025550101"}, []Part{
			{
				ContentType: "text/plain",
				Headers:     map[string]string{"content-location": "message.txt"},
				Data:        []byte("hello"),
			},
		}),
		NewNotificationInd("fuzz-notify", "https://mmsc.example/retrieve/1"),
		NewDeliveryInd("fuzz-message", "+12025550101", StatusRetrieved),
	}
	for _, seed := range seeds {
		data, err := Encode(seed)
		if err != nil {
			f.Fatalf("encode seed: %v", err)
		}
		f.Add(data)
	}
	f.Add([]byte{})
	f.Add([]byte{0x8c})

	f.Fuzz(func(t *testing.T, data []byte) {
		pdu, err := Decode(data)
		if err != nil {
			return
		}
		encoded, err := Encode(pdu)
		if err != nil {
			t.Fatalf("re-encode decoded PDU: %v", err)
		}
		if _, err := Decode(encoded); err != nil {
			t.Fatalf("decode re-encoded PDU: %v", err)
		}
	})
}

func FuzzDecodeMultipart(f *testing.F) {
	seeds := []*MultipartBody{
		{},
		{Parts: []Part{{
			ContentType:       "application/smil",
			ContentTypeParams: map[string]string{"charset": "utf-8", "name": "main.smil"},
			Headers:           map[string]string{"content-id": "<smil>"},
			Data:              []byte("<smil/>"),
		}}},
		{Parts: []Part{
			{ContentType: "text/plain", Data: []byte("hello")},
			{ContentType: "image/jpeg", Data: []byte{0xff, 0xd8, 0xff, 0xd9}},
		}},
	}
	for _, seed := range seeds {
		data, err := EncodeMultipart(seed)
		if err != nil {
			f.Fatalf("encode seed: %v", err)
		}
		f.Add(data)
	}
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0x7f})

	f.Fuzz(func(t *testing.T, data []byte) {
		body, err := DecodeMultipart(data)
		if err != nil {
			return
		}
		encoded, err := EncodeMultipart(body)
		if err != nil {
			t.Fatalf("re-encode decoded multipart body: %v", err)
		}
		decoded, err := DecodeMultipart(encoded)
		if err != nil {
			t.Fatalf("decode re-encoded multipart body: %v; body=%#v encoded=%x", err, body, encoded)
		}
		reencoded, err := EncodeMultipart(decoded)
		if err != nil {
			t.Fatalf("second multipart encode: %v", err)
		}
		if !bytes.Equal(encoded, reencoded) {
			t.Fatalf("multipart encoding is not stable: first=%x second=%x", encoded, reencoded)
		}
	})
}
