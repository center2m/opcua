// Copyright 2018-2019 opcua authors. All rights reserved.
// Use of this source code is governed by a MIT-style license that can be
// found in the LICENSE file.

package opcua

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/center2m/opcua/id"
	"github.com/center2m/opcua/ua"
	"github.com/center2m/opcua/uacp"
	"github.com/center2m/opcua/uasc"
)

// GetEndpoints returns the available endpoint descriptions for the server.
func GetEndpoints(endpoint string) ([]*ua.EndpointDescription, error) {
	c := NewClient(endpoint)
	if err := c.Dial(); err != nil {
		return nil, err
	}
	defer c.Close()
	res, err := c.GetEndpoints()
	if err != nil {
		return nil, err
	}
	return res.Endpoints, nil
}

// Client is a high-level client for an OPC/UA server.
// It establishes a secure channel and a session.
type Client struct {
	// endpointURL is the endpoint URL the client connects to.
	endpointURL string

	// cfg is the configuration for the secure channel.
	cfg *uasc.Config

	// sessionCfg is the configuration for the session.
	sessionCfg *uasc.SessionConfig

	// sechan is the open secure channel.
	sechan *uasc.SecureChannel

	// session is the active session.
	session atomic.Value // *Session

	// once initializes session
	once sync.Once
}

// NewClient creates a new Client.
//
// When no options are provided the new client is created from
// DefaultClientConfig() and DefaultSessionConfig(). If no authentication method
// is confiugred a UserIdentityToken for anonymous authentication will be set.
// See #Client.CreateSession for details.
//
// To modify configuration you can provide any number of Options as opts. See
// #Option for details.
//
// https://godoc.org/github.com/center2m/opcua#Option
func NewClient(endpoint string, opts ...Option) *Client {
	c := &Client{
		endpointURL: endpoint,
		cfg:         DefaultClientConfig(),
		sessionCfg:  DefaultSessionConfig(),
	}
	for _, opt := range opts {
		opt(c.cfg, c.sessionCfg)
	}

	return c
}

// Connect establishes a secure channel and creates a new session.
func (c *Client) Connect() (err error) {
	if c.sechan != nil {
		return fmt.Errorf("already connected")
	}
	if err := c.Dial(); err != nil {
		return err
	}
	s, err := c.CreateSession(c.sessionCfg)
	if err != nil {
		_ = c.Close()
		return err
	}
	if err := c.ActivateSession(s); err != nil {
		_ = c.Close()
		return err
	}
	return nil
}

// Dial establishes a secure channel.
func (c *Client) Dial() error {
	c.once.Do(func() { c.session.Store((*Session)(nil)) })
	if c.sechan != nil {
		return fmt.Errorf("secure channel already connected")
	}
	conn, err := uacp.Dial(context.Background(), c.endpointURL)
	if err != nil {
		return err
	}
	sechan, err := uasc.NewSecureChannel(c.endpointURL, conn, c.cfg)
	if err != nil {
		_ = conn.Close()
		return err
	}
	if err := sechan.Open(); err != nil {
		_ = conn.Close()
		return err
	}
	c.sechan = sechan
	return nil
}

// Close closes the session and the secure channel.
func (c *Client) Close() error {
	// try to close the session but ignore any error
	// so that we close the underlying channel and connection.
	_ = c.CloseSession()
	return c.sechan.Close()
}

// Session returns the active session.
func (c *Client) Session() *Session {
	return c.session.Load().(*Session)
}

// Session is a OPC/UA session as described in Part 4, 5.6.
type Session struct {
	cfg *uasc.SessionConfig

	// resp is the response to the CreateSession request which contains all
	// necessary parameters to activate the session.
	resp *ua.CreateSessionResponse

	// serverCertificate is the certificate used to generate the signatures for
	// the ActivateSessionRequest methods
	serverCertificate []byte

	// serverNonce is the secret nonce received from the server during Create and Activate
	// Session respones. Used to generate the signatures for the ActivateSessionRequest
	// and User Authorization
	serverNonce []byte
}

