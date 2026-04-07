package mmspdu

func NewReadRecInd(transactionID, messageID string) *PDU {
	return &PDU{
		MessageType:   MsgTypeReadRecInd,
		TransactionID: transactionID,
		MMSVersion:    "1.3",
		Headers: map[byte]HeaderValue{
			FieldTransactionID: NewTextValue(FieldTransactionID, transactionID),
			FieldMessageID:     NewTextValue(FieldMessageID, messageID),
		},
	}
}

func NewReadOrigInd(transactionID, messageID string) *PDU {
	return &PDU{
		MessageType:   MsgTypeReadOrigInd,
		TransactionID: transactionID,
		MMSVersion:    "1.3",
		Headers: map[byte]HeaderValue{
			FieldTransactionID: NewTextValue(FieldTransactionID, transactionID),
			FieldMessageID:     NewTextValue(FieldMessageID, messageID),
		},
	}
}
