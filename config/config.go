package config

import (
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var ValueOf = &config{}

type config struct {
	ApiID   int32  `envconfig:"API_ID" required:"true"`
	ApiHash string `envconfig:"API_HASH" required:"true"`
	// BOT_TOKEN must be the SAME token the bot repo uses. Files stored in a
	// user's own channel are only readable by that bot (the one the user made admin).
	BotToken string `envconfig:"BOT_TOKEN" required:"true"`
	// Owner/shared storage channel (same value as LOG_CHANNEL in the bot repo).
	LogChannelID int64 `envconfig:"LOG_CHANNEL" required:"true"`
	// Shared MongoDB with the bot repo. Required: link metadata lives here.
	DatabaseURL string `envconfig:"DATABASE_URL" required:"true"`
	// Secret used to sign links. MUST be identical in both repos.
	// Falls back to BOT_TOKEN so old deployments keep working.
	HashSecret string `envconfig:"HASH_SECRET"`

	Dev            bool   `envconfig:"DEV" default:"false"`
	Port           int    `envconfig:"PORT" default:"8080"`
	Host           string `envconfig:"HOST" default:""`
	HashLength     int    `envconfig:"HASH_LENGTH" default:"16"`
	UserSession    string `envconfig:"USER_SESSION"`
	UsePublicIP    bool   `envconfig:"USE_PUBLIC_IP" default:"false"`
	MultiTokens    []string
	BotName        string `envconfig:"BOT_NAME" default:"File to Link"`
	UpdatesChannel string `envconfig:"UPDATES_CHANNEL" default:""`

	StreamConcurrency int `envconfig:"STREAM_CONCURRENCY" default:"4"`
	StreamBufferCount int `envconfig:"STREAM_BUFFER_COUNT" default:"8"`
	StreamTimeoutSec  int `envconfig:"STREAM_TIMEOUT_SEC" default:"30"`
	StreamMaxRetries  int `envconfig:"STREAM_MAX_RETRIES" default:"3"`
}

var botTokenRegex = regexp.MustCompile(`MULTI\_TOKEN\d+=(.*)`)

func (c *config) loadFromEnvFile(log *zap.Logger) {
	envPath := filepath.Clean("fsb.env")
	err := godotenv.Load(envPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Sugar().Info("fsb.env not found, using process environment only")
		} else {
			log.Fatal("Unknown error while parsing env file.", zap.Error(err))
		}
	}
}

func SetFlagsFromConfig(cmd *cobra.Command) {
	cmd.Flags().Int32("api-id", ValueOf.ApiID, "Telegram API ID")
	cmd.Flags().String("api-hash", ValueOf.ApiHash, "Telegram API Hash")
	cmd.Flags().String("bot-token", ValueOf.BotToken, "Telegram Bot Token")
	cmd.Flags().Int64("log-channel", ValueOf.LogChannelID, "Owner storage channel ID")
	cmd.Flags().Bool("dev", ValueOf.Dev, "Enable development mode")
	cmd.Flags().IntP("port", "p", ValueOf.Port, "Server port")
	cmd.Flags().String("host", ValueOf.Host, "Public server URL")
	cmd.Flags().Int("hash-length", ValueOf.HashLength, "Hash length in links")
	cmd.Flags().String("user-session", ValueOf.UserSession, "Pyrogram user session")
	cmd.Flags().Bool("use-public-ip", ValueOf.UsePublicIP, "Use public IP instead of local IP")
}

