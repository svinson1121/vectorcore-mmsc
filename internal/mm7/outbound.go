package mm7

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"

	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
	"github.com/vectorcore/vectorcore-mmsc/internal/mmspdu"
)

type SendFunc func(ctx context.Context, url string, contentType string, body []byte, authHeader string, headers http.Header) ([]byte, string, error)

type Notifier struct {
	repo      db.Repository
	send      SendFunc
	version   string
	eaifVer   string
	namespace string
}

func NewNotifier(repo db.Repository, opts ...Option) *Notifier {
	n := &Notifier{
		repo:      repo,
		send:      sendHTTPRequest,
		version:   defaultMM7Ver,
		eaifVer:   "3.0",
		namespace: defaultNamespace,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(n)
		}
	}
	n.version = resolveMM7Version(n.version)
	n.eaifVer = resolveEAIFVersion(n.eaifVer)
	n.namespace = resolveMM7Namespace(n.namespace)
	return n
}

type Option func(*Notifier)

func WithProtocol(version string, namespace string) Option {
	return func(n *Notifier) {
		n.version = version
		n.namespace = namespace
	}
}

func WithEAIFVersion(version string) Option {
	return func(n *Notifier) {
		n.eaifVer = version
	}
}

func NewNotifierWithProtocol(repo db.Repository, version string, namespace string) *Notifier {
	return &Notifier{
		repo:      repo,
		send:      sendHTTPRequest,
		version:   resolveMM7Version(version),
		eaifVer:   "3.0",
		namespace: resolveMM7Namespace(namespace),
	}
}

func (n *Notifier) SendDeliveryReport(ctx context.Context, msg *message.Message, status message.Status) error {
	if msg == nil || msg.Origin != message.InterfaceMM7 || msg.OriginHost == "" {
		return nil
	}
	vasp, err := n.lookupVASP(ctx, msg.OriginHost)
	if err != nil || vasp.ReportURL == "" {
		return err
	}
	_ = n.repo.AppendMessageEvent(ctx, db.MessageEvent{
		MessageID: msg.ID,
		Source:    "mm7",
		Type:      "delivery-report",
		Summary:   "MM7 delivery report sent",
		Detail:    fmt.Sprintf("vasp=%s status=%s protocol=%s", vasp.VASPID, mm7StatusForMessage(status), vasp.Protocol),
	})
	req := DeliveryReportReq{
		MM7Version: n.version,
		MessageID:  msg.ID,
		Recipient:  recipientForMessage(msg),
		MMStatus:   mm7StatusForMessage(status),
		StatusText: statusTextForMessage(status),
	}
	respBody, respContentType, err := n.sendReport(ctx, vasp, msg, "delivery-report", req)
	if err != nil {
		return err
	}
	if isEAIFVASP(vasp) {
		return validateEAIFResponse(respContentType)
	}
	return validateSOAPResponse("delivery-report", respBody, respContentType)
}

func (n *Notifier) SendReadReply(ctx context.Context, msg *message.Message) error {
	if msg == nil || msg.Origin != message.InterfaceMM7 || msg.OriginHost == "" {
		return nil
	}
	vasp, err := n.lookupVASP(ctx, msg.OriginHost)
	if err != nil || vasp.ReportURL == "" {
		return err
	}
	_ = n.repo.AppendMessageEvent(ctx, db.MessageEvent{
		MessageID: msg.ID,
		Source:    "mm7",
		Type:      "read-reply",
		Summary:   "MM7 read reply sent",
		Detail:    fmt.Sprintf("vasp=%s protocol=%s", vasp.VASPID, vasp.Protocol),
	})
	req := ReadReplyReq{
		MM7Version: n.version,
		MessageID:  msg.ID,
		Recipient:  recipientForMessage(msg),
		MMStatus:   "Read",
	}
	respBody, respContentType, err := n.sendReport(ctx, vasp, msg, "read-reply", req)
	if err != nil {
		return err
	}
	if isEAIFVASP(vasp) {
		return validateEAIFResponse(respContentType)
	}
	return validateSOAPResponse("read-reply", respBody, respContentType)
}

func (n *Notifier) SendDeliverReq(ctx context.Context, vaspID string, msg *message.Message) error {
	if msg == nil {
		return nil
	}
	vasp, err := n.lookupVASP(ctx, vaspID)
	if err != nil || vasp.DeliverURL == "" {
		return err
	}
	_ = n.repo.AppendMessageEvent(ctx, db.MessageEvent{
		MessageID: msg.ID,
		Source:    "mm7",
		Type:      "deliver-req",
		Summary:   "MM7 deliver request sent",
		Detail:    fmt.Sprintf("vasp=%s protocol=%s", vasp.VASPID, vasp.Protocol),
	})
	respBody, respContentType, err := n.sendDeliver(ctx, vasp, msg)
	if err != nil {
		return err
	}
	if isEAIFVASP(vasp) {
		return validateEAIFResponse(respContentType)
	}
	return validateSOAPResponse("deliver", respBody, respContentType)
}

