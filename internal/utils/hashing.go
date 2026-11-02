package utils

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"strconv"

	"EverythingSuckz/fsb/config"
)

// LinkHash signs a link ID. Identical function lives in the bot repo.
// Because it only depends on the link ID, a signed URL stays verifiable even
// after its DB record is deleted — that is how the "expired" page knows the
// link was genuinely issued by us.
func LinkHash(linkID string) string {
	mac := hmac.New(sha256.New, []byte(config.ValueOf.HashSecret))
	mac.Write([]byte("link:" + linkID))
	return hex.EncodeToString(mac.Sum(nil))
}

func ShortHash(full string) string { return full[:config.ValueOf.HashLength] }

func CheckLinkHash(input, linkID string) bool {
	return hmac.Equal([]byte(input), []byte(ShortHash(LinkHash(linkID))))
}

// PackFileLegacy reproduces the pre-split hash so links created before the
// upgrade (…/stream/<messageID>?hash=…) keep working.
func PackFileLegacy(fileName string, fileSize int64, mimeType string, fileID int64) string {
	h := md5.New()
	h.Write([]byte(fileName))
	h.Write([]byte(strconv.FormatInt(fileSize, 10)))
	h.Write([]byte(mimeType))
	h.Write([]byte(strconv.FormatInt(fileID, 10)))
	mac := hmac.New(sha256.New, []byte(config.ValueOf.BotToken))
	mac.Write([]byte(hex.EncodeToString(h.Sum(nil))))
	return hex.EncodeToString(mac.Sum(nil))
}

func CheckLegacyHash(input, fullHash string) bool {
	return hmac.Equal([]byte(input), []byte(ShortHash(fullHash)))
}
