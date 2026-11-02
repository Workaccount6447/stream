package utils

import (
	"EverythingSuckz/fsb/internal/cache"
	"EverythingSuckz/fsb/internal/types"
	"context"
	"errors"
	"fmt"
	"mime"
	"strings"
	"unicode/utf8"

	"github.com/celestix/gotgproto"
	"github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

// ErrFileGone means the message was deleted or has no downloadable media.
var ErrFileGone = errors.New("file is no longer available")

func IsClientDisconnectError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "connection was aborted") ||
		strings.Contains(s, "connection reset by peer") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "forcibly closed") ||
		strings.Contains(s, "context canceled")
}

// ---- channel / message access ---------------------------------------------

func resolveChannel(ctx context.Context, client *gotgproto.Client, channelID, accessHash int64, forceFetch bool) (*tg.InputChannel, error) {
	if !forceFetch {
		if peer, ok := client.PeerStorage.GetInputPeerById(channelID).(*tg.InputPeerChannel); ok {
			return &tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash}, nil
		}
		if accessHash != 0 {
			return &tg.InputChannel{ChannelID: channelID, AccessHash: accessHash}, nil
		}
	}
	res, err := client.API().ChannelsGetChannels(ctx, []tg.InputChannelClass{&tg.InputChannel{ChannelID: channelID}})
	if err != nil {
		return nil, err
	}
	if len(res.GetChats()) == 0 {
		return nil, errors.New("channel not found (is the bot a member/admin?)")
	}
	ch, ok := res.GetChats()[0].(*tg.Channel)
	if !ok {
		return nil, errors.New("not a channel")
	}
	client.PeerStorage.AddPeer(ch.GetID(), ch.AccessHash, storage.TypeChannel, "")
	return ch.AsInput(), nil
}

func GetTGMessage(ctx context.Context, client *gotgproto.Client, channelID, accessHash int64, messageID int) (*tg.Message, error) {
	for attempt := 0; attempt < 2; attempt++ {
		channel, err := resolveChannel(ctx, client, channelID, accessHash, attempt == 1)
		if err != nil {
			return nil, err
		}
		res, err := client.API().ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: channel,
			ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}},
		})
		if err != nil {
			if attempt == 0 && (strings.Contains(err.Error(), "CHANNEL_INVALID") || strings.Contains(err.Error(), "ACCESS_HASH")) {
				continue
			}
			return nil, err
		}
		var msgs []tg.MessageClass
		switch r := res.(type) {
		case *tg.MessagesChannelMessages:
			msgs = r.Messages
		case *tg.MessagesMessages:
			msgs = r.Messages
		case *tg.MessagesMessagesSlice:
			msgs = r.Messages
		default:
			return nil, fmt.Errorf("unexpected response %T", res)
		}
		if len(msgs) == 0 {
			return nil, ErrFileGone
		}
		m, ok := msgs[0].(*tg.Message)
		if !ok { // tg.MessageEmpty => deleted
			return nil, ErrFileGone
		}
		return m, nil
	}
	return nil, ErrFileGone
}

// ---- media -> File ----------------------------------------------------------