func (n *Notifier) lookupVASP(ctx context.Context, vaspID string) (*db.MM7VASP, error) {
	vasps, err := n.repo.ListMM7VASPs(ctx)
	if err != nil {
		return nil, err
	}
	for _, vasp := range vasps {
		if vasp.Active && vasp.VASPID == vaspID {
			vaspCopy := vasp
			return &vaspCopy, nil
		}
	}
	return nil, fmt.Errorf("mm7 vasp %q not found", vaspID)
}

type outboundAttachment struct {
	ContentID   string
	ContentType string
	Data        []byte
}

func encodeSOAPRequest(operation, transactionID string, payload any, attachments []outboundAttachment, namespace string) ([]byte, string, error) {
	var soap bytes.Buffer
	soap.WriteString(xml.Header)
	encoder := xml.NewEncoder(&soap)
	envStart := xml.StartElement{
		Name: xml.Name{Local: "soapenv:Envelope"},
		Attr: []xml.Attr{
			{Name: xml.Name{Local: "xmlns:soapenv"}, Value: soapEnvNamespace},
			{Name: xml.Name{Local: "xmlns:mm7"}, Value: resolveMM7Namespace(namespace)},
		},
	}
	if err := encoder.EncodeToken(envStart); err != nil {
		return nil, "", err
	}
	if err := encodeHeader(encoder, Header{TransactionID: TransactionID{MustUnderstand: "1", Value: transactionID}}); err != nil {
		return nil, "", err
	}
	start := xml.StartElement{Name: xml.Name{Local: "soapenv:Body"}}
	if err := encoder.EncodeToken(start); err != nil {
		return nil, "", err
	}
	name := ""
	switch operation {
	case "deliver":
		name = "mm7:DeliverReq"
	case "delivery-report":
		name = "mm7:DeliveryReportReq"
	case "read-reply":
		name = "mm7:ReadReplyReq"
	default:
		return nil, "", fmt.Errorf("unsupported mm7 request operation %q", operation)
	}
	if err := encoder.EncodeElement(payload, xml.StartElement{Name: xml.Name{Local: name}}); err != nil {
		return nil, "", err
	}
	if err := encoder.EncodeToken(start.End()); err != nil {
		return nil, "", err
	}
	if err := encoder.EncodeToken(envStart.End()); err != nil {
		return nil, "", err
	}
	if err := encoder.Flush(); err != nil {
		return nil, "", err
	}

	if len(attachments) == 0 {
		return soap.Bytes(), "text/xml; charset=utf-8", nil
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	rootHeader := textproto.MIMEHeader{}
	rootHeader.Set("Content-Type", "text/xml; charset=utf-8")
	rootPart, err := writer.CreatePart(rootHeader)
	if err != nil {
		return nil, "", err
	}
	if _, err := rootPart.Write(soap.Bytes()); err != nil {
		return nil, "", err
	}
	for _, attachment := range attachments {
		header := textproto.MIMEHeader{}
		header.Set("Content-Type", attachment.ContentType)
		header.Set("Content-ID", "<"+attachment.ContentID+">")
		part, err := writer.CreatePart(header)
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(attachment.Data); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return body.Bytes(), "multipart/related; boundary=" + writer.Boundary(), nil
}

func sendHTTPRequest(ctx context.Context, url string, contentType string, body []byte, authHeader string, headers http.Header) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", contentType)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("mm7 remote returned status %d", resp.StatusCode)
	}
	return payload, resp.Header.Get("Content-Type"), nil
}

func (n *Notifier) sendDeliver(ctx context.Context, vasp *db.MM7VASP, msg *message.Message) ([]byte, string, error) {
	if isEAIFVASP(vasp) {
		body, headers, err := encodeEAIFDeliver(msg, resolveVASPVersion(vasp, n.eaifVer))
		if err != nil {
			return nil, "", err
		}
		return n.send(ctx, vasp.DeliverURL, "application/vnd.wap.mms-message", body, basicAuthHeader(vasp), headers)
	}
	contentID := "message-content"
	req := DeliverReq{
		MM7Version: n.version,
		MessageID:  msg.ID,
		Sender:     SenderAddress{Number: msg.From},
		Recipients: recipientsForMessage(msg),
		Subject:    msg.Subject,
		Content:    ContentRef{Href: "cid:" + contentID},
	}
	attachments := attachmentsForMessage(contentID, msg)
	body, contentType, err := encodeSOAPRequest("deliver", msg.TransactionID, req, attachments, n.namespace)
	if err != nil {
		return nil, "", err
	}
	return n.send(ctx, vasp.DeliverURL, contentType, body, basicAuthHeader(vasp), nil)
}

