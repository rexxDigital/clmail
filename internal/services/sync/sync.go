package sync

import (
	"context"
	"github.com/rexxDigital/clmail/internal/db"
	"github.com/rexxDigital/clmail/internal/imap"
	"log"
	"sync"
	"time"
)

type Syncer interface {
	Start()
	Stop()
	InitSync()
	GetStatus() Status
}

type Status struct {
	IsRunning    bool
	LastSync     time.Time
	ActiveFolder string
	//TODO: Add visuals to what folder is being synced in tui
}

type syncer struct {
	account   db.Account
	password  string
	dbClient  *db.Client
	syncQueue chan string
	isRunning bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewSyncService(account db.Account, password string, dbClient *db.Client) Syncer {
	return &syncer{
		account:   account,
		password:  password,
		dbClient:  dbClient,
		syncQueue: make(chan string, 10),
	}
}

func (s *syncer) Start() {
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.isRunning = true

	s.wg.Add(2)
	go s.syncerWorker()
	go s.syncerScheduler()
}

func (s *syncer) Stop() {
	if s.cancel != nil {
		s.cancel()
	}

	s.wg.Wait()

	s.isRunning = false
}

func (s *syncer) InitSync() {
	s.queueAllFolders()
}

func (s *syncer) GetStatus() Status {
	return Status{}
}

func (s *syncer) syncerWorker() {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return
		case folder := <-s.syncQueue:
			s.syncFolder(folder)
		}
	}
}

func (s *syncer) syncerScheduler() {
	defer s.wg.Done()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.queueAllFolders()
		}
	}
}

// queueAllFolders add all our folder names to the syncQueue channel
func (s *syncer) queueAllFolders() {
	folders, err := s.dbClient.ListFolders(context.Background(), s.account.ID)
	if err != nil {
		log.Printf("Failed to get folders: %v", err)
		return
	}

	for _, folder := range folders {
		s.syncQueue <- folder.Name
	}
}

func (s *syncer) syncFolder(folder string) {
	client, err := imap.NewSyncClient(s.account, s.password, s.dbClient)
	if err != nil {
		log.Printf("Failed to create imap client: %v", err)
		return
	}
	defer client.Close()

	err = client.SyncFolder(folder)
	if err != nil {
		log.Printf("Failed to sync folder: %v", err)
		return
	}
}
