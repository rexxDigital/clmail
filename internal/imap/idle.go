package imap

// TODO: Needs full reconnection support. I am short on time, will add this later

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
	"io"
	"log"
	"slices"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/rexxDigital/clmail/internal/db"
)

type IdleClient interface {
	GetFolders() []string
	Idle(folder string) error
	StopIdle() error
	Close() error
}

type idleClient struct {
	client       *imapclient.Client
	idleCtx      context.Context
	idleCancel   context.CancelFunc
	currIdleCmd  *imapclient.IdleCommand
	idleMutex    sync.Mutex
	isIdle       bool
	accountID    int64
	currFolder   string
	currFolderID int64
	dbClient     *db.Client
	account      db.Account
	password     string

	bodyFetchQueue  chan int64
	bodyFetchCtx    context.Context
	bodyFetchCancel context.CancelFunc
}

func NewIdleClient(account db.Account, password string, dbClient *db.Client) (IdleClient, error) {
	clientInstance := &idleClient{
		accountID:      account.ID,
		dbClient:       dbClient,
		account:        account,
		password:       password,
		bodyFetchQueue: make(chan int64, 1),
	}

	options := imapclient.Options{
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Expunge: func(seqNum uint32) {
				log.Printf("message %v has been expunged", seqNum)
			},
			Mailbox: func(data *imapclient.UnilateralDataMailbox) {
				if data.NumMessages != nil {
					if clientInstance != nil {
						go func() {
							err := clientInstance.fetchMessageHeaders()
							if err != nil {
								log.Printf("[IMAP::UnilateralData] Failed to fetch messages: %v", err)
							} else {
								log.Printf("[IMAP::UnilateralData] Successfully fetched new messages")
							}
						}()
					}
				}
			},
		},
	}

	client, err := imapclient.DialTLS(fmt.Sprintf("%v:%v", account.ImapServer, account.ImapPort), &options)
	if err != nil {
		return nil, fmt.Errorf("[IMAP::NewIdleClient] failed to dial: %w", err)
	}

	if err = client.Login(account.ImapUsername, password).Wait(); err != nil {
		return nil, fmt.Errorf("[IMAP::NewIdleClient] failed to login: %w", err)
	}

	hasIdleCap := client.Caps().Has(imap.CapIdle)
	if !hasIdleCap {
		return nil, fmt.Errorf("[IMAP::NewIdleClient] server does not support idle")
	}

	clientInstance.client = client

	clientInstance.bodyFetchCtx, clientInstance.bodyFetchCancel = context.WithCancel(context.Background())
	go clientInstance.startFetch()
	go clientInstance.bodyFetchTicker()

	return clientInstance, nil
}

func TestLoginAndGetFolders(account db.Account, password string, dbClient *db.Client) ([]string, error) {
	clientInstance := &idleClient{
		accountID:      account.ID,
		dbClient:       dbClient,
		account:        account,
		password:       password,
		bodyFetchQueue: make(chan int64, 1),
	}
	client, err := imapclient.DialTLS(fmt.Sprintf("%v:%v", account.ImapServer, account.ImapPort), nil)
	if err != nil {
		return nil, fmt.Errorf("[IMAP::NewIdleClient] failed to dial: %w", err)
	}

	if err = client.Login(account.Email, password).Wait(); err != nil {
		return nil, fmt.Errorf("[IMAP::NewIdleClient] failed to login: %w", err)
	}

	clientInstance.client = client

	folders := clientInstance.GetFolders()

	client.Logout().Wait()
	client.Close()

	return folders, nil
}

// GetFolders returns all folders without the \Noselect flag.
func (c *idleClient) GetFolders() []string {
	data, err := c.client.List("", "*", nil).Collect()
	if err != nil {
		log.Printf("[IMAP::GetFolders] Failed to get folders: %v", err)
		return []string{}
	}

	mailboxes := make([]string, 0)

	// i should add nested mailbox support here, and more robust checking for sent and trash etc.
	// TODO: Do this ^
	for _, m := range data {
		if slices.Contains(m.Attrs, "\\Noselect") {
			continue
		}
		mailboxes = append(mailboxes, m.Mailbox)
	}

	return mailboxes
}

