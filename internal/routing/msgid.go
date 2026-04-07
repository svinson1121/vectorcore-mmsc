package routing

import "github.com/google/uuid"

func NewMessageID() string {
	return uuid.NewString()
}
