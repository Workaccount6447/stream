package main

import (
	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/bot"
	"EverythingSuckz/fsb/internal/cache"
	"EverythingSuckz/fsb/internal/database"
	"EverythingSuckz/fsb/internal/routes"
	"EverythingSuckz/fsb/internal/utils"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the streaming server.",
	Run:   runApp,
}

var startTime = time.Now()

func runApp(cmd *cobra.Command, args []string) {
	utils.InitLogger(false)
	config.Load(utils.Logger, cmd)
	utils.InitLogger(config.ValueOf.Dev)
	log := utils.Logger
	mainLogger := log.Named("Main")
	mainLogger.Info("Starting stream server")

	if err := database.Init(config.ValueOf.DatabaseURL); err != nil {
		log.Sugar().Fatalf("MongoDB connection failed (DATABASE_URL is required): %v", err)
	}
	mainLogger.Info("MongoDB connected")

	cache.InitCache(log)

	mainBot, err := bot.StartClient(log)
	if err != nil {
		log.Sugar().Fatalf("Failed to start main client: %v", err)
	}
	workers, err := bot.StartWorkers(log)
	if err != nil {
		log.Sugar().Fatalf("Failed to start workers: %v", err)
	}
	workers.AddDefaultClient(mainBot, mainBot.Self)
	bot.StartUserBot(log)

	router := getRouter(log)
	mainLogger.Sugar().Infof("Stream server %s running at %s", versionString, config.ValueOf.Host)
	if err := router.Run(fmt.Sprintf(":%d", config.ValueOf.Port)); err != nil {
		mainLogger.Sugar().Fatalf("Server failed to start: %v", err)
	}
}

func getRouter(log *zap.Logger) *gin.Engine {
	if config.ValueOf.Dev {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}
	router := gin.Default()
	router.Use(gin.ErrorLogger())
	router.GET("/", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{
			"message": "Server is running.",
			"ok":      true,
			"uptime":  utils.TimeFormat(uint64(time.Since(startTime).Seconds())),
			"version": versionString,
		})
	})
	router.HEAD("/uptime", func(ctx *gin.Context) { ctx.Status(http.StatusOK) })
	routes.Load(log, router)
	return router
}
