package service

import (
	"errors"

	"gopkg.in/dedis/cothority.v2"
	"gopkg.in/dedis/onet.v2"
	"gopkg.in/dedis/onet.v2/log"
)

// Client is a structure to communicate with the CoSi
// service
type Client struct {
	*onet.Client
}

// NewClient instantiates a new ftcosi.Client
func NewClient() *Client {
	return &Client{Client: onet.NewClient(cothority.Suite, ServiceName)}
}

// SignatureRequest sends a CoSi sign request to the Cothority defined by the given
// Roster
func (c *Client) SignatureRequest(r *onet.Roster, msg []byte) (*SignatureResponse, error) {
	serviceReq := &SignatureRequest{
		Roster:  r,
		Message: msg,
	}
	if len(r.List) == 0 {
		return nil, errors.New("Got an empty roster-list")
	}
	dst := r.List[0]
	log.Lvl4("Sending message to", dst)
	reply := &SignatureResponse{}
	err := c.SendProtobuf(dst, serviceReq, reply)
	if err != nil {
		return nil, err
	}
	return reply, nil
}
