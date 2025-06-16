package smtp

import (
	"fmt"
	"github.com/rexxDigital/clmail/internal/db"
	"github.com/rexxDigital/clmail/internal/imap"
	"github.com/rexxDigital/clmail/types"
	"net/smtp"
	"strings"
	"time"
)

func SendMail(mail types.Mail, account *db.Account, password string, dbClient *db.Client) error {
	auth := smtp.PlainAuth("", account.SmtpUsername, password, account.SmtpServer)

	receivers := append(mail.CC, mail.To)

	var message strings.Builder

	// necessary headers :)
	if account.Name != "" {
		message.WriteString(fmt.Sprintf("From: %s <%s>\r\n", account.Name, account.Email))
	} else if account.DisplayName != "" {
		message.WriteString(fmt.Sprintf("From: %s <%s>\r\n", account.DisplayName, account.Email))
	} else {
		message.WriteString(fmt.Sprintf("From: %s\r\n", account.Email))
	}
	message.WriteString(fmt.Sprintf("To: %s\r\n", mail.To))

	if len(mail.CC) > 0 {
		message.WriteString(fmt.Sprintf("Cc: %s\r\n", strings.Join(mail.CC, ", ")))
	}

	message.WriteString(fmt.Sprintf("Subject: %s\r\n", mail.Subject))
	message.WriteString(fmt.Sprintf("Date: %s\r\n", mail.Date.Format(time.RFC1123Z)))

	message.WriteString(fmt.Sprintf("Message-ID: %s\r\n", mail.MessageID))

	message.WriteString("MIME-Version: 1.0\r\n")
	message.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")

	if mail.InReplyTo != "" {
		message.WriteString(fmt.Sprintf("In-Reply-To: %s\r\n", "<"+mail.InReplyTo+">"))
		message.WriteString(fmt.Sprintf("References: %s\r\n", mail.References))
	}

	message.WriteString(fmt.Sprintf("User-Agent: clmail/1.0\r\n"))

	// X headers
	message.WriteString("X-Mailer: clmail/1.0\r\n")
	message.WriteString("X-Original-To: " + mail.To + "\r\n")

	message.WriteString("\r\n")
	message.WriteString(mail.Body)

	err := smtp.SendMail(
		fmt.Sprintf("%s:%d", account.SmtpServer, account.SmtpPort),
		auth,
		account.Email,
		receivers,
		[]byte(message.String()))

	if err != nil {
		return err
	}

	syncClient, err := imap.NewSyncClient(*account, password, dbClient)
	if err != nil {
		return err
	}
	//defer syncClient.Close()

	err = syncClient.SaveSent(message.String(), mail.Date)
	if err != nil {
		return err
	}

	return nil
}
