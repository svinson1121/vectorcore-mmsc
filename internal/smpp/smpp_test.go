package smpp

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/vectorcore/vectorcore-mmsc/internal/wappush"
)

func TestClientSubmitWAPPush(t *testing.T) {
	t.Parallel()

	submits := make(chan *PDU, 8)
	serverConn, clientConn := net.Pipe()
	go fakeSMSCConn(t, serverConn, submits)

	client := NewClient(Config{
		Host:     "127.0.0.1",
		Port:     2775,
		SystemID: "mmsc",
		Password: "secret",
		BindMode: "transceiver",
	})
	client.SetDialFunc(func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	})
	defer client.Close()

	push := wappush.WrapMMSPDU(make([]byte, 240))
	if err := client.SubmitWAPPush(context.Background(), "+12025550199", "+12025550100", push); err != nil {
		t.Fatalf("submit wap push: %v", err)
	}

	count := 0
	timeout := time.After(2 * time.Second)
	for count < 2 {
		select {
		case pdu := <-submits:
			if pdu.CommandID != CmdSubmitSM || pdu.DestinationAddr != "+12025550100" || pdu.SourceAddr != "+12025550199" {
				t.Fatalf("unexpected submit pdu: %#v", pdu)
			}
			if pdu.ServiceType != "WAP" {
				t.Fatalf("unexpected service type: %q", pdu.ServiceType)
			}
			if pdu.DataCoding != 0xF5 {
				t.Fatalf("unexpected data coding: %x", pdu.DataCoding)
			}
			if pdu.RegisteredDelivery != 0x00 {
				t.Fatalf("unexpected registered delivery: %x", pdu.RegisteredDelivery)
			}
			count++
		case <-timeout:
			t.Fatalf("timed out waiting for submits, got %d", count)
		}
	}
}

func TestEnquireLink(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()
	go fakeSMSCConn(t, serverConn, nil)

	client := NewClient(Config{
		Host:     "127.0.0.1",
		Port:     2775,
		SystemID: "mmsc",
		Password: "secret",
		BindMode: "transceiver",
	})
	client.SetDialFunc(func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	})
	defer client.Close()

	if err := client.EnquireLink(context.Background()); err != nil {
		t.Fatalf("enquire link: %v", err)
	}
}

