// Package database is the READ-ONLY view the streaming service has of the
// MongoDB shared with the bot repo. The bot repo owns all writes.
package database

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var ErrLinkNotFound = errors.New("link not found")

// Link must stay in sync with FileLink in the bot repo.
type Link struct {
	LinkID            string     `bson:"link_id"`
	BatchID           string     `bson:"batch_id,omitempty"`
	UserID            int64      `bson:"user_id"`
	ChannelID         int64      `bson:"channel_id"`          // stripped (no -100 prefix)
	ChannelAccessHash int64      `bson:"channel_access_hash"` // 0 for owner channel
	Custom            bool       `bson:"custom"`              // true = user's own channel
	MessageID         int        `bson:"message_id"`
	FileName          string     `bson:"file_name"`
	FileSize          int64      `bson:"file_size"`
	MimeType          string     `bson:"mime_type"`
	CreatedAt         time.Time  `bson:"created_at"`
	ExpiresAt         *time.Time `bson:"expires_at,omitempty"` // nil = never expires
}

func (l *Link) Expired(now time.Time) bool {
	return l.ExpiresAt != nil && !l.ExpiresAt.After(now)
}

type DB struct {
	client *mongo.Client
	links  *mongo.Collection
}

var instance *DB

func Init(uri string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return err
	}
	if err := client.Ping(ctx, nil); err != nil {
		return err
	}
	instance = &DB{client: client, links: client.Database("fsb").Collection("links")}
	return nil
}

func GetDB() *DB { return instance }

// GetLink returns ErrLinkNotFound if the link never existed OR was deleted
// (expired / revoked / cleared). The caller tells those apart with the hash.
func (d *DB) GetLink(ctx context.Context, linkID string) (*Link, error) {
	var l Link
	err := d.links.FindOne(ctx, bson.M{"link_id": linkID}).Decode(&l)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrLinkNotFound
	}
	return &l, err
}

// GetLegacyLink finds an old-style record (message in LOG_CHANNEL, no link_id).
func (d *DB) HasLegacyLink(ctx context.Context, messageID int) bool {
	n, _ := d.links.CountDocuments(ctx, bson.M{"message_id": messageID, "link_id": bson.M{"$exists": false}})
	return n > 0
}