func (n *Notifier) sendReport(ctx context.Context, vasp *db.MM7VASP, msg *message.Message, operation string, payload any) ([]byte, string, error) {
	if isEAIFVASP(vasp) {
		body, headers, err := encodeEAIFReport(msg, operation, resolveVASPVersion(vasp, n.eaifVer))
		if err != nil {
			return nil, "", err
		}
		return n.send(ctx, vasp.ReportURL, "application/vnd.wap.mms-message", body, basicAuthHeader(vasp), headers)
	}
	body, contentType, err := encodeSOAPRequest(operation, msg.TransactionID, payload, nil, n.namespace)
	if err != nil {
		return nil, "", err
	}
	return n.send(ctx, vasp.ReportURL, contentType, body, basicAuthHeader(vasp), nil)
}

func validateSOAPResponse(operation string, payload []byte, contentType string) error {
	env, err := decodeSOAPEnvelope(payload, contentType)
	if err != nil {
		return err
	}
	if env.Body.Fault != nil {
		return fmt.Errorf("mm7 fault: %s", env.Body.Fault.FaultString)
	}

	var status *MM7Status
	switch operation {
	case "deliver":
		if env.Body.DeliverRsp == nil {
			return fmt.Errorf("mm7 response missing DeliverRsp")
		}
		status = &env.Body.DeliverRsp.Status
	case "delivery-report":
		if env.Body.DeliveryReportRsp == nil {
			return fmt.Errorf("mm7 response missing DeliveryReportRsp")
		}
		status = &env.Body.DeliveryReportRsp.Status
	case "read-reply":
		if env.Body.ReadReplyRsp == nil {
			return fmt.Errorf("mm7 response missing ReadReplyRsp")
		}
		status = &env.Body.ReadReplyRsp.Status
	default:
		return fmt.Errorf("unsupported mm7 response operation %q", operation)
	}
	if status == nil {
		return fmt.Errorf("mm7 response missing status")
	}
	if !strings.HasPrefix(status.StatusCode, "1") {
		return fmt.Errorf("mm7 response status %s: %s", status.StatusCode, status.StatusText)
	}
	return nil
}

func validateEAIFResponse(contentType string) error {
	if contentType == "" {
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return fmt.Errorf("parse eaif response content type: %w", err)
	}
	if mediaType != "text/plain" && mediaType != "application/octet-stream" && mediaType != "application/vnd.wap.mms-message" {
		return fmt.Errorf("unexpected eaif response content type %q", mediaType)
	}
	return nil
}

func decodeSOAPEnvelope(payload []byte, contentType string) (*Envelope, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil && contentType != "" {
		return nil, fmt.Errorf("parse response content type: %w", err)
	}
	if strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		reader := multipart.NewReader(bytes.NewReader(payload), params["boundary"])
		part, err := reader.NextPart()
		if err != nil {
			return nil, fmt.Errorf("read multipart soap response: %w", err)
		}
		payload, err = io.ReadAll(part)
		if err != nil {
			return nil, fmt.Errorf("read soap response part: %w", err)
		}
	}
	var env Envelope
	if err := xml.NewDecoder(bytes.NewReader(payload)).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode soap response: %w", err)
	}
	return &env, nil
}

func basicAuthHeader(vasp *db.MM7VASP) string {
	if vasp == nil || vasp.SharedSecret == "" {
		return ""
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(vasp.VASPID+":"+vasp.SharedSecret))
}

func isEAIFVASP(vasp *db.MM7VASP) bool {
	return vasp != nil && strings.EqualFold(strings.TrimSpace(vasp.Protocol), "eaif")
}

func resolveVASPVersion(vasp *db.MM7VASP, fallback string) string {
	if vasp != nil && strings.TrimSpace(vasp.Version) != "" {
		if isEAIFVASP(vasp) {
			return resolveEAIFVersion(vasp.Version)
		}
		return resolveMM7Version(vasp.Version)
	}
	if vasp != nil && isEAIFVASP(vasp) {
		return resolveEAIFVersion(fallback)
	}
	return resolveMM7Version(fallback)
}

