package mm7

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
)

type ParsedRequest struct {
	Envelope    Envelope
	Attachments map[string]Attachment
}

type Attachment struct {
	ContentID   string
	ContentType string
	Data        []byte
}

func ParseRequest(r *http.Request) (*ParsedRequest, error) {
	contentType := r.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil && contentType != "" {
		return nil, fmt.Errorf("parse content type: %w", err)
	}

	var (
		envelopeBytes []byte
		attachments   = map[string]Attachment{}
	)

	switch {
	case strings.HasPrefix(strings.ToLower(mediaType), "multipart/"):
		reader := multipart.NewReader(r.Body, params["boundary"])
		first := true
		for {
			part, err := reader.NextPart()
			if err != nil {
				if err == io.EOF {
					break
				}
				return nil, fmt.Errorf("read multipart request: %w", err)
			}
			payload, err := io.ReadAll(part)
			if err != nil {
				return nil, fmt.Errorf("read multipart part: %w", err)
			}
			if first {
				envelopeBytes = payload
				first = false
				continue
			}
			contentID := normalizeCID(part.Header.Get("Content-ID"))
			attachments[contentID] = Attachment{
				ContentID:   contentID,
				ContentType: part.Header.Get("Content-Type"),
				Data:        payload,
			}
		}
	default:
		envelopeBytes, err = io.ReadAll(r.Body)
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
	}

	var env Envelope
	decoder := xml.NewDecoder(bytes.NewReader(envelopeBytes))
	if err := decoder.Decode(&env); err != nil {
		return nil, fmt.Errorf("decode soap envelope: %w", err)
	}
	return &ParsedRequest{
		Envelope:    env,
		Attachments: attachments,
	}, nil
}

func WriteSubmitRsp(w http.ResponseWriter, transactionID, messageID string) error {
	return WriteOperationRsp(w, http.StatusOK, transactionID, "submit", messageID, defaultMM7Ver, defaultNamespace)
}

func WriteOperationRsp(w http.ResponseWriter, httpStatus int, transactionID, operation, messageID, version, namespace string) error {
	status := MM7Status{
		StatusCode: statusSuccess,
		StatusText: "Success",
	}
	version = resolveMM7Version(version)
	namespace = resolveMM7Namespace(namespace)
	env := Envelope{
		Header: Header{
			TransactionID: TransactionID{
				MustUnderstand: "1",
				Value:          transactionID,
			},
		},
	}
	switch operation {
	case "submit":
		env.Body.SubmitRsp = &SubmitRsp{MM7Version: version, Status: status, MessageID: messageID}
	case "cancel":
		env.Body.CancelRsp = &CancelRsp{MM7Version: version, Status: status, MessageID: messageID}
	case "replace":
		env.Body.ReplaceRsp = &ReplaceRsp{MM7Version: version, Status: status, MessageID: messageID}
	case "deliver":
		env.Body.DeliverRsp = &DeliverRsp{MM7Version: version, Status: status, MessageID: messageID}
	case "delivery-report":
		env.Body.DeliveryReportRsp = &DeliveryReportRsp{MM7Version: version, Status: status, MessageID: messageID}
	case "read-reply":
		env.Body.ReadReplyRsp = &ReadReplyRsp{MM7Version: version, Status: status, MessageID: messageID}
	default:
		return fmt.Errorf("unsupported mm7 response operation %q", operation)
	}
	return writeSOAPEnvelope(w, httpStatus, env, namespace)
}

func WriteFault(w http.ResponseWriter, httpStatus int, transactionID, statusCode, statusText, faultCode string) error {
	return WriteFaultWithVersion(w, httpStatus, transactionID, statusCode, statusText, faultCode, defaultNamespace)
}

func WriteFaultWithVersion(w http.ResponseWriter, httpStatus int, transactionID, statusCode, statusText, faultCode, namespace string) error {
	env := Envelope{
		Header: Header{
			TransactionID: TransactionID{
				MustUnderstand: "1",
				Value:          transactionID,
			},
		},
		Body: Body{
			Fault: &SOAPFault{
				FaultCode:   faultCode,
				FaultString: statusText,
			},
		},
	}
	w.Header().Set("X-MM7-Status-Code", statusCode)
	return writeSOAPEnvelope(w, httpStatus, env, resolveMM7Namespace(namespace))
}

