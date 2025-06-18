package services

import (
	"context"
	"fmt"
	"log"

	"github.com/rexxDigital/clmail/internal/accounts"
	"github.com/rexxDigital/clmail/internal/db"
	"github.com/rexxDigital/clmail/internal/imap"
	"github.com/rexxDigital/clmail/internal/services/sync"
)

type EmailService interface {
	InitializeAccount(account db.Account) error
	InitializeAllAccounts() error
	Close()
	GetAllClients() map[int64]*EmailClient
	HasAccount(accountID int64) bool
	GetClient(accountID int64) (*EmailClient, bool)
}

type emailService struct {
	dbClient *db.Client
	clients  map[int64]*EmailClient
}

type EmailClient struct {
	IdleClient imap.IdleClient
	SyncClient sync.Syncer
	Account    db.Account
}

func NewEmailService(dbClient *db.Client) EmailService {
	return &emailService{
		dbClient: dbClient,
		clients:  make(map[int64]*EmailClient),
	}
}

func (es *emailService) InitializeAccount(account db.Account) error {
	if _, exists := es.clients[account.ID]; exists {
		return nil
	}

	password, err := accounts.GetPassword(account.Email)
	if err != nil {
		return fmt.Errorf("failed to get password for %s: %w", account.Email, err)
	}

	idleClient, err := imap.NewIdleClient(account, password, es.dbClient)
	if err != nil {
		return fmt.Errorf("failed to init imap client: %w", err)
	}

	syncClient := sync.NewSyncService(account, password, es.dbClient)
	syncClient.Start()

	go idleClient.Idle("INBOX")
	go syncClient.InitSync()

	es.clients[account.ID] = &EmailClient{
		IdleClient: idleClient,
		SyncClient: syncClient,
		Account:    account,
	}

	return nil
}

func (es *emailService) GetClient(accountID int64) (*EmailClient, bool) {
	client, exists := es.clients[accountID]
	return client, exists
}

// building this to work with account switching even though we do not have it implemented yet.
func (es *emailService) InitializeAllAccounts() error {
	accounts, err := es.dbClient.ListAccounts(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get accounts: %w", err)
	}

	for _, account := range accounts {
		if err := es.InitializeAccount(account); err != nil {
			log.Printf("Failed to initialize account %s: %v", account.Email, err)
		}
	}

	return nil
}

func (es *emailService) Close() {
	for accountID, client := range es.clients {
		if client.IdleClient != nil {
			if err := client.IdleClient.Close(); err != nil {
				log.Printf("Failed to close idle client for account %d: %v", accountID, err)
			}
		}
		if client.SyncClient != nil {
			client.SyncClient.Close()
		}
	}

	es.clients = make(map[int64]*EmailClient)
}

func (es *emailService) GetAllClients() map[int64]*EmailClient {
	result := make(map[int64]*EmailClient)
	for id, client := range es.clients {
		result[id] = client
	}
	return result
}

func (es *emailService) HasAccount(accountID int64) bool {
	_, exists := es.clients[accountID]
	return exists
}
