package bot

import (
	"EverythingSuckz/fsb/config"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/celestix/gotgproto"
	"github.com/celestix/gotgproto/sessionMaker"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

type Worker struct {
	ID     int
	Client *gotgproto.Client
	Self   *tg.User
	log    *zap.Logger
}

func (w *Worker) String() string {
	return fmt.Sprintf("{Worker (%d|@%s)}", w.ID, w.Self.Username)
}

type BotWorkers struct {
	// Default is the main BOT_TOKEN client. Files in a user's own channel can
	// only be read by this bot (it is the one the user added as admin).
	Default  *Worker
	Bots     []*Worker
	starting int
	index    int
	mut      sync.Mutex
	log      *zap.Logger
}

var Workers *BotWorkers = &BotWorkers{
	log:  nil,
	Bots: make([]*Worker, 0),
}

func (w *BotWorkers) Init(log *zap.Logger) {
	w.log = log.Named("Workers")
}

func (w *BotWorkers) AddDefaultClient(client *gotgproto.Client, self *tg.User) {
	if w.Bots == nil {
		w.Bots = make([]*Worker, 0)
	}
	w.incStarting()
	worker := &Worker{
		Client: client,
		ID:     w.starting,
		Self:   self,
		log:    w.log,
	}
	w.Default = worker
	w.Bots = append(w.Bots, worker)
	w.log.Sugar().Info("Default bot loaded")
}

func (w *BotWorkers) incStarting() {
	w.mut.Lock()
	defer w.mut.Unlock()
	w.starting++
}

func (w *BotWorkers) Add(token string) (err error) {
	w.incStarting()
	var botID int = w.starting
	client, err := startWorker(w.log, token, botID)
	if err != nil {
		return err
	}
	w.log.Sugar().Infof("Bot @%s loaded with ID %d", client.Self.Username, botID)
	w.mut.Lock()
	defer w.mut.Unlock()
	w.Bots = append(w.Bots, &Worker{
		Client: client,
		ID:     botID,
		Self:   client.Self,
		log:    w.log,
	})
	return nil
}

// GetDefaultWorker returns the BOT_TOKEN client (used for user-owned channels).
func GetDefaultWorker() *Worker {
	if Workers.Default != nil {
		return Workers.Default
	}
	return GetNextWorker()
}

func GetNextWorker() *Worker {
	Workers.mut.Lock()
	defer Workers.mut.Unlock()
	index := (Workers.index + 1) % len(Workers.Bots)
	Workers.index = index
	worker := Workers.Bots[index]
	Workers.log.Sugar().Debugf("Using worker %d", worker.ID)
	return worker
}

func StartWorkers(log *zap.Logger) (*BotWorkers, error) {
	Workers.Init(log)

	if len(config.ValueOf.MultiTokens) == 0 {
		Workers.log.Sugar().Info("No worker bot tokens provided, skipping worker initialization")
		return Workers, nil
	}
	Workers.log.Sugar().Info("Starting")

	var wg sync.WaitGroup
	var successfulStarts int32
	totalBots := len(config.ValueOf.MultiTokens)

	for i := 0; i < totalBots; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			done := make(chan error, 1)
			go func() {
				err := Workers.Add(config.ValueOf.MultiTokens[i])
				done <- err
			}()

			select {
			case err := <-done:
				if err != nil {
					Workers.log.Error("Failed to start worker", zap.Int("index", i), zap.Error(err))
				} else {
					atomic.AddInt32(&successfulStarts, 1)
				}
			case <-ctx.Done():
				Workers.log.Error("Timed out starting worker", zap.Int("index", i))
			}
		}(i)
	}

	wg.Wait()
	Workers.log.Sugar().Infof("Successfully started %d/%d bots", successfulStarts, totalBots)
	return Workers, nil
}

func startWorker(l *zap.Logger, botToken string, index int) (*gotgproto.Client, error) {
	log := l.Named("Worker").Sugar()
	log.Infof("Starting worker with index - %d", index)

	// All workers use SimpleSession — no SQLite files, no sessions/ directory.
	// Bot tokens re-auth instantly on every start, so no session persistence needed.
	client, err := gotgproto.NewClient(
		int(config.ValueOf.ApiID),
		config.ValueOf.ApiHash,
		gotgproto.ClientTypeBot(botToken),
		&gotgproto.ClientOpts{
			Session:          sessionMaker.SimpleSession(),
			DisableCopyright: true,
			Middlewares:      GetFloodMiddleware(log.Desugar()),
		},
	)
	if err != nil {
		return nil, err
	}
	return client, nil
}
