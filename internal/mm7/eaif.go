package mm7

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/vectorcore/vectorcore-mmsc/internal/message"
	"github.com/vectorcore/vectorcore-mmsc/internal/mmspdu"
)

type EAIFRequest struct {
	From       string
	Recipients []string
	PDU        *mmspdu.PDU
	Raw        []byte
}

func ParseEAIFRequest(r *http.Request) (*EAIFRequest, error) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("missing MMS body")
	}
	pdu, err := mmspdu.Decode(raw)
	if err != nil {
		return nil, fmt.Errorf("decode eaif mms body: %w", err)
	}
	if pdu.MessageType != mmspdu.MsgTypeSendReq && pdu.MessageType != mmspdu.MsgTypeForwardReq {
		return nil, fmt.Errorf("unsupported eaif message type")
	}

	recipients := splitEAIFHeaderValues(r.Header.Values("X-NOKIA-MMSC-To"))
	if len(recipients) == 0 {
		if to, err := pduHeaderText(pdu, mmspdu.FieldTo); err == nil && strings.TrimSpace(to) != "" {
			recipients = []string{strings.TrimSpace(stripTypeSuffix(to))}
		}
	}
	if len(recipients) == 0 {
		return nil, fmt.Errorf("missing recipients")
	}

	from := strings.TrimSpace(r.Header.Get("X-NOKIA-MMSC-From"))
	if from == "" {
		if value, err := pduHeaderText(pdu, mmspdu.FieldFrom); err == nil {
			from = stripTypeSuffix(strings.TrimSpace(value))
		}
	}
	if from == "" {
		from = "anon@anon"
	}

	return &EAIFRequest{
		From:       from,
		Recipients: recipients,
		PDU:        pdu,
		Raw:        raw,
	}, nil
}

func WriteEAIFResponse(w http.ResponseWriter, statusCode int, version, messageID string) {
	w.Header().Set("X-NOKIA-MMSC-Version", resolveEAIFVersion(version))
	if messageID != "" {
		w.Header().Set("X-NOKIA-MMSC-Message-Id", messageID)
	}
	w.WriteHeader(statusCode)
	_, _ = w.Write(bytes.NewBuffer(nil).Bytes())
}

func splitEAIFHeaderValues(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(stripTypeSuffix(part))
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func messagePartsFromPDU(pdu *mmspdu.PDU) []message.Part {
	if pdu == nil || pdu.Body == nil || len(pdu.Body.Parts) == 0 {
		return nil
	}
	parts := make([]message.Part, 0, len(pdu.Body.Parts))
	for _, part := range pdu.Body.Parts {
		item := message.Part{
			ContentType: part.ContentType,
			Data:        append([]byte(nil), part.Data...),
			Size:        int64(len(part.Data)),
		}
		if cid, ok := part.Headers["content-id"]; ok {
			item.ContentID = normalizeCID(cid)
		}
		if loc, ok := part.Headers["content-location"]; ok {
			item.ContentLocation = loc
		}
		parts = append(parts, item)
	}
	return parts
}

func resolveEAIFVersion(version string) string {
	if strings.TrimSpace(version) == "" {
		return "3.0"
	}
	return version
}

func pduHeaderText(pdu *mmspdu.PDU, field byte) (string, error) {
	if pdu == nil {
		return "", fmt.Errorf("nil pdu")
	}
	value, ok := pdu.Headers[field]
	if !ok {
		return "", fmt.Errorf("header missing")
	}
	return value.Text()
}

func pduContentType(pdu *mmspdu.PDU) string {
	if pdu == nil {
		return ""
	}
	value, ok := pdu.Headers[mmspdu.FieldContentType]
	if !ok {
		return ""
	}
	ct, err := value.ContentType()
	if err != nil {
		return ""
	}
	return ct.MediaType
}

func stripTypeSuffix(value string) string {
	if idx := strings.Index(strings.ToUpper(value), "/TYPE="); idx > 0 {
		return value[:idx]
	}
	return value
}
