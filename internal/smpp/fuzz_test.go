package smpp

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func FuzzDecode(f *testing.F) {
	seeds := []*PDU{
		{CommandID: CmdEnquireLink, SequenceNumber: 1},
		{
			CommandID:        CmdBindTransceiver,
			SequenceNumber:   2,
			SystemID:         "mmsc",
			Password:         "secret",
			InterfaceVersion: 0x34,
		},
		{
			CommandID:       CmdSubmitSM,
			SequenceNumber:  3,
			SourceAddr:      "+12025550100",
			DestinationAddr: "+12025550101",
			ShortMessage:    []byte("hello"),
		},
		{CommandID: CmdSubmitSMResp, SequenceNumber: 3, MessageID: "message-1"},
	}
	for _, seed := range seeds {
		data, err := Encode(seed)
		if err != nil {
			f.Fatalf("encode seed: %v", err)
		}
		f.Add(data)
	}
	f.Add([]byte{})
	oversized := make([]byte, headerLen)
	binary.BigEndian.PutUint32(oversized[:4], maxCommandLength+1)
	f.Add(oversized)

	f.Fuzz(func(t *testing.T, data []byte) {
		pdu, err := Decode(bytes.NewReader(data))
		if err != nil {
			return
		}
		encoded, err := Encode(pdu)
		if err != nil {
			t.Fatalf("re-encode decoded PDU: %v", err)
		}
		if _, err := Decode(bytes.NewReader(encoded)); err != nil {
			t.Fatalf("decode re-encoded PDU: %v", err)
		}
	})
}
