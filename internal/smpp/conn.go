package smpp

import (
	"errors"
	"net"
	"sync/atomic"
)

type Conn struct {
	conn   net.Conn
	seq    uint32
	writes chan writeRequest
	closed chan struct{}
}

type writeRequest struct {
	data []byte
	done chan error
}

func NewConn(conn net.Conn) *Conn {
	c := &Conn{
		conn:   conn,
		writes: make(chan writeRequest),
		closed: make(chan struct{}),
	}
	go c.writeLoop()
	return c
}

func (c *Conn) ReadPDU() (*PDU, error) {
	return Decode(c.conn)
}

func (c *Conn) WritePDU(pdu *PDU) error {
	data, err := Encode(pdu)
	if err != nil {
		return err
	}
	req := writeRequest{
		data: data,
		done: make(chan error, 1),
	}
	select {
	case <-c.closed:
		return errors.New("smpp: connection closed")
	case c.writes <- req:
	}
	return <-req.done
}

func (c *Conn) NextSeq() uint32 {
	return atomic.AddUint32(&c.seq, 1)
}

func (c *Conn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return c.conn.Close()
}

func (c *Conn) writeLoop() {
	for {
		select {
		case <-c.closed:
			return
		case req := <-c.writes:
			_, err := c.conn.Write(req.data)
			req.done <- err
			close(req.done)
			if err != nil {
				return
			}
		}
	}
}
