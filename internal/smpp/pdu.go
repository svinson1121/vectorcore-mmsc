package smpp

const (
	CmdBindReceiver        uint32 = 0x00000001
	CmdBindTransmitter     uint32 = 0x00000002
	CmdSubmitSM            uint32 = 0x00000004
	CmdDeliverSM           uint32 = 0x00000005
	CmdUnbind              uint32 = 0x00000006
	CmdBindTransceiver     uint32 = 0x00000009
	CmdEnquireLink         uint32 = 0x00000015
	CmdSubmitSMResp        uint32 = 0x80000004
	CmdDeliverSMResp       uint32 = 0x80000005
	CmdBindReceiverResp    uint32 = 0x80000001
	CmdBindTransmitterResp uint32 = 0x80000002
	CmdBindTransceiverResp uint32 = 0x80000009
	CmdEnquireLinkResp     uint32 = 0x80000015
	CmdUnbindResp          uint32 = 0x80000006
	CmdGenericNack         uint32 = 0x80000000
)

const (
	ESMEROK        uint32 = 0x00000000
	ESMERINVBNDSTS uint32 = 0x00000004
	ESMERSYSERR    uint32 = 0x00000008
	ESMERINVDSTADR uint32 = 0x0000000B
	ESMERINVSYSID  uint32 = 0x0000000E
	ESMERINVPASWD  uint32 = 0x0000000F
)

const (
	ESMClassUDHI = 0x40
)

type PDU struct {
	CommandLength  uint32
	CommandID      uint32
	CommandStatus  uint32
	SequenceNumber uint32

	SystemID         string
	Password         string
	SystemType       string
	InterfaceVersion byte
	AddrTON          byte
	AddrNPI          byte
	AddressRange     string

	ServiceType          string
	SourceAddrTON        byte
	SourceAddrNPI        byte
	SourceAddr           string
	DestAddrTON          byte
	DestAddrNPI          byte
	DestinationAddr      string
	ESMClass             byte
	ProtocolID           byte
	PriorityFlag         byte
	ScheduleDeliveryTime string
	ValidityPeriod       string
	RegisteredDelivery   byte
	ReplaceIfPresentFlag byte
	DataCoding           byte
	SMDefaultMsgID       byte
	SMLength             byte
	ShortMessage         []byte

	MessageID string
}
