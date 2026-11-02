package routes

import (
	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/bot"
	"EverythingSuckz/fsb/internal/database"
	"EverythingSuckz/fsb/internal/stream"
	"EverythingSuckz/fsb/internal/types"
	"EverythingSuckz/fsb/internal/utils"
	"EverythingSuckz/fsb/internal/web"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gotd/td/tg"
	range_parser "github.com/quantumsheep/range-parser"
	"go.uber.org/zap"
)

var log *zap.Logger

func (e *allRoutes) LoadHome(r *Route) {
	log = e.log.Named("Stream")
	defer log.Info("Loaded stream route")
	// HEAD must be registered explicitly: players, browsers and Telegram's link
	// preview all probe with HEAD first, and gin does NOT answer HEAD for GET routes.
	r.Engine.GET("/stream/:id", getStreamRoute)
	r.Engine.HEAD("/stream/:id", getStreamRoute)
}

func isNumeric(s string) bool {
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}

func getStreamRoute(ctx *gin.Context) {
	w := ctx.Writer
	id := ctx.Param("id")
	authHash := ctx.Query("hash")
	if authHash == "" {
		web.Error(w, http.StatusBadRequest, "Invalid link", "This link is incomplete (the hash parameter is missing). Please copy the full link from the bot.")
		return
	}

	var (
		worker    *bot.Worker
		channelID = config.ValueOf.LogChannelID
		hashAcc   int64
		msgID     int
		legacy    bool
		linkFile  string
	)

	if isNumeric(id) {
		// ---- legacy link: /stream/<messageID>?hash=…  (owner channel, never expires)
		legacy = true
		msgID, _ = strconv.Atoi(id)
		worker = bot.GetNextWorker()
	} else {
		// ---- new link: /stream/<linkID>?hash=…
		if !utils.CheckLinkHash(authHash, id) {
			web.Error(w, http.StatusForbidden, "Invalid link", "The security hash of this link is wrong. Please copy the full link from the bot.")
			return
		}
		db := database.GetDB()
		link, err := db.GetLink(ctx, id)
		if err != nil {
			if errors.Is(err, database.ErrLinkNotFound) {
				// signature is valid but the record is gone => it was issued and then removed
				web.Expired(w, "")
				return
			}
			log.Error("db lookup failed", zap.Error(err))
			web.Error(w, http.StatusServiceUnavailable, "Temporarily unavailable", "Could not reach the database. Please try again in a moment.")
			return
		}
		if link.Expired(time.Now()) {
			web.Expired(w, link.FileName)
			return
		}
		channelID, hashAcc, msgID, linkFile = link.ChannelID, link.ChannelAccessHash, link.MessageID, link.FileName
		if link.Custom {
			worker = bot.GetDefaultWorker()
		} else {
			worker = bot.GetNextWorker()
		}
	}

	file, err := utils.TimeFuncWithResult(log, "FileFromChannel", func() (*types.File, error) {
		return utils.FileFromChannel(ctx, worker.Client, channelID, hashAcc, msgID)
	})
	if err != nil {
		if errors.Is(err, utils.ErrFileGone) {
			web.Expired(w, linkFile)
			return
		}
		log.Error("could not load file", zap.Error(err), zap.Int("msg", msgID), zap.Int64("channel", channelID))
		web.Error(w, http.StatusBadGateway, "Could not load file",
			"Telegram did not return this file. If it is stored in your own channel, make sure the bot is still an admin there.")
		return
	}

	if legacy {
		expected := utils.PackFileLegacy(file.FileName, file.FileSize, file.MimeType, file.ID)
		if !utils.CheckLegacyHash(authHash, expected) {
			web.Error(w, http.StatusForbidden, "Invalid link", "The security hash of this link is wrong. Please copy the full link from the bot.")
			return
		}
	}

	serveFile(ctx, worker, file)
}

func isActiveContent(mime string) bool {
	m := strings.ToLower(mime)
	return strings.HasPrefix(m, "text/html") || strings.Contains(m, "svg") ||
		strings.Contains(m, "xml") || strings.Contains(m, "javascript")
}

func serveFile(ctx *gin.Context, worker *bot.Worker, file *types.File) {
	w := ctx.Writer
	r := ctx.Request

	isDownload := ctx.Query("d") == "true"
	disposition := "inline"
	if isDownload {
		disposition = "attachment"
	}
	mimeType := file.MimeType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	if isDownload && !strings.HasPrefix(mimeType, "image/") {
		mimeType = "application/octet-stream"
	}
	name := utils.DisplayName(file)

	h := w.Header()
	h.Set("Content-Type", mimeType)
	h.Set("Content-Disposition", utils.ContentDisposition(disposition, name))
	h.Set("Accept-Ranges", "bytes")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, max-age=300")
	if !isDownload && isActiveContent(mimeType) {
		// user-uploaded HTML/SVG must never run scripts on our origin
		h.Set("Content-Security-Policy", "sandbox")
	}

	// Unknown size (should be rare): buffer it.
	if file.FileSize <= 0 {
		data, err := fetchSmall(ctx, worker, file)
		if err != nil {
			web.Error(w, http.StatusBadGateway, "Could not load file", err.Error())
			return
		}
		h.Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
		return
	}

	start, end := int64(0), file.FileSize-1
	status := http.StatusOK
	if rh := r.Header.Get("Range"); rh != "" {
		ranges, err := range_parser.Parse(file.FileSize, rh)
		if err != nil || len(ranges) == 0 {
			h.Set("Content-Range", fmt.Sprintf("bytes */%d", file.FileSize))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if len(ranges) == 1 { // multi-range is optional in the spec: serve whole file
			start, end = ranges[0].Start, ranges[0].End
			status = http.StatusPartialContent
			h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, file.FileSize))
		}
	}
	contentLength := end - start + 1
	h.Set("Content-Length", strconv.FormatInt(contentLength, 10))

	if r.Method == http.MethodHead {
		w.WriteHeader(status)
		return
	}

	pipe, err := stream.NewStreamPipe(ctx, worker.Client, file.Location, start, end, log)
	if err != nil {
		log.Error("Failed to create stream pipe", zap.Error(err))
		h.Del("Content-Length")
		h.Del("Content-Range")
		web.Error(w, http.StatusInternalServerError, "Stream error", "Could not start streaming this file.")
		return
	}
	defer pipe.Close()

	// headers first, THEN WriteHeader (the old code did it the other way round,
	// which silently dropped Content-Type / Length / Disposition).
	w.WriteHeader(status)
	if _, err := io.CopyN(w, pipe, contentLength); err != nil && !utils.IsClientDisconnectError(err) {
		log.Error("Error while copying stream", zap.Error(err))
	}
}

func fetchSmall(ctx *gin.Context, worker *bot.Worker, file *types.File) ([]byte, error) {
	const chunk = 1024 * 1024
	const maxSize = 32 * chunk
	var out []byte
	for off := 0; off < maxSize; off += chunk {
		res, err := worker.Client.API().UploadGetFile(ctx, &tg.UploadGetFileRequest{
			Location: file.Location, Offset: int64(off), Limit: chunk,
		})
		if err != nil {
			return nil, err
		}
		f, ok := res.(*tg.UploadFile)
		if !ok {
			return nil, errors.New("unexpected response from Telegram")
		}
		b := f.GetBytes()
		out = append(out, b...)
		if len(b) < chunk {
			break
		}
	}
	return out, nil
}
