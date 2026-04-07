package message

type Status int16

const (
	StatusQueued Status = iota
	StatusDelivering
	StatusDelivered
	StatusExpired
	StatusRejected
	StatusForwarded
	StatusUnreachable
)

type Direction int16

const (
	DirectionMO Direction = iota
	DirectionMT
)

type Interface int16

const (
	InterfaceMM1 Interface = iota
	InterfaceMM4
	InterfaceMM3
	InterfaceMM7
)

type MessageClass int16

const (
	ClassPersonal MessageClass = iota
	ClassAdvertisement
	ClassInformational
	ClassAuto
)

type Priority int16

const (
	PriorityLow Priority = iota
	PriorityNormal
	PriorityHigh
)
