package mmspdu

func NewAcknowledgeInd(transactionID string) *PDU {
	return &PDU{
		MessageType:   MsgTypeAcknowledgeInd,
		TransactionID: transactionID,
		MMSVersion:    "1.3",
		Headers: map[byte]HeaderValue{
			FieldTransactionID: NewTextValue(FieldTransactionID, transactionID),
			FieldReportAllowed: NewTokenValue(FieldReportAllowed, BooleanYes),
		},
	}
}

func NewForwardReq(transactionID string, to []string, parts []Part) *PDU {
	headers := map[byte]HeaderValue{
		FieldTransactionID: NewTextValue(FieldTransactionID, transactionID),
	}
	if len(to) > 0 {
		headers[FieldTo] = NewAddressValue(FieldTo, to[0])
	}
	return &PDU{
		MessageType:   MsgTypeForwardReq,
		TransactionID: transactionID,
		MMSVersion:    "1.3",
		Headers:       headers,
		Body:          &MultipartBody{Parts: parts},
	}
}

func NewForwardConf(transactionID, messageID string, responseStatus byte) *PDU {
	return &PDU{
		MessageType:   MsgTypeForwardConf,
		TransactionID: transactionID,
		MMSVersion:    "1.3",
		Headers: map[byte]HeaderValue{
			FieldTransactionID:  NewTextValue(FieldTransactionID, transactionID),
			FieldMessageID:      NewTextValue(FieldMessageID, messageID),
			FieldResponseStatus: NewTokenValue(FieldResponseStatus, responseStatus),
		},
	}
}
