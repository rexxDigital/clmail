package accounts

import (
	"context"
	"fmt"
	"github.com/rexxDigital/clmail/internal/db"
	"github.com/rexxDigital/clmail/internal/imap"
	"github.com/zalando/go-keyring"
	"log"
)

const serviceName = "clmail"

func CreateAccount(account db.CreateAccountParams, password string, dbClient *db.Client) error {
	folders, err := imap.TestLoginAndGetFolders(db.Account{
		Email:        account.ImapUsername,
		ImapUsername: account.ImapUsername,
		ImapServer:   account.ImapServer,
		ImapPort:     account.ImapPort,
		ImapUseSsl:   true,
	}, password, dbClient)

	if err != nil {
		folders, err = imap.TestLoginAndGetFolders(db.Account{
			Email:        account.Email,
			ImapUsername: account.Email,
			ImapServer:   account.ImapServer,
			ImapPort:     account.ImapPort,
			ImapUseSsl:   true,
		}, password, dbClient)
		account.ImapUsername = account.Email

		if err != nil {
			log.Printf("[ACCOUNTS::CreateAccount] Failed to create account: %v", err)
			return fmt.Errorf("invalid credentials")
		}
	}

	err = keyring.Set(serviceName, account.Email, password)
	if err != nil {
		return err
	}

	newAccount, err := dbClient.CreateAccount(context.Background(), account)
	if err != nil {
		keyring.Delete(serviceName, account.Email)
		return err
	}

	for _, folder := range folders {
		_, err = dbClient.CreateFolder(context.Background(), db.CreateFolderParams{
			AccountID: newAccount.ID,
			Name:      folder,
		})

		if err != nil {
			err = keyring.Delete(serviceName, account.Email)
			if err != nil {
				return err
			}
			err = dbClient.DeleteAccount(context.Background(), newAccount.ID)
			if err != nil {
				return err
			}
			return err
		}
	}

	return nil
}

func GetPassword(email string) (string, error) {
	return keyring.Get(serviceName, email)
}
