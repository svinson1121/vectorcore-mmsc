package mmspdu

const (
	FieldBCC                   = 0x01
	FieldCC                    = 0x02
	FieldContentLocation       = 0x03
	FieldContentType           = 0x04
	FieldDate                  = 0x05
	FieldDeliveryReport        = 0x06
	FieldDeliveryTime          = 0x07
	FieldExpiry                = 0x08
	FieldFrom                  = 0x09
	FieldMessageClass          = 0x0A
	FieldMessageID             = 0x0B
	FieldMessageType           = 0x0C
	FieldMMSVersion            = 0x0D
	FieldMessageSize           = 0x0E
	FieldPriority              = 0x0F
	FieldReadReply             = 0x10
	FieldReportAllowed         = 0x11
	FieldResponseStatus        = 0x12
	FieldResponseText          = 0x13
	FieldSenderVisibility      = 0x14
	FieldStatus                = 0x15
	FieldSubject               = 0x16
	FieldTo                    = 0x17
	FieldTransactionID         = 0x18
	FieldRetrieveStatus        = 0x19
	FieldRetrieveText          = 0x1A
	FieldReadStatus            = 0x1B
	FieldReplyCharging         = 0x1C
	FieldReplyChargingDeadline = 0x1D
	FieldReplyChargingID       = 0x1E
	FieldReplyChargingSize     = 0x1F
	FieldPreviouslySentBy      = 0x20
	FieldPreviouslySentDate    = 0x21
)

const (
	MsgTypeSendReq         = 0x80
	MsgTypeSendConf        = 0x81
	MsgTypeNotificationInd = 0x82
	MsgTypeNotifyRespInd   = 0x83
	MsgTypeRetrieveConf    = 0x84
	MsgTypeAcknowledgeInd  = 0x85
	MsgTypeDeliveryInd     = 0x86
	MsgTypeReadRecInd      = 0x87
	MsgTypeReadOrigInd     = 0x88
	MsgTypeForwardReq      = 0x89
	MsgTypeForwardConf     = 0x8A
)

const (
	MMSVersion10 = 0x90
	MMSVersion11 = 0x91
	MMSVersion12 = 0x92
	MMSVersion13 = 0x93
)

const (
	BooleanNo  = 0x80
	BooleanYes = 0x81
)

const (
	MessageClassPersonal      = 0x80
	MessageClassAdvertisement = 0x81
	MessageClassInformational = 0x82
	MessageClassAuto          = 0x83
)

const (
	PriorityLow    = 0x80
	PriorityNormal = 0x81
	PriorityHigh   = 0x82
)

const (
	ResponseStatusOk                       = 0x80
	ResponseStatusErrorUnsupported         = 0x87
	ResponseStatusErrorMessage             = 0x88
	ResponseStatusErrorMessageSizeExceeded = 0x93
)

// X-Mms-Status token values (OMA MMS ENC §7.3.42), used in m-notify-resp and
// m-delivery-ind. These are distinct from X-Mms-Response-Status tokens.
const (
	StatusExpired       = 0x80
	StatusRetrieved     = 0x81
	StatusRejected      = 0x82
	StatusDeferred      = 0x83
	StatusUnrecognised  = 0x84
	StatusIndeterminate = 0x85
	StatusForwarded     = 0x86
	StatusUnreachable   = 0x87
)

type FieldKind uint8

const (
	FieldKindUnknown FieldKind = iota
	FieldKindOctets
	FieldKindText
	FieldKindEncodedString
	FieldKindToken
	FieldKindShortInteger
	FieldKindLongInteger
	FieldKindDate
	FieldKindAddress
)

var fieldKinds = map[byte]FieldKind{
	FieldBCC:              FieldKindAddress,
	FieldCC:               FieldKindAddress,
	FieldContentLocation:  FieldKindText,
	FieldContentType:      FieldKindOctets,
	FieldDate:             FieldKindDate,
	FieldDeliveryReport:   FieldKindToken,
	FieldDeliveryTime:     FieldKindDate,
	FieldExpiry:           FieldKindDate,
	FieldFrom:             FieldKindAddress,
	FieldMessageClass:     FieldKindToken,
	FieldMessageID:        FieldKindText,
	FieldMessageType:      FieldKindToken,
	FieldMMSVersion:       FieldKindShortInteger,
	FieldMessageSize:      FieldKindLongInteger,
	FieldPriority:         FieldKindToken,
	FieldReadReply:        FieldKindToken,
	FieldReportAllowed:    FieldKindToken,
	FieldResponseStatus:   FieldKindToken,
	FieldResponseText:     FieldKindEncodedString,
	FieldSenderVisibility: FieldKindToken,
	FieldStatus:           FieldKindToken,
	FieldSubject:          FieldKindEncodedString,
	FieldTo:               FieldKindAddress,
	FieldTransactionID:    FieldKindText,
	FieldRetrieveStatus:   FieldKindToken,
	FieldRetrieveText:     FieldKindEncodedString,
	FieldReadStatus:       FieldKindToken,
}

func kindForField(field byte) FieldKind {
	if kind, ok := fieldKinds[field]; ok {
		return kind
	}
	return FieldKindUnknown
}
