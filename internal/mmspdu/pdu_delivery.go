package mmspdu

import "fmt"

type DeliveryInd struct {
	MessageID string
	To        string
	Status    byte
	Date      HeaderValue
}

func NewDeliveryInd(messageID, to string, status byte) *PDU {
	return &PDU{
		MessageType: MsgTypeDeliveryInd,
		MMSVersion:  "1.3",
		Headers: map[byte]HeaderValue{
			FieldMessageID: NewTextValue(FieldMessageID, messageID),
			FieldTo:        NewAddressValue(FieldTo, to),
			FieldStatus:    NewTokenValue(FieldStatus, status),
		},
	}
}

func ParseDeliveryInd(pdu *PDU) (*DeliveryInd, error) {
	if pdu == nil {
		return nil, fmt.Errorf("mmspdu: nil pdu")
	}
	out := &DeliveryInd{}
	if value, ok := pdu.Headers[FieldMessageID]; ok {
		text, err := value.Text()
		if err != nil {
			return nil, err
		}
		out.MessageID = text
	}
	if value, ok := pdu.Headers[FieldTo]; ok {
		text, err := value.Text()
		if err != nil {
			return nil, err
		}
		out.To = text
	}
	if value, ok := pdu.Headers[FieldStatus]; ok {
		token, err := value.Token()
		if err != nil {
			return nil, err
		}
		out.Status = token
	}
	if value, ok := pdu.Headers[FieldDate]; ok {
		out.Date = value
	}
	return out, nil
}
