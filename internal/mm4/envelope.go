package mm4

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"strings"
	"time"

	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

const mm4Version = "6.3.0"

const (
	mm4ForwardReq         = "MM4_forward.REQ"
	mm4ForwardRes         = "MM4_forward.RES"
	mm4DeliveryReportReq  = "MM4_delivery_report.REQ"
	mm4DeliveryReportRes  = "MM4_delivery_report.RES"
	mm4ReadReplyReportReq = "MM4_read_reply_report.REQ"
	mm4ReadReplyReportRes = "MM4_read_reply_report.RES"
)

type EnvelopeMeta struct {
	MessageType       string
	OriginatorSystem  string
	AckRequested      bool
	RequestStatusCode string
	Status            string
	ReadStatus        string
}

func EncodeEnvelope(msg *message.Message, originHost string) ([]byte, error) {
	if msg == nil {
		return nil, fmt.Errorf("mm4: nil message")
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, part := range msg.Parts {
		header := textproto.MIMEHeader{}
		header.Set("Content-Type", part.ContentType)
		if part.ContentID != "" {
			header.Set("Content-ID", part.ContentID)
		}
		if part.ContentLocation != "" {
			header.Set("Content-Location", part.ContentLocation)
		}
		pw, err := writer.CreatePart(header)
		if err != nil {
			return nil, err
		}
		if _, err := pw.Write(part.Data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	header := textproto.MIMEHeader{}
	header.Set("From", msg.From)
	header.Set("To", strings.Join(msg.To, ","))
	header.Set("MIME-Version", "1.0")
	header.Set("Content-Type", "multipart/related; boundary="+writer.Boundary())
	header.Set("X-Mms-3GPP-MMS-Version", mm4Version)
	header.Set("X-Mms-Message-Type", mm4ForwardReq)
	header.Set("X-Mms-Transaction-ID", msg.TransactionID)
	header.Set("X-Mms-Message-ID", msg.ID)
	header.Set("X-Mms-Originator-System", fmt.Sprintf("system=%s;party=%s", originHost, msg.From))
	header.Set("X-Mms-Sender-Address", withPLMN(msg.From))
	header.Set("X-Mms-To", joinedRecipientsWithPLMN(msg.To))
	if msg.Subject != "" {
		header.Set("Subject", msg.Subject)
	}
	if msg.Expiry != nil {
		header.Set("X-Mms-Expiry", msg.Expiry.UTC().Format(time.RFC3339))
	}
	if msg.DeliveryReport {
		header.Set("X-Mms-Delivery-Report", "Yes")
	}

	var out bytes.Buffer
	for key, values := range header {
		for _, value := range values {
			fmt.Fprintf(&out, "%s: %s\r\n", key, value)
		}
	}
	out.WriteString("\r\n")
	out.Write(body.Bytes())
	return out.Bytes(), nil
}

func EncodeDeliveryReportEnvelope(msg *message.Message, originHost string, status string, statusCode string) ([]byte, error) {
	if msg == nil {
		return nil, fmt.Errorf("mm4: nil message")
	}
	header := textproto.MIMEHeader{}
	header.Set("From", originHost)
	header.Set("To", reportRecipient(msg))
	header.Set("MIME-Version", "1.0")
	header.Set("Content-Type", "text/plain")
	header.Set("X-Mms-3GPP-MMS-Version", mm4Version)
	header.Set("X-Mms-Message-Type", mm4DeliveryReportReq)
	header.Set("X-Mms-Transaction-ID", msg.TransactionID)
	header.Set("X-Mms-Message-ID", msg.ID)
	header.Set("X-Mms-Originator-System", fmt.Sprintf("system=%s;party=%s", originHost, firstRecipient(msg.To)))
	header.Set("X-Mms-Status", status)
	if statusCode != "" {
		header.Set("X-Mms-Request-Status-Code", statusCode)
	}
	return renderMM4Headers(header, "OK"), nil
}

func EncodeReadReplyEnvelope(msg *message.Message, originHost string, readStatus string, statusCode string) ([]byte, error) {
	if msg == nil {
		return nil, fmt.Errorf("mm4: nil message")
	}
	header := textproto.MIMEHeader{}
	header.Set("From", originHost)
	header.Set("To", reportRecipient(msg))
	header.Set("MIME-Version", "1.0")
	header.Set("Content-Type", "text/plain")
	header.Set("X-Mms-3GPP-MMS-Version", mm4Version)
	header.Set("X-Mms-Message-Type", mm4ReadReplyReportReq)
	header.Set("X-Mms-Transaction-ID", msg.TransactionID)
	header.Set("X-Mms-Message-ID", msg.ID)
	header.Set("X-Mms-Originator-System", fmt.Sprintf("system=%s;party=%s", originHost, firstRecipient(msg.To)))
	header.Set("X-Mms-Read-Status", readStatus)
	if statusCode != "" {
		header.Set("X-Mms-Request-Status-Code", statusCode)
	}
	return renderMM4Headers(header, "OK"), nil
}

func DecodeEnvelope(data []byte) (*message.Message, error) {
	msg, _, err := DecodeEnvelopeWithMeta(data)
	return msg, err
}

func DecodeEnvelopeWithMeta(data []byte) (*message.Message, *EnvelopeMeta, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return nil, nil, err
	}

	ctype := msg.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(ctype)
	if err != nil {
		return nil, nil, err
	}
	meta := &EnvelopeMeta{
		MessageType:       msg.Header.Get("X-Mms-Message-Type"),
		OriginatorSystem:  msg.Header.Get("X-Mms-Originator-System"),
		AckRequested:      strings.EqualFold(msg.Header.Get("X-Mms-Ack-Request"), "Yes"),
		RequestStatusCode: msg.Header.Get("X-Mms-Request-Status-Code"),
		Status:            msg.Header.Get("X-Mms-Status"),
		ReadStatus:        msg.Header.Get("X-Mms-Read-Status"),
	}
	out := &message.Message{
		ID:            msg.Header.Get("X-Mms-Message-ID"),
		TransactionID: msg.Header.Get("X-Mms-Transaction-ID"),
		From:          stripType(msg.Header.Get("X-Mms-Sender-Address")),
		To:            splitRecipients(msg.Header.Get("X-Mms-To")),
		Subject:       msg.Header.Get("Subject"),
		ContentType:   mediaType,
		MMSVersion:    "1.3",
		Origin:        message.InterfaceMM4,
		OriginHost:    meta.OriginatorSystem,
	}
	if !strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		return out, meta, nil
	}

	reader := multipart.NewReader(msg.Body, params["boundary"])

	for {
		part, err := reader.NextPart()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, nil, err
		}
		payload, err := io.ReadAll(part)
		if err != nil {
			return nil, nil, err
		}
		out.Parts = append(out.Parts, message.Part{
			ContentType:     part.Header.Get("Content-Type"),
			ContentID:       part.Header.Get("Content-ID"),
			ContentLocation: part.Header.Get("Content-Location"),
			Data:            payload,
			Size:            int64(len(payload)),
		})
	}
	if len(out.Parts) == 0 {
		return nil, nil, fmt.Errorf("mm4: missing message content")
	}
	return out, meta, nil
}

