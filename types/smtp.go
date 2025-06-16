package types

import "time"

type Mail struct {
	MessageID               string
	References              string
	To, From, Subject, Body string
	CC                      []string
	Date                    time.Time
	InReplyTo               string
}
