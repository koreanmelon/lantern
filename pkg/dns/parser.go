package dns

import (
	"fmt"

	"golang.org/x/net/dns/dnsmessage"
)

// Parse decodes a DNS message without TCP's two-byte length prefix.
func Parse(packet []byte) (dnsmessage.Message, error) {
	var message dnsmessage.Message
	if err := message.Unpack(packet); err != nil {
		return dnsmessage.Message{}, fmt.Errorf("parse DNS message: %w", err)
	}
	return message, nil
}