func FileFromMedia(media tg.MessageMediaClass) (*types.File, error) {
	switch media := media.(type) {
	case *tg.MessageMediaDocument:
		document, ok := media.Document.AsNotEmpty()
		if !ok {
			return nil, ErrFileGone
		}
		var fileName string
		for _, attribute := range document.Attributes {
			if name, ok := attribute.(*tg.DocumentAttributeFilename); ok {
				fileName = name.FileName
				break
			}
		}
		return &types.File{
			Location: document.AsInputDocumentFileLocation(),
			FileSize: document.Size,
			FileName: fileName,
			MimeType: document.MimeType,
			ID:       document.ID,
		}, nil
	case *tg.MessageMediaPhoto:
		photo, ok := media.Photo.AsNotEmpty()
		if !ok {
			return nil, ErrFileGone
		}
		// pick the biggest real size (the last entry is not always the largest)
		var bestType string
		var bestBytes int64
		for _, s := range photo.Sizes {
			var b int64
			var t string
			switch v := s.(type) {
			case *tg.PhotoSize:
				b, t = int64(v.Size), v.Type
			case *tg.PhotoSizeProgressive:
				for _, x := range v.Sizes {
					if int64(x) > b {
						b = int64(x)
					}
				}
				t = v.Type
			default:
				continue
			}
			if b >= bestBytes {
				bestBytes, bestType = b, t
			}
		}
		if bestType == "" {
			return nil, errors.New("photo has no downloadable size")
		}
		return &types.File{
			Location: &tg.InputPhotoFileLocation{
				ID:            photo.GetID(),
				AccessHash:    photo.GetAccessHash(),
				FileReference: photo.GetFileReference(),
				ThumbSize:     bestType,
			},
			FileSize: bestBytes,
			FileName: fmt.Sprintf("photo_%d.jpg", photo.GetID()),
			MimeType: "image/jpeg",
			ID:       photo.GetID(),
		}, nil
	}
	return nil, ErrFileGone
}

// FileFromChannel fetches (and briefly caches) file info for a channel message.
// Cache TTL is short because Telegram file references expire.
func FileFromChannel(ctx context.Context, client *gotgproto.Client, channelID, accessHash int64, messageID int) (*types.File, error) {
	key := fmt.Sprintf("file:%d:%d:%d", channelID, messageID, client.Self.ID)
	log := Logger.Named("GetMessageMedia")
	var cached types.File
	if err := cache.GetCache().Get(key, &cached); err == nil {
		return &cached, nil
	}
	msg, err := GetTGMessage(ctx, client, channelID, accessHash, messageID)
	if err != nil {
		return nil, err
	}
	file, err := FileFromMedia(msg.Media)
	if err != nil {
		return nil, err
	}
	if err := cache.GetCache().Set(key, file, 300); err != nil {
		log.Warn("cache set failed", zap.Error(err))
	}
	return file, nil
}

// ---- HTTP header helpers ---------------------------------------------------

var mimeExt = map[string]string{
	"video/mp4": ".mp4", "video/x-matroska": ".mkv", "video/webm": ".webm", "video/quicktime": ".mov",
	"audio/mpeg": ".mp3", "audio/ogg": ".ogg", "audio/mp4": ".m4a", "audio/x-wav": ".wav", "audio/flac": ".flac",
	"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "image/gif": ".gif",
	"application/pdf": ".pdf", "application/zip": ".zip", "application/x-tgsticker": ".tgs",
	"application/vnd.android.package-archive": ".apk",
}

// DisplayName never returns an empty / header-unsafe name.
func DisplayName(f *types.File) string {
	name := strings.TrimSpace(f.FileName)
	if name == "" {
		ext := mimeExt[strings.ToLower(strings.SplitN(f.MimeType, ";", 2)[0])]
		if ext == "" {
			if exts, _ := mime.ExtensionsByType(f.MimeType); len(exts) > 0 {
				ext = exts[0]
			}
		}
		name = fmt.Sprintf("file_%d%s", f.ID, ext)
	}
	return name
}

// ContentDisposition builds an RFC 6266 / 5987 header that survives quotes,
// newlines and non-ASCII names (these used to produce broken responses).
func ContentDisposition(disposition, name string) string {
	var clean strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f:
			clean.WriteRune(' ')
		case r == '"' || r == '\\' || r == '/':
			clean.WriteRune('_')
		case !utf8.ValidRune(r):
			clean.WriteRune('_')
		default:
			clean.WriteRune(r)
		}
	}
	n := strings.TrimSpace(clean.String())
	if n == "" {
		n = "file"
	}
	var ascii strings.Builder
	for _, r := range n {
		if r < 0x80 {
			ascii.WriteRune(r)
		} else {
			ascii.WriteRune('_')
		}
	}
	return fmt.Sprintf("%s; filename=\"%s\"; filename*=UTF-8''%s", disposition, ascii.String(), rfc5987(n))
}

func rfc5987(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("!#$&+-.^_`|~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