// CreateSession creates a new session which is not yet activated and not
// associated with the client. Call ActivateSession to both activate and
// associate the session with the client.
//
// If no UserIdentityToken is given explicitly before calling CreateSesion,
// it automatically sets anonymous identity token with the same PolicyID
// that the server sent in Create Session Response. The default PolicyID
// "Anonymous" wii be set if it's missing in response.
//
// See Part 4, 5.6.2
func (c *Client) CreateSession(cfg *uasc.SessionConfig) (*Session, error) {
	if c.sechan == nil {
		return nil, fmt.Errorf("secure channel not connected")
	}

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}

	req := &ua.CreateSessionRequest{
		ClientDescription:       cfg.ClientDescription,
		EndpointURL:             c.endpointURL,
		SessionName:             fmt.Sprintf("gopcua-%d", time.Now().UnixNano()),
		ClientNonce:             nonce,
		ClientCertificate:       c.cfg.Certificate,
		RequestedSessionTimeout: float64(cfg.SessionTimeout / time.Millisecond),
	}

	var s *Session
	// for the CreateSessionRequest the authToken is always nil.
	// use c.sechan.Send() to enforce this.
	err := c.sechan.Send(req, nil, func(v interface{}) error {
		resp, ok := v.(*ua.CreateSessionResponse)
		if !ok {
			return fmt.Errorf("invalid response. Got %T, want CreateSessionResponse", v)
		}

		err := c.sechan.VerifySessionSignature(resp.ServerCertificate, nonce, resp.ServerSignature.Signature)
		if err != nil {
			log.Printf("error verifying session signature: %s", err)
			return nil
		}

		// Ensure we have a valid identity token that the server will accept before trying to activate a session
		if c.sessionCfg.UserIdentityToken == nil {
			opt := AuthAnonymous()
			opt(c.cfg, c.sessionCfg)

			p := anonymousPolicyID(resp.ServerEndpoints)
			opt = AuthPolicyID(p)
			opt(c.cfg, c.sessionCfg)
		}

		s = &Session{
			cfg:               cfg,
			resp:              resp,
			serverNonce:       resp.ServerNonce,
			serverCertificate: resp.ServerCertificate,
		}

		return nil
	})
	return s, err
}

const defaultAnonymousPolicyID = "Anonymous"

func anonymousPolicyID(endpoints []*ua.EndpointDescription) string {
	for _, e := range endpoints {
		if e.SecurityMode != ua.MessageSecurityModeNone || e.SecurityPolicyURI != ua.SecurityPolicyURINone {
			continue
		}

		for _, t := range e.UserIdentityTokens {
			if t.TokenType == ua.UserTokenTypeAnonymous {
				return t.PolicyID
			}
		}
	}

	return defaultAnonymousPolicyID
}