func TestClientHandlesUnsolicitedEnquireAndDeliverSM(t *testing.T) {
	t.Parallel()

	submits := make(chan *PDU, 8)
	delivers := make(chan *PDU, 1)
	serverConn, clientConn := net.Pipe()
	go func() {
		defer serverConn.Close()
		wrapped := NewConn(serverConn)
		bound := false
		for {
			pdu, err := wrapped.ReadPDU()
			if err != nil {
				return
			}
			switch pdu.CommandID {
			case CmdBindTransceiver:
				_ = wrapped.WritePDU(&PDU{
					CommandID:      CmdBindTransceiverResp,
					CommandStatus:  ESMEROK,
					SequenceNumber: pdu.SequenceNumber,
					SystemID:       "smsc",
				})
				if !bound {
					bound = true
					_ = wrapped.WritePDU(&PDU{
						CommandID:      CmdEnquireLink,
						CommandStatus:  ESMEROK,
						SequenceNumber: 99,
					})
					_ = wrapped.WritePDU(&PDU{
						CommandID:       CmdDeliverSM,
						CommandStatus:   ESMEROK,
						SequenceNumber:  100,
						SourceAddr:      "smsc",
						DestinationAddr: "+12025550100",
						ShortMessage:    []byte("dlr"),
					})
				}
			case CmdEnquireLinkResp:
				if pdu.SequenceNumber != 99 {
					t.Fatalf("unexpected enquire_link_resp sequence: %d", pdu.SequenceNumber)
				}
			case CmdDeliverSMResp:
				if pdu.SequenceNumber != 100 {
					t.Fatalf("unexpected deliver_sm_resp sequence: %d", pdu.SequenceNumber)
				}
			case CmdSubmitSM:
				submits <- pdu
				_ = wrapped.WritePDU(&PDU{
					CommandID:      CmdSubmitSMResp,
					CommandStatus:  ESMEROK,
					SequenceNumber: pdu.SequenceNumber,
					MessageID:      fmt.Sprintf("mid-%d", pdu.SequenceNumber),
				})
				return
			default:
				_ = wrapped.WritePDU(&PDU{
					CommandID:      CmdGenericNack,
					CommandStatus:  ESMERSYSERR,
					SequenceNumber: pdu.SequenceNumber,
				})
			}
		}
	}()

	client := NewClient(Config{
		Host:     "127.0.0.1",
		Port:     2775,
		SystemID: "mmsc",
		Password: "secret",
		BindMode: "transceiver",
	})
	client.SetDialFunc(func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	})
	client.SetDeliverSMHandler(func(pdu *PDU) {
		delivers <- pdu
	})
	defer client.Close()

	push := wappush.WrapMMSPDU(make([]byte, 32))
	if err := client.SubmitWAPPush(context.Background(), "+12025550199", "+12025550100", push); err != nil {
		t.Fatalf("submit wap push with unsolicited PDUs: %v", err)
	}

	select {
	case pdu := <-submits:
		if pdu.CommandID != CmdSubmitSM {
			t.Fatalf("unexpected submitted pdu: %#v", pdu)
		}
		if pdu.SourceAddr != "+12025550199" || pdu.SourceAddrTON != 0x01 || pdu.SourceAddrNPI != 0x01 {
			t.Fatalf("unexpected source addressing: %#v", pdu)
		}
		if pdu.DataCoding != 0xF5 {
			t.Fatalf("unexpected data coding: %x", pdu.DataCoding)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for submit_sm")
	}
	select {
	case pdu := <-delivers:
		if pdu.CommandID != CmdDeliverSM || string(pdu.ShortMessage) != "dlr" {
			t.Fatalf("unexpected unsolicited deliver_sm: %#v", pdu)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for deliver_sm callback")
	}
}

func TestParseDeliveryReceipt(t *testing.T) {
	t.Parallel()

	receipt, err := ParseDeliveryReceipt("id:abc123 sub:001 dlvrd:001 submit date:2404011200 done date:2404011201 stat:DELIVRD err:000 text:hello")
	if err != nil {
		t.Fatalf("parse lenient receipt: %v", err)
	}
	if receipt.ID != "abc123" || receipt.Stat != "DELIVRD" {
		t.Fatalf("unexpected lenient parsed receipt: %#v", receipt)
	}

	receipt, err = ParseDeliveryReceipt("id:abc123 sub:001 dlvrd:001 submit:2404011200 done:2404011201 stat:DELIVRD err:000 text:hello")
	if err != nil {
		t.Fatalf("parse receipt: %v", err)
	}
	if receipt.ID != "abc123" || receipt.Stat != "DELIVRD" || receipt.Err != "000" || receipt.Text != "hello" {
		t.Fatalf("unexpected parsed receipt: %#v", receipt)
	}
	if receipt.SubmittedAt == nil || receipt.DoneAt == nil {
		t.Fatalf("expected parsed timestamps, got %#v", receipt)
	}
}

func TestClientDeliversParsedReceiptToHandler(t *testing.T) {
	t.Parallel()

	var (
		mu      sync.Mutex
		receipt *DeliveryReceipt
	)
	serverConn, clientConn := net.Pipe()
	go func() {
		defer serverConn.Close()
		wrapped := NewConn(serverConn)
		for {
			pdu, err := wrapped.ReadPDU()
			if err != nil {
				return
			}
			switch pdu.CommandID {
			case CmdBindTransceiver:
				_ = wrapped.WritePDU(&PDU{
					CommandID:      CmdBindTransceiverResp,
					CommandStatus:  ESMEROK,
					SequenceNumber: pdu.SequenceNumber,
					SystemID:       "smsc",
				})
				_ = wrapped.WritePDU(&PDU{
					CommandID:       CmdDeliverSM,
					CommandStatus:   ESMEROK,
					SequenceNumber:  200,
					ESMClass:        0x04,
					SourceAddr:      "smsc",
					DestinationAddr: "+12025550100",
					ShortMessage:    []byte("id:abc123 sub:001 dlvrd:001 submit:2404011200 done:2404011201 stat:DELIVRD err:000 text:hello"),
				})
				return
			case CmdDeliverSMResp:
				return
			}
		}
	}()

	client := NewClient(Config{
		Host:     "127.0.0.1",
		Port:     2775,
		SystemID: "mmsc",
		Password: "secret",
		BindMode: "transceiver",
	})
	client.SetDialFunc(func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	})
	client.SetDeliverSMHandler(func(pdu *PDU) {
		r, err := ParseDeliveryReceipt(string(pdu.ShortMessage))
		if err != nil {
			t.Errorf("parse callback receipt: %v", err)
			return
		}
		mu.Lock()
		receipt = r
		mu.Unlock()
	})
	defer client.Close()

	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("connect client: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		got := receipt
		mu.Unlock()
		if got != nil {
			if got.ID != "abc123" || got.Stat != "DELIVRD" {
				t.Fatalf("unexpected callback receipt: %#v", got)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for parsed receipt callback")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func fakeSMSCConn(t *testing.T, conn net.Conn, submits chan<- *PDU) {
	t.Helper()
	defer conn.Close()

	wrapped := NewConn(conn)
	for {
		pdu, err := wrapped.ReadPDU()
		if err != nil {
			return
		}
		switch pdu.CommandID {
		case CmdBindTransceiver:
			_ = wrapped.WritePDU(&PDU{
				CommandID:      CmdBindTransceiverResp,
				CommandStatus:  ESMEROK,
				SequenceNumber: pdu.SequenceNumber,
				SystemID:       "smsc",
			})
		case CmdSubmitSM:
			if submits != nil {
				submits <- pdu
			}
			_ = wrapped.WritePDU(&PDU{
				CommandID:      CmdSubmitSMResp,
				CommandStatus:  ESMEROK,
				SequenceNumber: pdu.SequenceNumber,
				MessageID:      fmt.Sprintf("mid-%d", pdu.SequenceNumber),
			})
		case CmdEnquireLink:
			_ = wrapped.WritePDU(&PDU{
				CommandID:      CmdEnquireLinkResp,
				CommandStatus:  ESMEROK,
				SequenceNumber: pdu.SequenceNumber,
			})
		default:
			_ = wrapped.WritePDU(&PDU{
				CommandID:      CmdGenericNack,
				CommandStatus:  ESMERSYSERR,
				SequenceNumber: pdu.SequenceNumber,
			})
		}
	}
}

func TestWAPPushReferenceByteIsUnique(t *testing.T) {
	t.Parallel()

	submits := make(chan *PDU, 16)
	serverConn, clientConn := net.Pipe()
	go fakeSMSCConn(t, serverConn, submits)

	client := NewClient(Config{
		Host:     "127.0.0.1",
		Port:     2775,
		SystemID: "mmsc",
		Password: "secret",
		BindMode: "transceiver",
	})
	client.SetDialFunc(func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	})
	defer client.Close()

	// 200-byte MMS PDU produces a 2-segment WAP push (> 128-byte limit).
	push := wappush.WrapMMSPDU(make([]byte, 200))

	if err := client.SubmitWAPPush(context.Background(), "+12025550199", "+12025550100", push); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if err := client.SubmitWAPPush(context.Background(), "+12025550199", "+12025550100", push); err != nil {
		t.Fatalf("second submit: %v", err)
	}

	// Collect all 4 segments (2 pushes × 2 segments each).
	timeout := time.After(2 * time.Second)
	var pdus []*PDU
	for len(pdus) < 4 {
		select {
		case pdu := <-submits:
			pdus = append(pdus, pdu)
		case <-timeout:
			t.Fatalf("timed out, collected %d/4 segments", len(pdus))
		}
	}

	// UDH layout: [0x0B, port-IE(6 bytes), concat-IE-id, concat-IE-len, ref, total, seq]
	// reference byte is at index 9 of ShortMessage.
	ref := func(pdu *PDU) byte {
		if len(pdu.ShortMessage) < 10 {
			t.Fatalf("short message too small to contain UDH: %d bytes", len(pdu.ShortMessage))
		}
		return pdu.ShortMessage[9]
	}

	ref1, ref2 := ref(pdus[0]), ref(pdus[2])

	// Segments within the same push must share the same reference.
	if ref(pdus[0]) != ref(pdus[1]) {
		t.Fatalf("segments of the same push have different references: %x != %x", ref(pdus[0]), ref(pdus[1]))
	}
	if ref(pdus[2]) != ref(pdus[3]) {
		t.Fatalf("segments of the same push have different references: %x != %x", ref(pdus[2]), ref(pdus[3]))
	}

	// The two pushes must have different references.
	if ref1 == ref2 {
		t.Fatalf("two distinct pushes produced the same reference byte 0x%02x", ref1)
	}
}
