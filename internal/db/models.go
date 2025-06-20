// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.29.0

package db

import (
	"database/sql"
	"time"
)

type Account struct {
	ID                     int64
	Name                   string
	DisplayName            string
	Email                  string
	ImapServer             string
	ImapPort               int64
	ImapUsername           string
	ImapUseSsl             bool
	ImapAuthMethod         string
	SmtpServer             string
	SmtpPort               int64
	SmtpUsername           string
	SmtpUseTls             bool
	SmtpAuthMethod         string
	RefreshIntervalMinutes int64
	Signature              sql.NullString
	IsDefault              bool
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type Attachment struct {
	ID        int64
	EmailID   int64
	Filename  string
	MimeType  string
	SizeBytes int64
	Content   []byte
	LocalPath sql.NullString
}

type Email struct {
	ID           int64
	Uid          int64
	ThreadID     int64
	AccountID    int64
	FolderID     int64
	MessageID    string
	FromAddress  string
	FromName     sql.NullString
	ToAddresses  string
	CcAddresses  sql.NullString
	BccAddresses sql.NullString
	ReferenceID  sql.NullString
	Subject      string
	BodyText     sql.NullString
	BodyHtml     sql.NullString
	ReceivedDate time.Time
	IsRead       bool
	IsStarred    bool
	IsDraft      bool
}

type Folder struct {
	ID        int64
	AccountID int64
	Name      string
}

type Thread struct {
	ID                int64
	AccountID         int64
	Subject           string
	Snippet           sql.NullString
	IsRead            bool
	IsStarred         bool
	HasAttachments    bool
	MessageCount      int64
	LatestMessageDate time.Time
}