// Idle just starts an idle on the INBOX folder as this is the most important one IMO
func (c *idleClient) Idle(folder string) error {
	if c.isIdle {
		return fmt.Errorf("[IMAP::Idle] already idle")
	}

	_, err := c.client.Select(folder, nil).Wait()
	if err != nil {
		return fmt.Errorf("[IMAP::Idle] failed to select folder: %w", err)
	}

	folderID, err := getFolderID(folder, c.accountID, c.dbClient)
	if err != nil {
		return fmt.Errorf("[IMAP::Idle] failed to get folder ID: %w", err)
	}

	c.currFolder = folder
	c.currFolderID = folderID

	err = c.fetchMessageHeaders()
	if err != nil {
		log.Printf("[IMAP::Idle] Failed to fetch existing messages: %v", err)
	}

	c.idleCtx, c.idleCancel = context.WithCancel(context.Background())

	go c.runIdle()

	c.isIdle = true
	return nil
}

// TODO: fix this for gracefull shutdown
func (c *idleClient) StopIdle() error {
	return nil
}

// handles our idle command by restarting every 20 minutes and retrying on err.
func (c *idleClient) runIdle() {
	defer func() {
		c.isIdle = false
	}()

	for {
		c.idleMutex.Lock()
		idleCmd, err := c.client.Idle()
		if err != nil {
			log.Printf("[IMAP::runIdle] Failed to start idle: %v", err)
			c.idleMutex.Unlock()

			select {
			case <-c.idleCtx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}

		c.currIdleCmd = idleCmd
		c.idleMutex.Unlock()

		select {
		case <-c.idleCtx.Done():
			c.idleMutex.Lock()
			if c.currIdleCmd != nil {
				idleCmd.Close()
				c.currIdleCmd = nil
			}
			c.idleMutex.Unlock()
			return
		case <-time.After(20 * time.Minute):
			c.idleMutex.Lock()
			if c.currIdleCmd != nil {
				idleCmd.Close()
				c.currIdleCmd = nil
			}
			c.idleMutex.Unlock()
		}
	}
}

// fetchMessageHeaders has to close idle since it is a blocking command
// and then restart our idle, so we keep track of it inside our struct.
func (c *idleClient) fetchMessageHeaders() error {
	c.idleMutex.Lock()
	defer c.idleMutex.Unlock()

	if c.currIdleCmd != nil {
		log.Printf("[IMAP::fetchWithIdleRestart] Stopping IDLE...")
		err := c.currIdleCmd.Close()
		if err != nil {
			log.Printf("[IMAP::fetchWithIdleRestart] Failed to stop IDLE: %v", err)
		}
		c.currIdleCmd = nil
	}

	if c.currFolder == "" {
		log.Printf("[IMAP::fetchNewMessages] No folder selected")
		return nil
	}

	status, err := c.client.Status(c.currFolder, &imap.StatusOptions{
		NumMessages: true,
		UIDNext:     true,
	}).Wait()
	if err != nil {

		return fmt.Errorf("[IMAP::fetchMessages] Failed to get status: %v", err)
	}

	if *status.NumMessages == 0 {
		return nil
	}

	uidSet := imap.UIDSet{}

	// only get new messages, so we don't refetch large amounts of mails that we already track.
	highestUID, err := getHighestUIDInFolder(c.currFolderID, c.dbClient)
	if err != nil || highestUID == 0 {
		uidSet.AddRange(1, 0)
	} else {
		uidSet.AddRange(imap.UID(highestUID+1), 0)
	}

	currentMailbox := c.client.Mailbox()
	if currentMailbox == nil || currentMailbox.Name != c.currFolder {
		_, err = c.client.Select(c.currFolder, nil).Wait()
		if err != nil {
			return fmt.Errorf("[IMAP::fetchMessageHeaders] Failed to select folder: %w", err)
		}
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
		processBodyStructure(msg, c.currFolderID, c.accountID, c.dbClient)
	}

	return nil
}

func (c *idleClient) startFetch() {
	for {
		select {
		case <-c.bodyFetchCtx.Done():
			return
		case emailID := <-c.bodyFetchQueue:
			err := c.fetchEmailBody(emailID)
			if err != nil {
				// could implement retry logic here
				log.Printf("[IMAP::startFetch] Error: %d", err)
			}

			// small delay so we do not spam the server
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// bodyFetchTicker fetches email bodies every second
func (c *idleClient) bodyFetchTicker() {
	ticker := time.NewTicker(20 * time.Second) // check every second
	defer ticker.Stop()

	for {
		select {
		case <-c.bodyFetchCtx.Done():
			return
		case <-ticker.C:
			c.queueEmailsForBodyFetching()
		}
	}
}

// queueEmailsForBodyFetching finds emails without bodies and queues them
func (c *idleClient) queueEmailsForBodyFetching() {
	emails, err := c.dbClient.GetEmailsWithoutBodies(context.Background(), db.GetEmailsWithoutBodiesParams{
		AccountID: c.accountID,
		FolderID:  c.currFolderID,
		Limit:     50,
	})
	if err != nil {
		log.Printf("[IMAP::queueEmailsForBodyFetching] Failed to get emails without bodies: %v", err)
		return
	}

	for _, email := range emails {
		select {
		case c.bodyFetchQueue <- email.Uid:
		default:
			return
		}
	}
}

// fetchEmailBody creates a new client, since fetching body content takes a long time. We want the idle command to still be able to fetch new mails in the meantime.
func (c *idleClient) fetchEmailBody(emailID int64) error {
	client, err := imapclient.DialTLS(fmt.Sprintf("%v:%v", c.account.ImapServer, c.account.ImapPort), nil)
	if err != nil {
		return fmt.Errorf("[IMAP::fetchEmailBody] failed to dial: %w", err)
	}

	if err = client.Login(c.account.ImapUsername, c.password).Wait(); err != nil {
		return fmt.Errorf("[IMAP::fetchEmailBody] failed to login: %w", err)
	}

	// defer logging out and closing our connection!!
	defer func() {
		if client != nil {
			logoutCmd := client.Logout()
			if logoutCmd != nil {
				logoutCmd.Wait()
			}
			client.Close()
		}
	}()

	email, err := c.dbClient.GetEmailByFolderAndUID(context.Background(), db.GetEmailByFolderAndUIDParams{
		Uid:      emailID,
		FolderID: c.currFolderID,
	})
	if err != nil {
		return fmt.Errorf("[IMAP::fetchEmailBody] failed to get email: %w", err)
	}

	if email.BodyText.Valid {
		log.Printf("[IMAP::fetchEmailBody] VALID??")
		return nil
	}

	folder, err := c.dbClient.GetFolder(context.Background(), c.currFolderID)
	if err != nil {
		return fmt.Errorf("[IMAP::fetchEmailBody] failed to get folder: %w", err)
	}

	_, err = client.Select(folder.Name, nil).Wait()
	if err != nil {
		return fmt.Errorf("[IMAP::fetchEmailBody] failed to select folder: %w", err)
	}

	fetchOptions := &imap.FetchOptions{
		BodySection: []*imap.FetchItemBodySection{{}},
	}

	uidSet := imap.UIDSet{}
	uidSet.AddNum(imap.UID(email.Uid))

	fetchCmd := client.Fetch(uidSet, fetchOptions)
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
				log.Println("[IMAP::fetchEmailBody] Unknown data type")
			}
			if ok {
				break
			}
		}
		if !ok {
			log.Println("[IMAP::fetchEmailBody] Could not fetch all data from message")
			continue
		}

		// create mail reader
		mailReader, err := mail.CreateReader(bodySection.Literal)
		if err != nil {
			log.Printf("[IMAP::fetchEmailBody] Could not create mail reader: %v", err)
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
				log.Printf("[IMAP::fetchEmailBody] Could not read mail part trying to decode charset with error: %v", err.Error())
			} else if err != nil {
				log.Printf("[IMAP::fetchEmailBody] Could not read mail part with error: %v", err.Error())
				break
			}

			switch h := part.Header.(type) {
			case *mail.InlineHeader:
				ct, params, _ := h.ContentType()

				charset := params["charset"]

				decodedBody, err := decodeCharset(charset, part.Body)
				if err != nil {
					log.Printf("[IMAP::fetchEmailBody] Could not decode charset with error: %v", err.Error())
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
			return fmt.Errorf("[IMAP::fetchEmailBody] failed to update email: %w", err)
		}
	}

	return nil
}

// Close logs out our user and closes the connection
func (c *idleClient) Close() error {
	err := c.client.Logout().Wait()
	if err != nil {
		return fmt.Errorf("[IMAP::Close] failed to logout: %w", err)
	}
	err = c.client.Close()
	if err != nil {
		return fmt.Errorf("[IMAP::Close] failed to close: %w", err)
	}
	return nil
}