func withPLMN(value string) string {
	if value == "" || strings.Contains(strings.ToUpper(value), "/TYPE=") {
		return value
	}
	return value + "/TYPE=PLMN"
}

func stripType(value string) string {
	if idx := strings.Index(strings.ToUpper(value), "/TYPE="); idx > 0 {
		return value[:idx]
	}
	return value
}

func splitRecipients(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(stripType(part))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func firstRecipient(recipients []string) string {
	if len(recipients) == 0 {
		return ""
	}
	return recipients[0]
}

func joinedRecipientsWithPLMN(recipients []string) string {
	if len(recipients) == 0 {
		return ""
	}
	values := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		if strings.TrimSpace(recipient) == "" {
			continue
		}
		values = append(values, withPLMN(recipient))
	}
	return strings.Join(values, ",")
}

func EncodeResponseEnvelope(messageType, transactionID, messageID, statusCode, sender, to string) ([]byte, error) {
	header := textproto.MIMEHeader{}
	header.Set("From", sender)
	header.Set("To", to)
	header.Set("MIME-Version", "1.0")
	header.Set("Content-Type", "text/plain")
	header.Set("X-Mms-3GPP-MMS-Version", mm4Version)
	header.Set("X-Mms-Message-Type", messageType)
	header.Set("X-Mms-Transaction-ID", transactionID)
	if messageID != "" {
		header.Set("X-Mms-Message-ID", messageID)
	}
	if statusCode != "" {
		header.Set("X-Mms-Request-Status-Code", statusCode)
	}

	return renderMM4Headers(header, "OK"), nil
}

func renderMM4Headers(header textproto.MIMEHeader, body string) []byte {
	var out bytes.Buffer
	for key, values := range header {
		for _, value := range values {
			fmt.Fprintf(&out, "%s: %s\r\n", key, value)
		}
	}
	out.WriteString("\r\n")
	out.WriteString(body)
	return out.Bytes()
}

func parseOriginatorSystemHost(value string) string {
	for _, part := range strings.Split(value, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(part), "system=") {
			return strings.TrimSpace(part[len("system="):])
		}
	}
	return strings.TrimSpace(value)
}

func reportRecipient(msg *message.Message) string {
	if msg == nil || msg.From == "" {
		return ""
	}
	if strings.Contains(msg.From, "@") {
		return msg.From
	}
	return withPLMN(msg.From)
}