func writeSOAPEnvelope(w http.ResponseWriter, httpStatus int, env Envelope, namespace string) error {
	var body bytes.Buffer
	body.WriteString(xml.Header)
	encoder := xml.NewEncoder(&body)
	start := xml.StartElement{
		Name: xml.Name{Local: "soapenv:Envelope"},
		Attr: []xml.Attr{
			{Name: xml.Name{Local: "xmlns:soapenv"}, Value: soapEnvNamespace},
			{Name: xml.Name{Local: "xmlns:mm7"}, Value: namespace},
		},
	}
	if err := encoder.EncodeToken(start); err != nil {
		return err
	}
	if err := encodeHeader(encoder, env.Header); err != nil {
		return err
	}
	if err := encodeBody(encoder, env.Body); err != nil {
		return err
	}
	if err := encoder.EncodeToken(start.End()); err != nil {
		return err
	}
	if err := encoder.Flush(); err != nil {
		return err
	}

	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(httpStatus)
	_, err := w.Write(body.Bytes())
	return err
}

func encodeHeader(enc *xml.Encoder, header Header) error {
	start := xml.StartElement{Name: xml.Name{Local: "soapenv:Header"}}
	if err := enc.EncodeToken(start); err != nil {
		return err
	}
	if header.TransactionID.Value != "" {
		txnStart := xml.StartElement{
			Name: xml.Name{Local: "mm7:TransactionID"},
			Attr: []xml.Attr{{Name: xml.Name{Local: "soapenv:mustUnderstand"}, Value: "1"}},
		}
		if err := enc.EncodeElement(header.TransactionID.Value, txnStart); err != nil {
			return err
		}
	}
	return enc.EncodeToken(start.End())
}

func encodeBody(enc *xml.Encoder, body Body) error {
	start := xml.StartElement{Name: xml.Name{Local: "soapenv:Body"}}
	if err := enc.EncodeToken(start); err != nil {
		return err
	}
	switch {
	case body.SubmitRsp != nil:
		if err := enc.EncodeElement(body.SubmitRsp, xml.StartElement{Name: xml.Name{Local: "mm7:SubmitRsp"}}); err != nil {
			return err
		}
	case body.CancelRsp != nil:
		if err := enc.EncodeElement(body.CancelRsp, xml.StartElement{Name: xml.Name{Local: "mm7:CancelRsp"}}); err != nil {
			return err
		}
	case body.ReplaceRsp != nil:
		if err := enc.EncodeElement(body.ReplaceRsp, xml.StartElement{Name: xml.Name{Local: "mm7:ReplaceRsp"}}); err != nil {
			return err
		}
	case body.DeliverRsp != nil:
		if err := enc.EncodeElement(body.DeliverRsp, xml.StartElement{Name: xml.Name{Local: "mm7:DeliverRsp"}}); err != nil {
			return err
		}
	case body.DeliveryReportRsp != nil:
		if err := enc.EncodeElement(body.DeliveryReportRsp, xml.StartElement{Name: xml.Name{Local: "mm7:DeliveryReportRsp"}}); err != nil {
			return err
		}
	case body.ReadReplyRsp != nil:
		if err := enc.EncodeElement(body.ReadReplyRsp, xml.StartElement{Name: xml.Name{Local: "mm7:ReadReplyRsp"}}); err != nil {
			return err
		}
	case body.Fault != nil:
		if err := enc.EncodeElement(body.Fault, xml.StartElement{Name: xml.Name{Local: "soapenv:Fault"}}); err != nil {
			return err
		}
	}
	return enc.EncodeToken(start.End())
}

func normalizeCID(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "<")
	value = strings.TrimSuffix(value, ">")
	return value
}

func resolveMM7Version(version string) string {
	if version == "" {
		return defaultMM7Ver
	}
	return version
}

func resolveMM7Namespace(namespace string) string {
	if namespace == "" {
		return defaultNamespace
	}
	return namespace
}
