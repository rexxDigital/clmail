package imap

// TODO: Needs full reconnection support. I am short on time, will add this later

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/wlynxg/chardet"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/transform"
	"io"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/rexxDigital/clmail/internal/db"
)

type ImapClient interface {
	GetFolders() []string
	Idle(folder string) error
	StopIdle() error
	Close() error
}

type imapClient struct {
	client       *imapclient.Client
	idleCtx      context.Context
	idleCancel   context.CancelFunc
	currIdleCmd  *imapclient.IdleCommand
	idleMutex    sync.Mutex
	isIdle       bool
	accountId    int64
	currFolder   string
	currFolderId int64
	dbClient     *db.Client
	account      db.Account
	password     string

	bodyFetchQueue  chan int64
	bodyFetchCtx    context.Context
	bodyFetchCancel context.CancelFunc
}

func NewImapClient(account db.Account, password string, dbClient *db.Client) (ImapClient, error) {
	clientInstance := &imapClient{
		accountId:      account.ID,
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
		return nil, fmt.Errorf("[IMAP::NewImapClient] failed to dial: %w", err)
	}

	if err = client.Login(account.Email, password).Wait(); err != nil {
		return nil, fmt.Errorf("[IMAP::NewImapClient] failed to login: %w", err)
	}

	hasIdleCap := client.Caps().Has(imap.CapIdle)
	if !hasIdleCap {
		return nil, fmt.Errorf("[IMAP::NewImapClient] server does not support idle")
	}

	clientInstance.client = client

	clientInstance.bodyFetchCtx, clientInstance.bodyFetchCancel = context.WithCancel(context.Background())
	go clientInstance.startFetch()
	go clientInstance.bodyFetchTicker()

	return clientInstance, nil
}

func TestLoginAndGetFolders(account db.Account, password string, dbClient *db.Client) ([]string, error) {
	clientInstance := &imapClient{
		accountId:      account.ID,
		dbClient:       dbClient,
		account:        account,
		password:       password,
		bodyFetchQueue: make(chan int64, 1),
	}
	client, err := imapclient.DialTLS(fmt.Sprintf("%v:%v", account.ImapServer, account.ImapPort), nil)
	if err != nil {
		return nil, fmt.Errorf("[IMAP::NewImapClient] failed to dial: %w", err)
	}

	if err = client.Login(account.Email, password).Wait(); err != nil {
		return nil, fmt.Errorf("[IMAP::NewImapClient] failed to login: %w", err)
	}

	clientInstance.client = client

	folders := clientInstance.GetFolders()

	client.Logout().Wait()
	client.Close()

	return folders, nil
}