// ActivateSession activates the session and associates it with the client. If
// the client already has a session it will be closed. To retain the current
// session call DetachSession.
//
// See Part 4, 5.6.3
func (c *Client) ActivateSession(s *Session) error {
	sig, sigAlg, err := c.sechan.NewSessionSignature(s.serverCertificate, s.serverNonce)
	if err != nil {
		log.Printf("error creating session signature: %s", err)
		return nil
	}

	switch tok := s.cfg.UserIdentityToken.(type) {
	case *ua.AnonymousIdentityToken:
		// nothing to do

	case *ua.UserNameIdentityToken:
		pass, passAlg, err := c.sechan.EncryptUserPassword(s.cfg.AuthPolicyURI, s.cfg.AuthPassword, s.serverCertificate, s.serverNonce)
		if err != nil {
			log.Printf("error encrypting user password: %s", err)
			return err
		}
		tok.Password = pass
		tok.EncryptionAlgorithm = passAlg

	case *ua.X509IdentityToken:
		tokSig, tokSigAlg, err := c.sechan.NewUserTokenSignature(s.cfg.AuthPolicyURI, s.serverCertificate, s.serverNonce)
		if err != nil {
			log.Printf("error creating session signature: %s", err)
			return err
		}
		s.cfg.UserTokenSignature = &ua.SignatureData{
			Algorithm: tokSigAlg,
			Signature: tokSig,
		}

	case *ua.IssuedIdentityToken:
		tok.EncryptionAlgorithm = ""
	}

	req := &ua.ActivateSessionRequest{
		ClientSignature: &ua.SignatureData{
			Algorithm: sigAlg,
			Signature: sig,
		},
		ClientSoftwareCertificates: nil,
		LocaleIDs:                  s.cfg.LocaleIDs,
		UserIdentityToken:          ua.NewExtensionObject(s.cfg.UserIdentityToken),
		UserTokenSignature:         s.cfg.UserTokenSignature,
	}
	return c.sechan.Send(req, s.resp.AuthenticationToken, func(v interface{}) error {
		resp, ok := v.(*ua.ActivateSessionResponse)
		if !ok {
			return fmt.Errorf("invalid response. Got %T, want ActivateSessionResponse", v)
		}

		// Save the nonce for the next request
		s.serverNonce = resp.ServerNonce

		if err := c.CloseSession(); err != nil {
			// try to close the newly created session but report
			// only the initial error.
			_ = c.closeSession(s)
			return err
		}
		c.session.Store(s)
		return nil
	})
}

// CloseSession closes the current session.
//
// See Part 4, 5.6.4
func (c *Client) CloseSession() error {
	if err := c.closeSession(c.Session()); err != nil {
		return err
	}
	c.session.Store((*Session)(nil))
	return nil
}

// closeSession closes the given session.
func (c *Client) closeSession(s *Session) error {
	if s == nil {
		return nil
	}
	req := &ua.CloseSessionRequest{DeleteSubscriptions: true}
	return c.Send(req, func(v interface{}) error {
		_, ok := v.(*ua.CloseSessionResponse)
		if !ok {
			return fmt.Errorf("invalid response. Got %T, want ActivateSessionResponse", v)
		}
		return nil
	})
}

// DetachSession removes the session from the client without closing it. The
// caller is responsible to close or re-activate the session. If the client
// does not have an active session the function returns no error.
func (c *Client) DetachSession() (*Session, error) {
	s := c.Session()
	c.session.Store((*Session)(nil))
	return s, nil
}

// Send sends the request via the secure channel and registers a handler for
// the response. If the client has an active session it injects the
// authenticaton token.
func (c *Client) Send(req interface{}, h func(interface{}) error) error {
	var authToken *ua.NodeID
	if s := c.Session(); s != nil {
		authToken = s.resp.AuthenticationToken
	}
	return c.sechan.Send(req, authToken, h)
}

// Node returns a node object which accesses its attributes
// through this client connection.
func (c *Client) Node(id *ua.NodeID) *Node {
	return &Node{ID: id, c: c}
}

func (c *Client) GetEndpoints() (*ua.GetEndpointsResponse, error) {
	req := &ua.GetEndpointsRequest{
		EndpointURL: c.endpointURL,
	}
	var res *ua.GetEndpointsResponse
	err := c.Send(req, func(v interface{}) error {
		r, ok := v.(*ua.GetEndpointsResponse)
		if !ok {
			return fmt.Errorf("invalid response: %T", v)
		}
		res = r
		return nil
	})
	return res, err
}

