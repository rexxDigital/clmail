package imap

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
	"github.com/rexxDigital/clmail/internal/db"
	"io"
	"log"
	"strings"
	"time"
)

type SyncClient interface {
	SyncFolder(folder string) error
	SaveSent(mail string, date time.Time) error
	Close() error
}

type syncClient struct {
	account  db.Account
	password string
	client   *imapclient.Client
	dbClient *db.Client
	folderID int64
}

func NewSyncClient(account db.Account, password string, dbClient *db.Client) (SyncClient, error) {
	client, err := imapclient.DialTLS(fmt.Sprintf("%v:%v", account.ImapServer, account.ImapPort), nil)
	if err != nil {
		return nil, fmt.Errorf("[SyncClient::NewIdleClient] failed to dial: %w", err)
	}

	if err = client.Login(account.ImapUsername, password).Wait(); err != nil {
		return nil, fmt.Errorf("[SyncClient::NewIdleClient] failed to login: %w", err)
	}

	return &syncClient{
		account:  account,
		password: password,
		client:   client,
		dbClient: dbClient,
	}, nil
}

func (c *syncClient) SyncFolder(folder string) error {
	dbFolder, err := c.dbClient.GetFolderByName(context.Background(), db.GetFolderByNameParams{
		Name:      folder,
		AccountID: c.account.ID,
	})
	if err != nil {
		return err
	}

	if dbFolder.Name == "INBOX" {
		return nil
	}

	c.folderID = dbFolder.ID

	_, err = c.client.Select(folder, nil).Wait()
	if err != nil {
		return fmt.Errorf("[SyncClient::SyncFolder] failed to select folder: %w", err)
	}

	if err = c.fetchMessageHeaders(folder, dbFolder.ID); err != nil {
		return err
	}

	if err = c.fetchBodiesForFolder(folder); err != nil {
		return err
	}
	return nil
}

func (c *syncClient) SaveSent(mail string, date time.Time) error {
	folders, err := c.dbClient.ListFolders(context.Background(), c.account.ID)
	if err != nil {
		return err
	}

	sentFolder := ""
	for _, folder := range folders {
		if strings.Contains(strings.ToLower(folder.Name), strings.ToLower("sent")) {
			sentFolder = folder.Name
			break
		}
	}

	appendCmd := c.client.Append(sentFolder, int64(len(mail)), &imap.AppendOptions{
		Flags: []imap.Flag{imap.FlagSeen},
		Time:  date,
	})

	_, err = appendCmd.Write([]byte(mail))
	if err != nil {
		return err
	}

	err = appendCmd.Close()
	if err != nil {
		return err
	}

	_, err = appendCmd.Wait()
	if err != nil {
		return err
	}

	return nil
}

func (c *syncClient) Close() error {
	if err := c.client.Logout().Wait(); err != nil {
		return fmt.Errorf("[SyncClient::Close] failed to logout: %w", err)
	}
	return c.client.Close()
}

// fetchMessageHeaders has to close idle since it is a blocking command
// and then restart our idle, so we keep track of it inside our struct.
func (c *syncClient) fetchMessageHeaders(folder string, folderID int64) error {
	status, err := c.client.Status(folder, &imap.StatusOptions{
		NumMessages: true,
		UIDNext:     true,
	}).Wait()
	if err != nil {

		return fmt.Errorf("[SyncClient::fetchMessages] Failed to get status: %v", err)
	}

	if *status.NumMessages == 0 {
		return nil
	}

	uidSet := imap.UIDSet{}

	// only get new messages, so we don't refetch large amounts of mails that we already track.
	highestUID, err := getHighestUIDInFolder(folderID, c.dbClient)
	if err != nil || highestUID == 0 {
		uidSet.AddRange(1, 0)
	} else {
		uidSet.AddRange(imap.UID(highestUID+1), 0)
	}

	// we are fetching the body structure first as it is fast, and we can get all necessary metadata
	// for displaying new mails in the mail list
	fetchOptions := &imap.FetchOptions{
		Flags:         true,
		Envelope:      true,
		UID:           true,
		BodyStructure: &imap.FetchItemBodyStructure{},
	}

	messages, err := c.client.Fetch(uidSet, fetchOptions).Collect()
	if err != nil {
		return fmt.Errorf("failed to fetch messages: %w", err)
	}

	// Process each message
	for _, msg := range messages {
		processBodyStructure(msg, folderID, c.account.ID, c.dbClient)
	}

	return nil
}