func recipientForMessage(msg *message.Message) Recipient {
	if msg == nil || len(msg.To) == 0 {
		return Recipient{}
	}
	return Recipient{Number: msg.To[0]}
}

func recipientsForMessage(msg *message.Message) Recipients {
	out := Recipients{To: make([]Recipient, 0, len(msg.To))}
	for _, to := range msg.To {
		out.To = append(out.To, Recipient{Number: to})
	}
	return out
}

func attachmentsForMessage(contentID string, msg *message.Message) []outboundAttachment {
	if msg == nil {
		return nil
	}
	attachments := make([]outboundAttachment, 0, len(msg.Parts))
	for idx, part := range msg.Parts {
		id := normalizeCID(part.ContentID)
		if id == "" && idx == 0 {
			id = contentID
		}
		if id == "" {
			id = fmt.Sprintf("part-%d", idx+1)
		}
		attachments = append(attachments, outboundAttachment{
			ContentID:   id,
			ContentType: part.ContentType,
			Data:        append([]byte(nil), part.Data...),
		})
	}
	return attachments
}

func encodeEAIFDeliver(msg *message.Message, version string) ([]byte, http.Header, error) {
	body, err := encodeEAIFPDU(msg)
	if err != nil {
		return nil, nil, err
	}
	headers := http.Header{}
	headers.Set("X-NOKIA-MMSC-Version", resolveEAIFVersion(version))
	headers.Set("X-NOKIA-MMSC-Message-Type", "MultiMediaMessage")
	if msg != nil {
		headers.Set("X-NOKIA-MMSC-From", msg.From)
		headers.Set("X-NOKIA-MMSC-To", strings.Join(msg.To, ","))
		if msg.ID != "" {
			headers.Set("X-NOKIA-MMSC-Message-Id", msg.ID)
		}
	}
	return body, headers, nil
}

func encodeEAIFReport(msg *message.Message, operation string, version string) ([]byte, http.Header, error) {
	body, err := encodeEAIFPDU(msg)
	if err != nil {
		return nil, nil, err
	}
	headers := http.Header{}
	headers.Set("X-NOKIA-MMSC-Version", resolveEAIFVersion(version))
	switch operation {
	case "delivery-report":
		headers.Set("X-NOKIA-MMSC-Message-Type", "DeliveryReport")
	case "read-reply":
		headers.Set("X-NOKIA-MMSC-Message-Type", "ReadReply")
	default:
		return nil, nil, fmt.Errorf("unsupported eaif operation %q", operation)
	}
	if msg != nil {
		headers.Set("X-NOKIA-MMSC-From", msg.From)
		headers.Set("X-NOKIA-MMSC-To", strings.Join(msg.To, ","))
		if msg.ID != "" {
			headers.Set("X-NOKIA-MMSC-Message-Id", msg.ID)
		}
	}
	return body, headers, nil
}

func encodeEAIFPDU(msg *message.Message) ([]byte, error) {
	if msg == nil {
		return nil, fmt.Errorf("message is required")
	}
	pdu := mmspdu.NewRetrieveConf(msg.TransactionID, toPDUParts(msg.Parts))
	pdu.Headers[mmspdu.FieldFrom] = mmspdu.NewFromValue(msg.From)
	if len(msg.To) > 0 {
		pdu.Headers[mmspdu.FieldTo] = mmspdu.NewAddressValue(mmspdu.FieldTo, msg.To[0])
	}
	if msg.Subject != "" {
		pdu.Headers[mmspdu.FieldSubject] = mmspdu.NewEncodedStringValue(mmspdu.FieldSubject, msg.Subject)
	}
	if msg.ID != "" {
		pdu.Headers[mmspdu.FieldMessageID] = mmspdu.NewTextValue(mmspdu.FieldMessageID, msg.ID)
	}
	return mmspdu.Encode(pdu)
}

func mm7StatusForMessage(status message.Status) string {
	switch status {
	case message.StatusDelivered:
		return "Retrieved"
	case message.StatusRejected:
		return "Rejected"
	case message.StatusExpired:
		return "Expired"
	case message.StatusUnreachable:
		return "Unrecognised"
	default:
		return "Forwarded"
	}
}

func statusTextForMessage(status message.Status) string {
	switch status {
	case message.StatusDelivered:
		return "Message delivered"
	case message.StatusRejected:
		return "Message rejected by recipient"
	case message.StatusExpired:
		return "Message expired"
	case message.StatusUnreachable:
		return "Recipient unreachable"
	default:
		return "Message forwarded"
	}
}
