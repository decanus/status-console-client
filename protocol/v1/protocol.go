package protocol

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"time"
)

// Protocol is an interface defining basic methods to receive and send messages.
type Protocol interface {
	// Subscribe listens to new messages.
	Subscribe(ctx context.Context, messages chan<- *Message, options SubscribeOptions) (*Subscription, error)

	// Send sends a message to the network.
	// Identity is required as the protocol requires
	// all messages to be signed.
	Send(ctx context.Context, data []byte, options SendOptions) ([]byte, error)

	// Request retrieves historic messages.
	Request(ctx context.Context, params RequestOptions) error
}

// Message contains a decoded message payload
// and some additional fields that we learnt
// about the message.
type Message struct {
	Decoded   StatusMessage    `json:"message"`
	SigPubKey *ecdsa.PublicKey `json:"-"`
	Hash      []byte           `json:"hash"`
}

// ChatOptions are chat specific options, usually related to the recipient/destination.
type ChatOptions struct {
	ChatName  string           // for public chats
	Recipient *ecdsa.PublicKey // for private chats
}

// RequestOptions is a list of params required
// to request for historic messages.
type RequestOptions struct {
	ChatOptions
	Limit int
	From  int64
	To    int64
}

// Validate verifies that the given request options are valid.
func (o RequestOptions) Validate() error {
	if o == (RequestOptions{}) {
		return errors.New("empty options")
	}
	if o.ChatName == "" && o.Recipient == nil {
		return errors.New("field ChatName or Recipient is required")
	}
	if o.ChatName != "" && o.Recipient != nil {
		return errors.New("field ChatName and Recipient both set")
	}
	return nil
}

// DefaultRequestOptions returns default options returning messages
// from the last 24 hours.
func DefaultRequestOptions() RequestOptions {
	return RequestOptions{
		From:  time.Now().Add(-24 * time.Hour).Unix(),
		To:    time.Now().Unix(),
		Limit: 1000,
	}
}

// SubscribeOptions are options for Chat.Subscribe method.
type SubscribeOptions struct {
	ChatOptions
}

// Validate vierifies that the given options are valid.
func (o SubscribeOptions) Validate() error {
	if o == (SubscribeOptions{}) {
		return errors.New("empty options")
	}
	if o.Recipient != nil && o.ChatName != "" {
		return errors.New("fields Identity and ChatName both set")
	}
	return nil
}

// SendOptions are options for Chat.Send.
type SendOptions struct {
	ChatOptions
}

// Validate verifies that the given options are valid.
func (o SendOptions) Validate() error {
	if o.ChatName == "" && o.Recipient == nil {
		return errors.New("field ChatName or Recipient is required")
	}
	if o.ChatName != "" && o.Recipient != nil {
		return errors.New("fields ChatName and Recipient both set")
	}
	return nil
}