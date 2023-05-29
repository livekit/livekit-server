package service

import (
	"context"
	"log"

	p2p_database "github.com/dTelecom/p2p-realtime-database"
	"github.com/pkg/errors"
)

const (
	prefixIncomingMessageTopic = "incoming_messages_"
	defaultBufferMessagesChan  = 100
)

type NodeCommunication struct {
	messages chan p2p_database.Event
	db       *p2p_database.DB
}

func NewNodeCommunication(db *p2p_database.DB) *NodeCommunication {
	return &NodeCommunication{
		messages: make(chan p2p_database.Event, defaultBufferMessagesChan),
		db:       db,
	}
}

func (c *NodeCommunication) Setup(
	ctx context.Context,
	onMessage func(e p2p_database.Event),
) {
	go func() {
		responsesChannel, err := c.ListenIncomingMessages(ctx)
		if err != nil {
			log.Fatalf("cannot listen incoming messsages database room %s %s", c.db.Name, err)
		}

		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-responsesChannel:
				log.Printf("got message %s from node %s from database room %s: %s", msg.ID, msg.FromPeerId, c.db.Name, msg.Message)
				onMessage(msg)
			}
		}
	}()
}

func (c *NodeCommunication) SendAsyncMessageToPeerId(ctx context.Context, peerId string, message interface{}) (string, error) {
	m, err := c.db.Publish(ctx, c.messagesP2PTopicName(peerId), message)
	if err != nil {
		return "", errors.Wrap(err, "db publish")
	}
	return m.ID, nil
}

func (c *NodeCommunication) ListenIncomingMessages(ctx context.Context) (chan p2p_database.Event, error) {
	err := c.db.Subscribe(ctx, c.messagesP2PTopicName(c.db.GetHost().ID().String()), func(event p2p_database.Event) {
		c.messages <- event
	})
	if err != nil {
		return nil, errors.Wrap(err, "subscribe message topic")
	}
	return c.messages, nil
}

func (c *NodeCommunication) messagesP2PTopicName(peerId string) string {
	return prefixIncomingMessageTopic + peerId
}