func (c *config) loadConfigFromArgs(cmd *cobra.Command) {
	if v, _ := cmd.Flags().GetInt32("api-id"); v != 0 {
		os.Setenv("API_ID", strconv.Itoa(int(v)))
	}
	if v, _ := cmd.Flags().GetString("api-hash"); v != "" {
		os.Setenv("API_HASH", v)
	}
	if v, _ := cmd.Flags().GetString("bot-token"); v != "" {
		os.Setenv("BOT_TOKEN", v)
	}
	if v, _ := cmd.Flags().GetInt64("log-channel"); v != 0 {
		os.Setenv("LOG_CHANNEL", strconv.FormatInt(v, 10))
	}
	if v, _ := cmd.Flags().GetBool("dev"); v {
		os.Setenv("DEV", "true")
	}
	if v, _ := cmd.Flags().GetInt("port"); v != 0 {
		os.Setenv("PORT", strconv.Itoa(v))
	}
	if v, _ := cmd.Flags().GetString("host"); v != "" {
		os.Setenv("HOST", v)
	}
	if v, _ := cmd.Flags().GetInt("hash-length"); v != 0 {
		os.Setenv("HASH_LENGTH", strconv.Itoa(v))
	}
	if v, _ := cmd.Flags().GetString("user-session"); v != "" {
		os.Setenv("USER_SESSION", v)
	}
	if v, _ := cmd.Flags().GetBool("use-public-ip"); v {
		os.Setenv("USE_PUBLIC_IP", "true")
	}
}

func (c *config) setupEnvVars(log *zap.Logger, cmd *cobra.Command) {
	c.loadFromEnvFile(log)
	c.loadConfigFromArgs(cmd)
	if err := envconfig.Process("", c); err != nil {
		log.Fatal("Error while parsing env variables", zap.Error(err))
	}
	if c.Host == "" {
		ip, err := getIP(c.UsePublicIP)
		if err != nil {
			log.Error("Error while getting IP", zap.Error(err))
		}
		c.Host = "http://" + ip + ":" + strconv.Itoa(c.Port)
		log.Sugar().Info("HOST not set, automatically set to " + c.Host)
	}
	c.Host = strings.TrimRight(c.Host, "/")
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "MULTI_TOKEN") {
			if m := botTokenRegex.FindStringSubmatch(env); len(m) == 2 && m[1] != "" {
				c.MultiTokens = append(c.MultiTokens, m[1])
			}
		}
	}
	reflect.ValueOf(c).Elem().FieldByName("MultiTokens").Set(reflect.ValueOf(c.MultiTokens))
}

func Load(log *zap.Logger, cmd *cobra.Command) {
	log = log.Named("Config")
	defer log.Info("Loaded config")
	ValueOf.setupEnvVars(log, cmd)
	ValueOf.LogChannelID = StripChannelID(ValueOf.LogChannelID)
	if ValueOf.HashSecret == "" {
		ValueOf.HashSecret = ValueOf.BotToken
		log.Sugar().Warn("HASH_SECRET not set, falling back to BOT_TOKEN. Set the same HASH_SECRET in the bot repo.")
	}
	if ValueOf.HashLength < 5 || ValueOf.HashLength > 64 {
		log.Sugar().Info("HASH_LENGTH out of range (5-64), defaulting to 16")
		ValueOf.HashLength = 16
	}
	if ValueOf.StreamConcurrency <= 0 {
		ValueOf.StreamConcurrency = 4
	}
	if ValueOf.StreamBufferCount <= 0 {
		ValueOf.StreamBufferCount = 8
	}
	if ValueOf.StreamTimeoutSec <= 0 {
		ValueOf.StreamTimeoutSec = 30
	}
	if ValueOf.StreamMaxRetries <= 0 {
		ValueOf.StreamMaxRetries = 3
	}
}

// StripChannelID turns -1001234567890 (or 1234567890) into 1234567890.
func StripChannelID(id int64) int64 {
	if id < 0 {
		id = -id
	}
	s := strconv.FormatInt(id, 10)
	if strings.HasPrefix(s, "100") && len(s) > 10 {
		s = s[3:]
	}
	out, _ := strconv.ParseInt(s, 10, 64)
	return out
}

func getIP(public bool) (string, error) {
	var ip string
	var err error
	if public {
		ip, err = GetPublicIP()
	} else {
		ip, err = getInternalIP()
	}
	if ip == "" {
		ip = "localhost"
	}
	if err != nil {
		return "localhost", err
	}
	return ip, nil
}

func getInternalIP() (string, error) {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "", errors.New("no internet connection")
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String(), nil
}

func GetPublicIP() (string, error) {
	resp, err := http.Get("https://api.ipify.org?format=text")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	ip, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(ip), nil
}
