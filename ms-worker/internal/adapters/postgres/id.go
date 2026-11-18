package postgres

import "github.com/MarceloVilela/microservices-go/platform/messages"

// DefaultNewID gera message_id de UUID v4.
func DefaultNewID() string { return messages.NewID() }
