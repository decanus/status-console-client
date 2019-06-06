package adapters

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/binary"
	"log"
	"sort"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/pkg/errors"
	"github.com/status-im/mvds"
	"github.com/status-im/status-console-client/protocol/v1"
	whisper "github.com/status-im/whisper/whisperv6"
)

// DataSyncClient is an adapter for MVDS
// that implements the Protocol interface.
type DataSyncClient struct {
	sync *mvds.Node
	t    *DataSyncWhisperTransport
}

func NewDataSyncClient(sync *mvds.Node, t *DataSyncWhisperTransport) *DataSyncClient {
	go sync.Run()

	return &DataSyncClient{
		sync: sync,
		t:    t,
	}
}

// Subscribe subscribes to a public chat using the Whisper service.
func (c *DataSyncClient) Subscribe(ctx context.Context, messages chan<- *protocol.Message, options protocol.SubscribeOptions) (*protocol.Subscription, error) {
	return c.t.subscribe(messages, options)
}

// Send appends a message to the data sync node for later sending.
func (c *DataSyncClient) Send(ctx context.Context, data []byte, options protocol.SendOptions) ([]byte, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}

	if options.ChatName == "" {
		return nil, errors.New("missing chat name")
	}

	topic, err := ToTopic(options.ChatName)
	if err != nil {
		return nil, err
	}

	gid := toGroupId(topic)

	c.peer(gid, options.Recipient)

	id, err := c.sync.AppendMessage(toGroupId(topic), data)
	if err != nil {
		return nil, err
	}

	return id[:], nil
}

func (*DataSyncClient) Request(ctx context.Context, params protocol.RequestOptions) error {
	return nil
}

func (c *DataSyncClient) peer(id mvds.GroupID, peer *ecdsa.PublicKey) {
	if peer == nil {
		return
	}

	p := mvds.PublicKeyToPeerID(*peer)

	if c.sync.IsPeerInGroup(id, p) {
		return
	}

	c.sync.AddPeer(id, p)
	c.sync.Share(id, p)
}

type DataSyncWhisperTransport struct {
	shh         *whisper.Whisper
	keysManager *whisperServiceKeysManager

	packets chan mvds.Packet
}

func NewDataSyncWhisperTransport(shh *whisper.Whisper, privateKey *ecdsa.PrivateKey) *DataSyncWhisperTransport {
	return &DataSyncWhisperTransport{
		shh: shh,
		keysManager: &whisperServiceKeysManager{
			shh:               shh,
			privateKey:        privateKey,
			passToSymKeyCache: make(map[string]string),
		},
		packets: make(chan mvds.Packet),
	}
}

func (t *DataSyncWhisperTransport) Watch() mvds.Packet {
	return <-t.packets
}

// Send sends a new message using the Whisper service.
func (t *DataSyncWhisperTransport) Send(group mvds.GroupID, _ mvds.PeerID, peer mvds.PeerID, payload mvds.Payload) error {
	data, err := proto.Marshal(&payload)
	if err != nil {
		return err
	}

	newMessage, err := newNewMessage(t.keysManager, data)
	if err != nil {
		return err
	}

	newMessage.Topic = toTopicType(group)

	// @todo set SymKeyID or PublicKey depending on chat type

	newMessage.PublicKey = peer[:]

	_, err = whisper.NewPublicWhisperAPI(t.shh).Post(context.Background(), newMessage.ToWhisper())
	return err
}

func (t *DataSyncWhisperTransport) subscribe(in chan<- *protocol.Message, options protocol.SubscribeOptions) (*protocol.Subscription, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}

	filter := newFilter(t.keysManager)
	if err := updateFilterFromSubscribeOptions(filter, options); err != nil {
		return nil, err
	}

	filterID, err := t.shh.Subscribe(filter.ToWhisper())
	if err != nil {
		return nil, err
	}

	subWhisper := newWhisperSubscription(t.shh, filterID)
	sub := protocol.NewSubscription()

	go func() {
		defer subWhisper.Unsubscribe() // nolint: errcheck

		tick := time.NewTicker(time.Second)
		defer tick.Stop()

		for {
			select {
			case <-tick.C:
				received, err := subWhisper.Messages()
				if err != nil {
					sub.Cancel(err)
					return
				}

				for _, item := range received {
					payload := t.handlePayload(item)
					if payload == nil {
						continue
					}

					t.packets <- mvds.Packet{
						Group:   toGroupId(item.Topic),
						Sender:  mvds.PublicKeyToPeerID(*item.Src),
						Payload: *payload,
					}

					messages := t.decodeMessages(*payload)
					for _, m := range messages {
						m.SigPubKey = item.Src
						in <- m
					}
				}
			case <-sub.Done():
				return
			}
		}
	}()

	return sub, nil
}

// @todo return error?
func (t *DataSyncWhisperTransport) handlePayload(received *whisper.ReceivedMessage) *mvds.Payload {
	payload := &mvds.Payload{}
	err := proto.Unmarshal(received.Payload, payload)
	if err != nil {
		log.Printf("failed to decode message %#+x: %v", received.EnvelopeHash.Bytes(), err)
		return nil // @todo
	}

	return payload
}

func (t *DataSyncWhisperTransport) decodeMessages(payload mvds.Payload) []*protocol.Message {
	messages := make([]*protocol.Message, 0)

	for _, message := range payload.Messages {
		decoded, err := protocol.DecodeMessage(message.Body)
		if err != nil {
			// @todo log or something?
			continue
		}

		decoded.ID = messageID(*message)

		messages = append(messages, &decoded)
	}

	sort.Slice(messages, func(i, j int) bool {
		return messages[i].Clock < messages[j].Clock
	})

	return messages
}

// CalculateSendTime calculates the next epoch
// at which a message should be sent.
func CalculateSendTime(count uint64, time int64) int64 {
	return time + int64(count*2)
}

func toGroupId(topicType whisper.TopicType) mvds.GroupID {
	g := mvds.GroupID{}
	copy(g[:], topicType[:])
	return g
}

func toTopicType(g mvds.GroupID) whisper.TopicType {
	t := whisper.TopicType{}
	copy(t[:], g[:4])
	return t
}

func messageID(m mvds.Message) []byte {
	t := make([]byte, 8)
	binary.LittleEndian.PutUint64(t, uint64(m.Timestamp))

	b := append([]byte("MESSAGE_ID"), m.GroupId[:]...)
	b = append(b, t...)
	b = append(b, m.Body...)

	r := sha256.Sum256(b)
	hash := make([]byte, len(r))
	copy(hash[:], r[:])

	return hash
}
