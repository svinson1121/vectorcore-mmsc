package mmspdu

import "strings"

func NewRetrieveConf(transactionID string, parts []Part) *PDU {
	deliveryParts := stripSMILParts(parts)
	return &PDU{
		MessageType:   MsgTypeRetrieveConf,
		TransactionID: transactionID,
		MMSVersion:    "1.3",
		Headers: map[byte]HeaderValue{
			FieldTransactionID: NewTextValue(FieldTransactionID, transactionID),
			FieldContentType:   ContentTypeFromParts(deliveryParts),
		},
		Body: &MultipartBody{Parts: deliveryParts},
	}
}

// stripSMILParts removes application/smil parts from the part list, leaving
// only the media content (images, text, etc.) for delivery.
func stripSMILParts(parts []Part) []Part {
	out := make([]Part, 0, len(parts))
	for _, p := range parts {
		if !strings.EqualFold(p.ContentType, "application/smil") {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return parts // don't strip everything
	}
	return out
}

// ContentTypeFromParts derives the outer multipart Content-Type for a
// m-retrieve-conf. Uses application/vnd.wap.multipart.mixed (WAP-native
// multipart type, token 0xA3) which iOS recognises for inline rendering.
func ContentTypeFromParts(parts []Part) HeaderValue {
	return NewContentTypeValue("application/vnd.wap.multipart.mixed", nil)
}
