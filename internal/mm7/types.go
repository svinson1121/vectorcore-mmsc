package mm7

import "encoding/xml"

const (
	soapEnvNamespace = "http://schemas.xmlsoap.org/soap/envelope/"
	defaultNamespace = "http://www.3gpp.org/ftp/Specs/archive/23_series/23.140/schema/REL-5-MM7-1-0"
	defaultMM7Ver    = "5.3.0"
	statusSuccess    = "1000"
	statusClientErr  = "2000"
	statusAuthErr    = "2004"
	statusServerErr  = "5000"
)

type Envelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Header  Header   `xml:"Header"`
	Body    Body     `xml:"Body"`
}

type Header struct {
	TransactionID TransactionID `xml:"TransactionID"`
}

type TransactionID struct {
	MustUnderstand string `xml:"mustUnderstand,attr,omitempty"`
	Value          string `xml:",chardata"`
}

type Body struct {
	SubmitReq         *SubmitReq         `xml:"SubmitReq,omitempty"`
	SubmitRsp         *SubmitRsp         `xml:"SubmitRsp,omitempty"`
	CancelReq         *CancelReq         `xml:"CancelReq,omitempty"`
	CancelRsp         *CancelRsp         `xml:"CancelRsp,omitempty"`
	ReplaceReq        *ReplaceReq        `xml:"ReplaceReq,omitempty"`
	ReplaceRsp        *ReplaceRsp        `xml:"ReplaceRsp,omitempty"`
	DeliverReq        *DeliverReq        `xml:"DeliverReq,omitempty"`
	DeliverRsp        *DeliverRsp        `xml:"DeliverRsp,omitempty"`
	DeliveryReportReq *DeliveryReportReq `xml:"DeliveryReportReq,omitempty"`
	DeliveryReportRsp *DeliveryReportRsp `xml:"DeliveryReportRsp,omitempty"`
	ReadReplyReq      *ReadReplyReq      `xml:"ReadReplyReq,omitempty"`
	ReadReplyRsp      *ReadReplyRsp      `xml:"ReadReplyRsp,omitempty"`
	Fault             *SOAPFault         `xml:"Fault,omitempty"`
}

type SubmitReq struct {
	MM7Version           string               `xml:"MM7Version"`
	SenderIdentification SenderIdentification `xml:"SenderIdentification"`
	Recipients           Recipients           `xml:"Recipients"`
	Subject              string               `xml:"Subject,omitempty"`
	Content              ContentRef           `xml:"Content"`
}

type SenderIdentification struct {
	VASPID        string        `xml:"VASPID,omitempty"`
	VASID         string        `xml:"VASID,omitempty"`
	SenderAddress SenderAddress `xml:"SenderAddress"`
}

type SenderAddress struct {
	Number    string `xml:"Number,omitempty"`
	ShortCode string `xml:"ShortCode,omitempty"`
}

type Recipients struct {
	To []Recipient `xml:"To"`
}

type Recipient struct {
	Number string `xml:"Number,omitempty"`
	Email  string `xml:"RFC2822Address,omitempty"`
}

type ContentRef struct {
	Href string `xml:"href,attr"`
}

type SubmitRsp struct {
	MM7Version string    `xml:"MM7Version"`
	Status     MM7Status `xml:"Status"`
	MessageID  string    `xml:"MessageID,omitempty"`
	Details    string    `xml:"Details,omitempty"`
}

type CancelReq struct {
	MM7Version string `xml:"MM7Version"`
	VASPID     string `xml:"VASPID,omitempty"`
	MessageID  string `xml:"MessageID"`
}

type CancelRsp struct {
	MM7Version string    `xml:"MM7Version"`
	Status     MM7Status `xml:"Status"`
	MessageID  string    `xml:"MessageID,omitempty"`
}

type ReplaceReq struct {
	MM7Version string     `xml:"MM7Version"`
	VASPID     string     `xml:"VASPID,omitempty"`
	MessageID  string     `xml:"MessageID"`
	Subject    string     `xml:"Subject,omitempty"`
	Content    ContentRef `xml:"Content"`
}

type ReplaceRsp struct {
	MM7Version string    `xml:"MM7Version"`
	Status     MM7Status `xml:"Status"`
	MessageID  string    `xml:"MessageID,omitempty"`
}

type DeliverReq struct {
	MM7Version string        `xml:"MM7Version"`
	MessageID  string        `xml:"MessageID"`
	Sender     SenderAddress `xml:"SenderAddress"`
	Recipients Recipients    `xml:"Recipients"`
	Subject    string        `xml:"Subject,omitempty"`
	Content    ContentRef    `xml:"Content"`
}

type DeliverRsp struct {
	MM7Version string    `xml:"MM7Version"`
	Status     MM7Status `xml:"Status"`
	MessageID  string    `xml:"MessageID,omitempty"`
}

type DeliveryReportReq struct {
	MM7Version string    `xml:"MM7Version"`
	MessageID  string    `xml:"MessageID"`
	Recipient  Recipient `xml:"Recipient"`
	MMStatus   string    `xml:"MMStatus"`
	StatusText string    `xml:"StatusText,omitempty"`
}

type DeliveryReportRsp struct {
	MM7Version string    `xml:"MM7Version"`
	Status     MM7Status `xml:"Status"`
	MessageID  string    `xml:"MessageID,omitempty"`
}

type ReadReplyReq struct {
	MM7Version string    `xml:"MM7Version"`
	MessageID  string    `xml:"MessageID"`
	Recipient  Recipient `xml:"Recipient"`
	MMStatus   string    `xml:"MMStatus"`
}

type ReadReplyRsp struct {
	MM7Version string    `xml:"MM7Version"`
	Status     MM7Status `xml:"Status"`
	MessageID  string    `xml:"MessageID,omitempty"`
}

type MM7Status struct {
	StatusCode string `xml:"StatusCode"`
	StatusText string `xml:"StatusText"`
}

type SOAPFault struct {
	FaultCode   string `xml:"faultcode"`
	FaultString string `xml:"faultstring"`
}