// GetFolders returns all folders without the \Noselect flag.
func (c *imapClient) GetFolders() []string {
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
func (c *imapClient) Idle(folder string) error {
	if c.isIdle {
		return fmt.Errorf("[IMAP::Idle] already idle")
	}

	_, err := c.client.Select(folder, nil).Wait()
	if err != nil {
		return fmt.Errorf("[IMAP::Idle] failed to select folder: %w", err)
	}

	folderID, err := c.getFolderID(folder)

	c.currFolder = folder
	c.currFolderId = folderID

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
func (c *imapClient) StopIdle() error {
	return nil
}

// handles our idle command by restarting every 20 minutes and retrying on err.
func (c *imapClient) runIdle() {
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
func (c *imapClient) fetchMessageHeaders() error {
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
	highestUID, err := c.getHighestUIDInFolder(c.currFolderId)
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
		c.processBodyStructure(msg)
	}

	return nil
}

func (c *imapClient) processBodyStructure(msg *imapclient.FetchMessageBuffer) {
	if msg.Envelope.MessageID != "" {
		_, err := c.dbClient.GetEmailByMessageID(context.Background(), msg.Envelope.MessageID)
		if err == nil {
			return
		}
	}

	// comma separated cc and bcc for db strings
	cscc := c.buildAddressListString(msg.Envelope.Cc)
	csbcc := c.buildAddressListString(msg.Envelope.Bcc)

	if len(msg.Envelope.From) < 1 || len(msg.Envelope.To) < 1 {
		return
	}

	threadID, err := c.threadMail(msg.Envelope)
	if err != nil {
		log.Printf("[IMAP::processBodyStructure] Failed to thread mail: %v", err)
		return
	}

	_, err = c.dbClient.CreateEmail(context.Background(), db.CreateEmailParams{
		Uid:          int64(msg.UID),
		ThreadID:     threadID,
		AccountID:    c.accountId,
		FolderID:     c.currFolderId,
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

func (c *imapClient) startFetch() {
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
func (c *imapClient) bodyFetchTicker() {
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
func (c *imapClient) queueEmailsForBodyFetching() {
	// Get emails without bodies, ordered by newest first
	emails, err := c.dbClient.GetEmailsWithoutBodies(context.Background(), db.GetEmailsWithoutBodiesParams{
		AccountID: c.accountId,
		Limit:     50, // Process 50 at a time
	})
	if err != nil {
		log.Printf("[IMAP::queueEmailsForBodyFetching] Failed to get emails without bodies: %v", err)
		return
	}

	for _, email := range emails {
		select {
		case c.bodyFetchQueue <- email.ID:
			// Queued successfully
		default:
			// Queue is full, skip for now
			log.Printf("[IMAP::queueEmailsForBodyFetching] Body fetch queue is full, skipping email %d", email.ID)
			return
		}
	}
}

// fetchEmailBody creates a new client, since fetching body content takes a long time. We want the idle command to still be able to fetch new mails in the meantime.
func (c *imapClient) fetchEmailBody(emailID int64) error {
	log.Printf("[IMAP::fetchEmailBody] Starting body fetch for email ID: %d", emailID)

	client, err := imapclient.DialTLS(fmt.Sprintf("%v:%v", c.account.ImapServer, c.account.ImapPort), nil)
	if err != nil {
		return fmt.Errorf("[IMAP::fetchEmailBody] failed to dial: %w", err)
	}

	if err = client.Login(c.account.Email, c.password).Wait(); err != nil {
		return fmt.Errorf("[IMAP::fetchEmailBody] failed to login: %w", err)
	}

	// defer logging out and closing our connection!!
	defer func() {
		log.Printf("[IMAP::fetchEmailBody] Starting cleanup")
		if client != nil {
			logoutCmd := client.Logout()
			if logoutCmd != nil {
				logoutCmd.Wait()
			}
			client.Close()
		}
		log.Printf("[IMAP::fetchEmailBody] Cleanup completed")
	}()

	email, err := c.dbClient.GetEmail(context.Background(), emailID)
	if err != nil {
		return fmt.Errorf("[IMAP::fetchEmailBody] failed to get email: %w", err)
	}

	log.Printf("[IMAP::fetchEmailBody] Email UID: %d, Subject: %s", email.Uid, email.Subject)

	if email.BodyText.Valid {
		log.Printf("[IMAP::fetchEmailBody] VALID??")
		return nil
	}

	folder, err := c.dbClient.GetFolder(context.Background(), email.FolderID)
	if err != nil {
		return fmt.Errorf("[IMAP::fetchEmailBody] failed to get folder: %w", err)
	}

	log.Printf("[IMAP::fetchEmailBody] Selecting folder: %s", folder.Name)

	_, err = client.Select(folder.Name, nil).Wait()
	if err != nil {
		return fmt.Errorf("[IMAP::fetchEmailBody] failed to select folder: %w", err)
	}

	fetchOptions := &imap.FetchOptions{
		BodyStructure: &imap.FetchItemBodyStructure{},
		BodySection: []*imap.FetchItemBodySection{
			{
				Specifier: imap.PartSpecifierText,
			},
		},
	}

	uidSet := imap.UIDSet{}
	uidSet.AddNum(imap.UID(email.Uid))

	log.Printf("[IMAP::fetchEmailBody] Fetching UID: %d", email.Uid)

	fetchCmd := client.Fetch(uidSet, fetchOptions)
	defer fetchCmd.Close()

	for {
		msg := fetchCmd.Next()
		if msg == nil {
			break
		}

		log.Printf("[IMAP::fetchEmailBody] Processing message")

		// create variable for storing the body
		var bodySection imapclient.FetchItemDataBodySection
		found := false

		for {
			item := msg.Next()
			if item == nil {
				break
			}
			if bs, ok := item.(imapclient.FetchItemDataBodySection); ok {
				log.Printf("[IMAP::fetchEmailBody] Found body section: %s", bs.Section.Specifier)

				bodySection = bs
				found = true
				break
			}
		}
		if !found {
			log.Printf("[IMAP::fetchEmailBody] failed to read body section")
			continue
		}

		content, err := io.ReadAll(bodySection.Literal)
		//
		//// create our mail reader
		//mailReader, err := mail.CreateReader(bodySection.Literal)
		//if err != nil {
		//	log.Printf("[IMAP::fetchEmailBody] failed to create mail reader: %v", err)
		//	continue
		//}
		//
		//// TODO: add support for references :)
		//// header := mailReader.Header -> mailReferences, err := header.MsgIDList("References")
		//
		//// time to get the mail body data!
		//var bodyPlain []byte
		//
		//for {
		//	part, err := mailReader.NextPart()
		//	if err == io.EOF {
		//		break
		//	} else if err != nil {
		//		break
		//	}
		//
		//	switch h := part.Header.(type) {
		//	case *mail.InlineHeader:
		//		ct, params, _ := h.ContentType()
		//
		//		charset := params["charset"]
		//
		//		decodedBody, err := decodeCharset(charset, part.Body)
		//		if err != nil {
		//			log.Printf("[Bialetti::getMails] Could not decode charset with error: %v", err.Error())
		//		}
		//
		//		switch ct {
		//		case "text/plain":
		//			bodyPlain, _ = io.ReadAll(decodedBody)
		//			// wen html support!?
		//			//case "text/html":
		//			//	bodyHTML, _ = io.ReadAll(decodedBody)
		//		}
		//	case *mail.AttachmentHeader:
		//		// TODO: Add support for attachments
		//		_, _ = h.Filename()
		//	}
		//}
		//
		log.Printf("[IMAP::fetchEmailBody] Saving to db")

		_, err = c.dbClient.UpdateEmailBody(context.Background(), db.UpdateEmailBodyParams{
			ID: emailID,
			BodyText: sql.NullString{
				String: (string)(content),
				Valid:  content != nil,
			},
		})

		if err != nil {
			return fmt.Errorf("[IMAP::fetchEmailBody] failed to update email: %w", err)
		}
	}

	return nil
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
			return input, nil // Fallback to default (assume UTF-8)
		}
		decoder = enc.NewDecoder()
	}

	// convert to UTF-8
	return transform.NewReader(input, decoder), nil
}

func (c *imapClient) getFolderID(folder string) (int64, error) {
	dbFolder, err := c.dbClient.GetFolderByName(context.Background(), db.GetFolderByNameParams{
		Name:      folder,
		AccountID: c.accountId,
	})
	if err != nil {
		return 0, err
	}

	return dbFolder.ID, nil
}

// threadMail attempts to thread to an existing thread or creates a new one in case it is new
func (c *imapClient) threadMail(envelope *imap.Envelope) (int64, error) {
	// check if we have a reply-to value, if we do, link to thread
	if len(envelope.InReplyTo) != 0 {
		email, err := c.dbClient.GetEmailByMessageID(context.Background(), envelope.InReplyTo[0])
		if err == nil {
			_, err = c.dbClient.UpdateThread(context.Background(), db.UpdateThreadParams{
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

	newThread, err := c.dbClient.CreateThread(context.Background(), db.CreateThreadParams{
		AccountID:         c.accountId,
		Subject:           envelope.Subject,
		MessageCount:      1,
		LatestMessageDate: envelope.Date,
	})
	if err != nil {
		return 0, err
	}

	return newThread.ID, nil
}

func (c *imapClient) buildAddressListString(addresses []imap.Address) string {
	if len(addresses) == 0 {
		return ""
	}

	var addrs []string
	for _, addr := range addresses {
		addrs = append(addrs, addr.Addr())
	}

	return strings.Join(addrs, ",")
}

// getHighestUIDInFolder returns the highest UID we have stored for this folder
func (c *imapClient) getHighestUIDInFolder(folderID int64) (uint32, error) {
	uid, err := c.dbClient.GetHighestUIDInFolder(context.Background(), folderID)
	if err != nil {
		// No emails in folder yet
		return 0, nil
	}

	return uint32(uid), nil
}

// Close logs out our user and closes the connection
func (c *imapClient) Close() error {
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
