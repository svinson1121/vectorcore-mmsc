package mmspdu

// notificationIndHeaderOrder is the mandatory field order for m-notification-ind
// per OMA MMS 1.2/1.3 ENC §7.3. X-Mms-Content-Location must be last.
// encodeHeadersBySequence silently skips fields absent from the headers map,
// so optional fields (Subject, Priority) are safe to include here.
var notificationIndHeaderOrder = []byte{
	FieldMessageType, FieldTransactionID, FieldMMSVersion,
	FieldFrom, FieldSubject, FieldMessageClass, FieldPriority,
	FieldMessageSize, FieldExpiry, FieldContentLocation,
}

func NewNotificationInd(transactionID, contentLocation string) *PDU {
	return &PDU{
		MessageType:   MsgTypeNotificationInd,
		TransactionID: transactionID,
		MMSVersion:    "1.3",
		Headers: map[byte]HeaderValue{
			FieldTransactionID:   NewTextValue(FieldTransactionID, transactionID),
			FieldContentLocation: NewTextValue(FieldContentLocation, contentLocation),
		},
		HeaderOrder: notificationIndHeaderOrder,
	}
}

func NewNotifyRespInd(transactionID string, status byte) *PDU {
	return &PDU{
		MessageType:   MsgTypeNotifyRespInd,
		TransactionID: transactionID,
		MMSVersion:    "1.3",
		Headers: map[byte]HeaderValue{
			FieldTransactionID: NewTextValue(FieldTransactionID, transactionID),
			FieldStatus:        NewTokenValue(FieldStatus, status),
		},
	}
}