// fetchBodiesForFolder finds emails without bodies fetches all body data
func (c *syncClient) fetchBodiesForFolder(folder string) error {
	emails, err := c.dbClient.GetEmailsWithoutBodies(context.Background(), db.GetEmailsWithoutBodiesParams{
		AccountID: c.account.ID,
		FolderID:  c.folderID,
		Limit:     50,
	})
	if err != nil {

		return fmt.Errorf("[SyncClient::queueEmailsForBodyFetching] Failed to get emails without bodies: %v", err)
	}

	_, err = c.client.Select(folder, nil).Wait()
	if err != nil {
		return fmt.Errorf("failed to select folder %s: %w", folder, err)
	}

	// fetch bodies immediately unlike idle
	for _, email := range emails {
		err := c.fetchEmailBody(email.Uid)
		if err != nil {
			return fmt.Errorf("[SyncClient::fetchBodiesForFolder] Failed to fetch body for email %d (UID %d): %v",
				email.ID, email.Uid, err)
		}
	}

	return nil
}

func (c *syncClient) fetchEmailBody(emailID int64) error {
	email, err := c.dbClient.GetEmailByFolderAndUID(context.Background(), db.GetEmailByFolderAndUIDParams{
		Uid:      emailID,
		FolderID: c.folderID,
	})
	if err != nil {
		return fmt.Errorf("[SyncClient::fetchEmailBody] failed to get email: %w", err)
	}

	if email.BodyText.Valid {
		log.Printf("[SyncClient::fetchEmailBody] VALID??")
		return nil
	}

	fetchOptions := &imap.FetchOptions{
		BodySection: []*imap.FetchItemBodySection{{}},
	}

	uidSet := imap.UIDSet{}
	uidSet.AddNum(imap.UID(email.Uid))

	fetchCmd := c.client.Fetch(uidSet, fetchOptions)
	defer fetchCmd.Close()

	// loop through each mail message
	for {
		msg := fetchCmd.Next()

		/*
			if the message is nil we don't have any more messages to fetch
			if we don't have anything more to fetch we break
		*/
		if msg == nil {
			break
		}

		var bodySection imapclient.FetchItemDataBodySection

		// loop through the mail data to find each item we are looking for
		ok := false
		for {
			// get next data point
			item := msg.Next()

			// check if we have read all the data points we want
			if item == nil {
				break
			}

			// go through each section and set data
			bodySection, ok = item.(imapclient.FetchItemDataBodySection)
			switch item := item.(type) {
			case imapclient.FetchItemDataBodySection:
				bodySection = item
				ok = true
			default:
			}
			if ok {
				break
			}
		}
		if !ok {
			log.Println("[SyncClient::fetchEmailBody] Could not fetch all data from message")
			continue
		}

		// create mail reader
		mailReader, err := mail.CreateReader(bodySection.Literal)
		if err != nil {
			log.Printf("[SyncClient::fetchEmailBody] Could not create mail reader: %v", err)
			continue
		}

		// get References from mail header
		header := mailReader.Header
		/*
		   try to get references from msg-id if available
		*/
		mailReferences, err := header.MsgIDList("References")
		if mailReferences == nil {
			mailReferences = make([]string, 0)
		}

		// get mail body data
		var _, bodyPlain []byte

		for {
			part, err := mailReader.NextPart()
			if err == io.EOF {
				break
			} else if message.IsUnknownCharset(err) {
				log.Printf("[SyncClient::fetchEmailBody] Could not read mail part trying to decode charset with error: %v", err.Error())
			} else if err != nil {
				log.Printf("[SyncClient::fetchEmailBody] Could not read mail part with error: %v", err.Error())
				break
			}

			switch h := part.Header.(type) {
			case *mail.InlineHeader:
				ct, params, _ := h.ContentType()

				charset := params["charset"]

				decodedBody, err := decodeCharset(charset, part.Body)
				if err != nil {
					log.Printf("[SyncClient::fetchEmailBody] Could not decode charset with error: %v", err.Error())
				}

				switch ct {
				case "text/plain":
					bodyPlain, _ = io.ReadAll(decodedBody)
				case "text/html":
					_, _ = io.ReadAll(decodedBody)
				}
			case *mail.AttachmentHeader:
				// TODO: Add support for attachments
				_, _ = h.Filename()
			}
		}

		refs := ""
		for i, ref := range mailReferences {
			if i != len(mailReferences)-1 {
				refs += ref + ","
			} else {
				refs += ref
			}
		}

		_, err = c.dbClient.UpdateEmailBodyAndReferences(context.Background(), db.UpdateEmailBodyAndReferencesParams{
			ID: email.ID,
			BodyText: sql.NullString{
				String: (string)(bodyPlain),
				Valid:  bodyPlain != nil,
			},
			ReferenceID: sql.NullString{
				String: refs,
				Valid:  refs != "",
			},
		})

		if err != nil {
			return fmt.Errorf("[SyncClient::fetchEmailBody] failed to update email: %w", err)
		}
	}

	return nil
}
