package protocol

import (
	"math/rand"

	whisper "github.com/status-im/whisper/whisperv6"
)

func randomItem(items []string) string {
	l := len(items)
	return items[rand.Intn(l)]
}

func createWhisperNewMessage(topic whisper.TopicType, data []byte, sigKey string) whisper.NewMessage {
	return whisper.NewMessage{
		TTL:       60,
		Topic:     topic,
		Payload:   data,
		PowTarget: 2.0,
		PowTime:   5,
		Sig:       sigKey,
	}
}
