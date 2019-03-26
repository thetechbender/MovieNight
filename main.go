package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/nareix/joy4/format"
	"github.com/nareix/joy4/format/rtmp"
	"github.com/zorchenhimer/MovieNight/common"
)

var (
	addr       string
	sKey       string
	stats      = newStreamStats()
	chatServer *http.Server
)

func setupSettings() error {
	var err error
	settings, err = LoadSettings("settings.json")
	if err != nil {
		return fmt.Errorf("Unable to load settings: %s", err)
	}
	if len(settings.StreamKey) == 0 {
		return fmt.Errorf("Missing stream key is settings.json")
	}

	if err = settings.SetupLogging(); err != nil {
		return fmt.Errorf("Unable to setup logger: %s", err)
	}

	// Save admin password to file
	if err = settings.Save(); err != nil {
		return fmt.Errorf("Unable to save settings: %s", err)
	}

	return nil
}

func main() {
	flag.StringVar(&addr, "l", ":8089", "host:port of the MovieNight")
	flag.StringVar(&sKey, "k", "", "Stream key, to protect your stream")
	flag.Parse()

	format.RegisterAll()

	if err := setupSettings(); err != nil {
		fmt.Printf("Error loading settings: %v\n", err)
		os.Exit(1)
	}

	// Load emotes before starting server.
	var err error
	if chat, err = newChatRoom(); err != nil {
		common.LogErrorln(err)
		os.Exit(1)
	}

	if addr != "" {
		addr = settings.ListenAddress
	}

	// A stream key was passed on the command line.  Use it, but don't save
	// it over the stream key in the settings.json file.
	if sKey != "" {
		settings.SetTempKey(sKey)
	}

	common.LogInfoln("Stream key: ", settings.GetStreamKey())
	common.LogInfoln("Admin password: ", settings.AdminPassword)
	common.LogInfoln("Listen and serve ", addr)

	server := &rtmp.Server{
		HandlePlay:    handlePlay,
		HandlePublish: handlePublish,
	}
	chatServer := &http.Server{
		Addr:           addr,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	chatServer.RegisterOnShutdown(func() { chat.Shutdown() })
	//server.RegisterOnShutdown(func() { common.LogDebugln("server shutdown callback called.") })

	// Signal handler
	exit := make(chan bool)
	go func() {
		ch := make(chan os.Signal)
		signal.Notify(ch, os.Interrupt)
		<-ch
		common.LogInfoln("Closing server")
		if settings.StreamStats {
			stats.Print()
		}

		if err := chatServer.Shutdown(context.Background()); err != nil {
			common.LogErrorf("Error shutting down chat server: %v", err)
		}

		common.LogInfoln("Shutdown() sent.  Sending exit.")
		exit <- true
	}()

	// Chat and HTTP server
	go func() {
		// Chat websocket
		mux := http.NewServeMux()
		mux.HandleFunc("/ws", wsHandler)
		mux.HandleFunc("/static/js/", wsStaticFiles)
		mux.HandleFunc("/static/css/", wsStaticFiles)
		mux.HandleFunc("/static/img/", wsImages)
		mux.HandleFunc("/static/main.wasm", wsWasmFile)
		mux.HandleFunc("/emotes/", wsEmotes)
		mux.HandleFunc("/favicon.ico", wsStaticFiles)
		mux.HandleFunc("/chat", handleIndexTemplate)
		mux.HandleFunc("/video", handleIndexTemplate)
		mux.HandleFunc("/help", handleHelpTemplate)

		mux.HandleFunc("/", handleDefault)

		chatServer.Handler = mux
		err := chatServer.ListenAndServe()
		if err != http.ErrServerClosed {
			// If the server cannot start, don't pretend we can continue.
			panic("Error trying to start chat/http server: " + err.Error())
		}
		common.LogDebugln("ChatServer closed.")
	}()

	// RTMP server
	go func() {
		err := server.ListenAndServe()
		if err != http.ErrServerClosed {
			// If the server cannot start, don't pretend we can continue.
			panic("Error trying to start rtmp server: " + err.Error())
		}
		common.LogDebugln("RTMP server closed.")
	}()

	<-exit
}
