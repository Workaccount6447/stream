package bot

import (
	"EverythingSuckz/fsb/config"
	"context"
	"time"

	"github.com/celestix/gotgproto"
	"github.com/celestix/gotgproto/sessionMaker"
	"go.uber.org/zap"
)

// StartClient starts the main BOT_TOKEN client. In the streaming repo it has
// NO command handlers — the bot repo owns all user interaction.
func StartClient(log *zap.Logger) (*gotgproto.Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	type result struct {
		client *gotgproto.Client
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		client, err := gotgproto.NewClient(
			int(config.ValueOf.ApiID),
			config.ValueOf.ApiHash,
			gotgproto.ClientTypeBot(config.ValueOf.BotToken),
			&gotgproto.ClientOpts{
				Session:          sessionMaker.SimpleSession(),
				DisableCopyright: true,
				Middlewares:      GetFloodMiddleware(log),
			},
		)
		ch <- result{client, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		log.Info("Client started", zap.String("username", r.client.Self.Username))
		return r.client, nil
	}
}