// Read executes a synchronous read request.
//
// By default, the function requests the value of the nodes
// in the default encoding of the server.
func (c *Client) Read(req *ua.ReadRequest) (*ua.ReadResponse, error) {
	// clone the request and the ReadValueIDs to set defaults without
	// manipulating them in-place.
	rvs := make([]*ua.ReadValueID, len(req.NodesToRead))
	for i, rv := range req.NodesToRead {
		rc := &ua.ReadValueID{}
		*rc = *rv
		if rc.AttributeID == 0 {
			rc.AttributeID = ua.AttributeIDValue
		}
		if rc.DataEncoding == nil {
			rc.DataEncoding = &ua.QualifiedName{}
		}
		rvs[i] = rc
	}
	req = &ua.ReadRequest{
		MaxAge:             req.MaxAge,
		TimestampsToReturn: req.TimestampsToReturn,
		NodesToRead:        rvs,
	}

	var res *ua.ReadResponse
	err := c.Send(req, func(v interface{}) error {
		r, ok := v.(*ua.ReadResponse)
		if !ok {
			return fmt.Errorf("invalid response: %T", v)
		}
		res = r
		return nil
	})
	return res, err
}

// Write executes a synchronous write request.
func (c *Client) Write(req *ua.WriteRequest) (res *ua.WriteResponse, err error) {
	err = c.Send(req, func(v interface{}) error {
		r, ok := v.(*ua.WriteResponse)
		if !ok {
			return fmt.Errorf("invalid response: %T", v)
		}
		res = r
		return nil
	})
	return
}

// Browse executes a synchronous browse request.
func (c *Client) Browse(req *ua.BrowseRequest) (*ua.BrowseResponse, error) {
	var res *ua.BrowseResponse
	err := c.Send(req, func(v interface{}) error {
		r, ok := v.(*ua.BrowseResponse)
		if !ok {
			return fmt.Errorf("invalid response: %T", v)
		}
		res = r
		return nil
	})
	return res, err
}

// BrowseNext executes a synchronous browse request.
func (c *Client) BrowseNext(req *ua.BrowseNextRequest) (*ua.BrowseNextResponse, error) {
	var res *ua.BrowseNextResponse
	err := c.Send(req, func(v interface{}) error {
		r, ok := v.(*ua.BrowseNextResponse)
		if !ok {
			return fmt.Errorf("invalid response: %T", v)
		}
		res = r
		return nil
	})
	return res, err
}

// todo(fs): this is not done yet since we need to be able to register
// todo(fs): monitored items.
type Subscription struct {
	res *ua.CreateSubscriptionResponse
}

// todo(fs): return subscription object with channel
func (c *Client) Subscribe(intv time.Duration) (*Subscription, error) {
	req := &ua.CreateSubscriptionRequest{
		RequestedPublishingInterval: float64(intv / time.Millisecond),
		RequestedLifetimeCount:      60,
		RequestedMaxKeepAliveCount:  20,
		PublishingEnabled:           true,
	}

	var res *ua.CreateSubscriptionResponse
	err := c.Send(req, func(v interface{}) error {
		r, ok := v.(*ua.CreateSubscriptionResponse)
		if !ok {
			return fmt.Errorf("invalid response: %T", v)
		}
		res = r
		return nil
	})
	return &Subscription{res}, err
}

type PublishNotificationData struct {
	SubscriptionID uint32
	Error          error
	Value          interface{}
}

