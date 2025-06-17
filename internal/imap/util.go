package imap

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/rexxDigital/clmail/internal/db"
	"github.com/wlynxg/chardet"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/transform"
	"io"
	"log"
	"slices"
	"strings"
)

func buildAddressListString(addresses []imap.Address) string {
	if len(addresses) == 0 {
		return ""
	}

	var addrs []string
	for _, addr := range addresses {
		addrs = append(addrs, addr.Addr())
	}

	return strings.Join(addrs, ",")
}

// threadMail attempts to thread to an existing thread or creates a new one in case it is new
func threadMail(envelope *imap.Envelope, accountID int64, dbClient *db.Client) (int64, error) {
	// check if we have a reply-to value, if we do, link to thread
	if len(envelope.InReplyTo) != 0 {
		email, err := dbClient.GetEmailByMessageID(context.Background(), envelope.InReplyTo[0])
		if err == nil {
			_, err = dbClient.UpdateThread(context.Background(), db.UpdateThreadParams{
				Subject:           envelope.Subject,
				LatestMessageDate: envelope.Date,
				ID:                email.ThreadID,
			})

			if err != nil {
				return 0, err
			}

			return email.ThreadID, nil
		}
	}

	// TODO: should really add reference checking as well here, and maybe even subject checking as a backup
	// not threading by reference can have issues, right now threading doesnt work all too well on already existing mail accounts
	// this will get fixed in the coming days (today is 17/06/25)

	newThread, err := dbClient.CreateThread(context.Background(), db.CreateThreadParams{
		AccountID:         accountID,
		Subject:           envelope.Subject,
		MessageCount:      1,
		LatestMessageDate: envelope.Date,
	})
	if err != nil {
		return 0, err
	}

	return newThread.ID, nil
}

// getHighestUIDInFolder returns the highest UID we have stored for this folder
func getHighestUIDInFolder(folderID int64, dbClient *db.Client) (uint32, error) {
	uid, err := dbClient.GetHighestUIDInFolder(context.Background(), folderID)
	if err != nil {
		// No emails in folder yet
		return 0, nil
	}

	return uint32(uid), nil
}

func processBodyStructure(msg *imapclient.FetchMessageBuffer, folderID int64, accountID int64, dbClient *db.Client) {
	if msg.Envelope.MessageID != "" {
		_, err := dbClient.GetEmailByMessageID(context.Background(), msg.Envelope.MessageID)
		if err == nil {
			return
		}
	}

	// comma separated cc and bcc for db strings
	cscc := buildAddressListString(msg.Envelope.Cc)
	csbcc := buildAddressListString(msg.Envelope.Bcc)

	if len(msg.Envelope.From) < 1 || len(msg.Envelope.To) < 1 {
		return
	}

	threadID, err := threadMail(msg.Envelope, accountID, dbClient)
	if err != nil {
		log.Printf("[IMAP::processBodyStructure] Failed to thread mail: %v", err)
		return
	}

	_, err = dbClient.CreateEmail(context.Background(), db.CreateEmailParams{
		Uid:          int64(msg.UID),
		ThreadID:     threadID,
		AccountID:    accountID,
		FolderID:     folderID,
		MessageID:    msg.Envelope.MessageID,
		FromAddress:  msg.Envelope.From[0].Addr(),
		FromName:     sql.NullString{String: msg.Envelope.From[0].Name, Valid: msg.Envelope.From[0].Name != ""},
		ToAddresses:  msg.Envelope.To[0].Addr(),
		CcAddresses:  sql.NullString{String: cscc, Valid: cscc != ""},
		BccAddresses: sql.NullString{String: csbcc, Valid: csbcc != ""},
		Subject:      msg.Envelope.Subject,
		BodyText:     sql.NullString{},
		BodyHtml:     sql.NullString{},
		ReceivedDate: msg.Envelope.Date,
		IsRead:       slices.Contains(msg.Flags, "\\Seen"),
		IsStarred:    slices.Contains(msg.Flags, "\\Flagged"),
		IsDraft:      slices.Contains(msg.Flags, "\\Draft"),
	})

	if err != nil {
		log.Printf("[IMAP::processBodyStructure] Failed to create email: %v", err)
		return
	}

	return
}

// i wrote this decodeCharset function when we had issues with charsets at Sendswift

func decodeCharset(charset string, input io.Reader) (io.Reader, error) {
	if charset == "" {
		detector := chardet.NewUniversalDetector(0)

		buffer := make([]byte, 4096)
		var totalData []byte

		for {
			n, readErr := input.Read(buffer)

			if n > 0 {
				totalData = append(totalData, buffer[:n]...)
				detector.Feed(buffer[:n])
			}

			if readErr == io.EOF {
				break
			}

			if readErr != nil {
				return nil, fmt.Errorf("error reading input: %v", readErr)
			}
		}

		result := detector.GetResult()

		charset = result.Encoding // use detected charset
	}

	var decoder *encoding.Decoder

	switch strings.ToLower(charset) {
	case "windows-1252":
		decoder = charmap.Windows1252.NewDecoder()
	case "iso-8859-1":
		decoder = charmap.ISO8859_1.NewDecoder()
	case "iso-8859-15":
		decoder = charmap.ISO8859_15.NewDecoder()
	case "windows-1250":
		decoder = charmap.Windows1250.NewDecoder()
	case "utf-8", "us-ascii":
		return input, nil // already UTF-8 compatible
	default:
		// try to find charset using IANA registry as fallback
		enc, err := ianaindex.IANA.Encoding(charset)
		if err != nil || enc == nil {
			return input, nil // fallback to default (assume UTF-8)
		}
		decoder = enc.NewDecoder()
	}

	// convert to UTF-8
	return transform.NewReader(input, decoder), nil
}

func getFolderID(folder string, accountID int64, dbClient *db.Client) (int64, error) {
	dbFolder, err := dbClient.GetFolderByName(context.Background(), db.GetFolderByNameParams{
		Name:      folder,
		AccountID: accountID,
	})
	if err != nil {
		return 0, err
	}

	return dbFolder.ID, nil
}
