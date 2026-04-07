package smpp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

const headerLen = 16

func Encode(pdu *PDU) ([]byte, error) {
	body, err := encodeBody(pdu)
	if err != nil {
		return nil, err
	}
	pdu.CommandLength = uint32(headerLen + len(body))

	out := make([]byte, pdu.CommandLength)
	binary.BigEndian.PutUint32(out[0:4], pdu.CommandLength)
	binary.BigEndian.PutUint32(out[4:8], pdu.CommandID)
	binary.BigEndian.PutUint32(out[8:12], pdu.CommandStatus)
	binary.BigEndian.PutUint32(out[12:16], pdu.SequenceNumber)
	copy(out[16:], body)
	return out, nil
}

func Decode(r io.Reader) (*PDU, error) {
	header := make([]byte, headerLen)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}

	pdu := &PDU{
		CommandLength:  binary.BigEndian.Uint32(header[0:4]),
		CommandID:      binary.BigEndian.Uint32(header[4:8]),
		CommandStatus:  binary.BigEndian.Uint32(header[8:12]),
		SequenceNumber: binary.BigEndian.Uint32(header[12:16]),
	}
	if pdu.CommandLength < headerLen {
		return nil, fmt.Errorf("smpp: invalid command length %d", pdu.CommandLength)
	}

	bodyLen := int(pdu.CommandLength) - headerLen
	if bodyLen == 0 {
		return pdu, nil
	}
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	if err := decodeBody(pdu, body); err != nil {
		return nil, err
	}
	return pdu, nil
}

func encodeBody(pdu *PDU) ([]byte, error) {
	var buf bytes.Buffer

	switch pdu.CommandID {
	case CmdBindReceiver, CmdBindTransmitter, CmdBindTransceiver:
		writeCString(&buf, pdu.SystemID)
		writeCString(&buf, pdu.Password)
		writeCString(&buf, pdu.SystemType)
		buf.WriteByte(pdu.InterfaceVersion)
		buf.WriteByte(pdu.AddrTON)
		buf.WriteByte(pdu.AddrNPI)
		writeCString(&buf, pdu.AddressRange)
	case CmdBindReceiverResp, CmdBindTransmitterResp, CmdBindTransceiverResp:
		writeCString(&buf, pdu.SystemID)
	case CmdSubmitSM, CmdDeliverSM:
		writeCString(&buf, pdu.ServiceType)
		buf.WriteByte(pdu.SourceAddrTON)
		buf.WriteByte(pdu.SourceAddrNPI)
		writeCString(&buf, pdu.SourceAddr)
		buf.WriteByte(pdu.DestAddrTON)
		buf.WriteByte(pdu.DestAddrNPI)
		writeCString(&buf, pdu.DestinationAddr)
		buf.WriteByte(pdu.ESMClass)
		buf.WriteByte(pdu.ProtocolID)
		buf.WriteByte(pdu.PriorityFlag)
		writeCString(&buf, pdu.ScheduleDeliveryTime)
		writeCString(&buf, pdu.ValidityPeriod)
		buf.WriteByte(pdu.RegisteredDelivery)
		buf.WriteByte(pdu.ReplaceIfPresentFlag)
		buf.WriteByte(pdu.DataCoding)
		buf.WriteByte(pdu.SMDefaultMsgID)
		pdu.SMLength = byte(len(pdu.ShortMessage))
		buf.WriteByte(pdu.SMLength)
		buf.Write(pdu.ShortMessage)
	case CmdSubmitSMResp, CmdDeliverSMResp:
		writeCString(&buf, pdu.MessageID)
	case CmdEnquireLink, CmdEnquireLinkResp, CmdUnbind, CmdUnbindResp, CmdGenericNack:
	default:
		return nil, fmt.Errorf("smpp: unsupported command id 0x%08x", pdu.CommandID)
	}

	return buf.Bytes(), nil
}

func decodeBody(pdu *PDU, body []byte) error {
	r := bytes.NewReader(body)
	var err error

	switch pdu.CommandID {
	case CmdBindReceiver, CmdBindTransmitter, CmdBindTransceiver:
		if pdu.SystemID, err = readCString(r); err != nil {
			return err
		}
		if pdu.Password, err = readCString(r); err != nil {
			return err
		}
		if pdu.SystemType, err = readCString(r); err != nil {
			return err
		}
		if pdu.InterfaceVersion, err = r.ReadByte(); err != nil {
			return err
		}
		if pdu.AddrTON, err = r.ReadByte(); err != nil {
			return err
		}
		if pdu.AddrNPI, err = r.ReadByte(); err != nil {
			return err
		}
		if pdu.AddressRange, err = readCString(r); err != nil {
			return err
		}
	case CmdBindReceiverResp, CmdBindTransmitterResp, CmdBindTransceiverResp:
		if pdu.SystemID, err = readCString(r); err != nil {
			return err
		}
	case CmdSubmitSM, CmdDeliverSM:
		if pdu.ServiceType, err = readCString(r); err != nil {
			return err
		}
		if pdu.SourceAddrTON, err = r.ReadByte(); err != nil {
			return err
		}
		if pdu.SourceAddrNPI, err = r.ReadByte(); err != nil {
			return err
		}
		if pdu.SourceAddr, err = readCString(r); err != nil {
			return err
		}
		if pdu.DestAddrTON, err = r.ReadByte(); err != nil {
			return err
		}
		if pdu.DestAddrNPI, err = r.ReadByte(); err != nil {
			return err
		}
		if pdu.DestinationAddr, err = readCString(r); err != nil {
			return err
		}
		if pdu.ESMClass, err = r.ReadByte(); err != nil {
			return err
		}
		if pdu.ProtocolID, err = r.ReadByte(); err != nil {
			return err
		}
		if pdu.PriorityFlag, err = r.ReadByte(); err != nil {
			return err
		}
		if pdu.ScheduleDeliveryTime, err = readCString(r); err != nil {
			return err
		}
		if pdu.ValidityPeriod, err = readCString(r); err != nil {
			return err
		}
		if pdu.RegisteredDelivery, err = r.ReadByte(); err != nil {
			return err
		}
		if pdu.ReplaceIfPresentFlag, err = r.ReadByte(); err != nil {
			return err
		}
		if pdu.DataCoding, err = r.ReadByte(); err != nil {
			return err
		}
		if pdu.SMDefaultMsgID, err = r.ReadByte(); err != nil {
			return err
		}
		if pdu.SMLength, err = r.ReadByte(); err != nil {
			return err
		}
		pdu.ShortMessage = make([]byte, pdu.SMLength)
		if _, err := io.ReadFull(r, pdu.ShortMessage); err != nil {
			return err
		}
	case CmdSubmitSMResp, CmdDeliverSMResp:
		if pdu.MessageID, err = readCString(r); err != nil && err != io.EOF {
			return err
		}
	case CmdEnquireLink, CmdEnquireLinkResp, CmdUnbind, CmdUnbindResp, CmdGenericNack:
	default:
		return fmt.Errorf("smpp: unsupported command id 0x%08x", pdu.CommandID)
	}

	return nil
}

func writeCString(buf *bytes.Buffer, s string) {
	buf.WriteString(s)
	buf.WriteByte(0x00)
}

func readCString(r *bytes.Reader) (string, error) {
	var out bytes.Buffer
	for {
		b, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		if b == 0x00 {
			return out.String(), nil
		}
		out.WriteByte(b)
	}
}