func (c *Client) Publish(notif chan<- PublishNotificationData) {
	// Empty SubscriptionAcknowledgements for first PublishRequest
	var acks = make([]*ua.SubscriptionAcknowledgement, 0)

	for {
		req := &ua.PublishRequest{
			SubscriptionAcknowledgements: acks,
		}

		var res *ua.PublishResponse
		err := c.Send(req, func(v interface{}) error {
			r, ok := v.(*ua.PublishResponse)
			if !ok {
				return fmt.Errorf("invalid response: %T", v)
			}
			res = r
			return nil
		})
		if err != nil {
			notif <- PublishNotificationData{Error: err}
			continue
		}

		// Check for errors
		status := ua.StatusOK
		for _, res := range res.Results {
			if res != ua.StatusOK {
				status = res
				break
			}
		}

		if status != ua.StatusOK {
			notif <- PublishNotificationData{
				SubscriptionID: res.SubscriptionID,
				Error:          status,
			}
			continue
		}

		// Prepare SubscriptionAcknowledgement for next PublishRequest
		acks = make([]*ua.SubscriptionAcknowledgement, 0)
		for _, i := range res.AvailableSequenceNumbers {
			ack := &ua.SubscriptionAcknowledgement{
				SubscriptionID: res.SubscriptionID,
				SequenceNumber: i,
			}
			acks = append(acks, ack)
		}

		if res.NotificationMessage == nil {
			notif <- PublishNotificationData{
				SubscriptionID: res.SubscriptionID,
				Error:          fmt.Errorf("empty NotificationMessage"),
			}
			continue
		}

		// Part 4, 7.21 NotificationMessage
		for _, data := range res.NotificationMessage.NotificationData {
			// Part 4, 7.20 NotificationData parameters
			if data == nil || data.Value == nil {
				notif <- PublishNotificationData{
					SubscriptionID: res.SubscriptionID,
					Error:          fmt.Errorf("missing NotificationData parameter"),
				}
				continue
			}

			switch data.Value.(type) {
			// Part 4, 7.20.2 DataChangeNotification parameter
			// Part 4, 7.20.3 EventNotificationList parameter
			// Part 4, 7.20.4 StatusChangeNotification parameter
			case *ua.DataChangeNotification,
				*ua.EventNotificationList,
				*ua.StatusChangeNotification:
				notif <- PublishNotificationData{
					SubscriptionID: res.SubscriptionID,
					Value:          data.Value,
				}

			// Error
			default:
				notif <- PublishNotificationData{
					SubscriptionID: res.SubscriptionID,
					Error:          fmt.Errorf("unknown NotificationData parameter: %T", data.Value),
				}
			}
		}
	}
}

func (c *Client) CreateMonitoredItems(subID uint32, ts ua.TimestampsToReturn, items ...*ua.MonitoredItemCreateRequest) (*ua.CreateMonitoredItemsResponse, error) {
	if subID == 0 {
		return nil, fmt.Errorf("subscriptionID is 0")
	}

	// Part 4, 5.12.2.2 CreateMonitoredItems Service Parameters
	reqItems := &ua.CreateMonitoredItemsRequest{
		SubscriptionID:     subID,
		TimestampsToReturn: ts,
		ItemsToCreate:      items,
	}

	respItems := &ua.CreateMonitoredItemsResponse{}
	err := c.Send(reqItems, func(v interface{}) error {
		r, ok := v.(*ua.CreateMonitoredItemsResponse)
		if !ok {
			return fmt.Errorf("invalid response: %T", v)
		}
		respItems = r
		return nil
	})

	if err != nil {
		return nil, err
	}

	return respItems, nil

}

func (c *Client) HistoryReadRawModified(nodes []*ua.HistoryReadValueID, details *ua.ReadRawModifiedDetails) (*ua.HistoryReadResponse, error) {
	// Part 4, 5.10.3 HistoryRead
	req := &ua.HistoryReadRequest{
		TimestampsToReturn: ua.TimestampsToReturnBoth,
		NodesToRead:        nodes,
		// Part 11, 6.4 HistoryReadDetails parameters
		HistoryReadDetails: &ua.ExtensionObject{
			TypeID:       ua.NewFourByteExpandedNodeID(0, id.ReadRawModifiedDetails_Encoding_DefaultBinary),
			EncodingMask: ua.ExtensionObjectBinary,
			Value:        details,
		},
	}

	data := &ua.HistoryReadResponse{}
	err := c.Send(req, func(v interface{}) error {
		res, ok := v.(*ua.HistoryReadResponse)
		if !ok {
			return fmt.Errorf("cant parse response")
		}

		data = res
		return nil
	})

	return data, err
}
