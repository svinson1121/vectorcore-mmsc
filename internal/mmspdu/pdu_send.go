package mmspdu

func NewSendReq(transactionID, from string, to []string) *PDU {
	headers := map[byte]HeaderValue{
		FieldFrom:          NewFromValue(from),
		FieldTransactionID: NewTextValue(FieldTransactionID, transactionID),
	}
	if len(to) > 0 {
		headers[FieldTo] = NewAddressValue(FieldTo, to[0])
	}
	return &PDU{
		MessageType:   MsgTypeSendReq,
		TransactionID: transactionID,
		MMSVersion:    "1.3",
		Headers:       headers,
	}
}

func NewSendReqWithParts(transactionID, from string, to []string, parts []Part) *PDU {
	pdu := NewSendReq(transactionID, from, to)
	pdu.Headers[FieldContentType] = NewContentTypeValue("multipart/related", nil)
	pdu.Body = &MultipartBody{Parts: parts}
	return pdu
}

func NewSendConf(transactionID, messageID string, responseStatus byte) *PDU {
	return &PDU{
		MessageType:   MsgTypeSendConf,
		TransactionID: transactionID,
		MMSVersion:    "1.3",
		Headers: map[byte]HeaderValue{
			FieldTransactionID:  NewTextValue(FieldTransactionID, transactionID),
			FieldMessageID:      NewTextValue(FieldMessageID, messageID),
			FieldResponseStatus: NewTokenValue(FieldResponseStatus, responseStatus),
		},
	}
}
