package smpp

import "context"

func (c *Client) Probe(ctx context.Context) error {
	return c.EnquireLink(ctx)
}
