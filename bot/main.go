package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: bot <command>")
		fmt.Println("Commands:")
		fmt.Println("  run        Start the WhatsApp bot")
		fmt.Println("  add-user   Add a new user")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		runBot()
	case "add-user":
		addUser()
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func runBot() {
	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	db, err := NewDB("data/bot.db")
	if err != nil {
		log.Fatalf("Failed to init database: %v", err)
	}
	defer db.Close()

	dbLog := waLog.Stdout("Database", "WARN", true)
	container, err := sqlstore.New(context.Background(), "sqlite", "file:data/whatsmeow.db?_pragma=foreign_keys(1)", dbLog)
	if err != nil {
		log.Fatalf("Failed to init whatsmeow store: %v", err)
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		log.Fatalf("Failed to get device: %v", err)
	}

	clientLog := waLog.Stdout("Client", "WARN", true)
	waClient := whatsmeow.NewClient(deviceStore, clientLog)

	claude := NewClaudeClient(cfg.AnthropicAPIKey)
	cal := NewCalendarClient(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURI)
	transcription := NewTranscriptionClient(cfg.TranscriptionURL)
	orchestrator := NewOrchestrator(claude, cal, transcription, db, cfg)

	handler := NewHandler(waClient, db, orchestrator)
	waClient.AddEventHandler(handler.HandleEvent)

	if waClient.Store.ID == nil {
		qrChan, _ := waClient.GetQRChannel(context.Background())
		err = waClient.Connect()
		if err != nil {
			log.Fatalf("Failed to connect: %v", err)
		}
		for evt := range qrChan {
			if evt.Event == "code" {
				fmt.Println("QR Code — scan with WhatsApp:")
				fmt.Println(evt.Code)
			} else {
				log.Printf("QR event: %s", evt.Event)
			}
		}
	} else {
		err = waClient.Connect()
		if err != nil {
			log.Fatalf("Failed to connect: %v", err)
		}
	}
	log.Println("WhatsApp connected")

	scheduler := NewScheduler(db, cal, cfg, handler.SendTextToPhone)
	scheduler.Start()
	defer scheduler.Stop()

	go startOAuthServer(cal, db, cfg)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	waClient.Disconnect()
}

func startOAuthServer(cal *CalendarClient, db *DB, cfg *Config) {
	http.HandleFunc("/oauth/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state")

		if code == "" || state == "" {
			http.Error(w, "Missing code or state", http.StatusBadRequest)
			return
		}

		token, err := cal.ExchangeCode(r.Context(), code)
		if err != nil {
			log.Printf("OAuth exchange error: %v", err)
			http.Error(w, "OAuth exchange failed", http.StatusInternalServerError)
			return
		}

		user, err := db.GetUserByPhone(state)
		if err != nil {
			http.Error(w, "User not found", http.StatusNotFound)
			return
		}

		encrypted, err := Encrypt(token.RefreshToken, cfg.EncryptionKey)
		if err != nil {
			http.Error(w, "Encryption failed", http.StatusInternalServerError)
			return
		}

		if err := db.UpdateUserCredentials(user.ID, encrypted); err != nil {
			http.Error(w, "Failed to save credentials", http.StatusInternalServerError)
			return
		}

		fmt.Fprintf(w, "Google Calendar autorizado com sucesso para %s! Pode fechar esta janela.", user.Name)
		log.Printf("OAuth completed for %s (%s)", user.Name, state)
	})

	log.Println("OAuth callback server listening on :8080")
	http.ListenAndServe(":8080", nil)
}

func addUser() {
	fs := flag.NewFlagSet("add-user", flag.ExitOnError)
	phone := fs.String("phone", "", "Phone number (e.g. 5511999999999)")
	name := fs.String("name", "", "User name")
	calendarID := fs.String("calendar", "", "Google Calendar email")
	fs.Parse(os.Args[2:])

	if *phone == "" || *name == "" || *calendarID == "" {
		fmt.Println("Usage: bot add-user --phone=5511... --name=Name --calendar=email@gmail.com")
		os.Exit(1)
	}

	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	db, err := NewDB("data/bot.db")
	if err != nil {
		log.Fatalf("Failed to init database: %v", err)
	}
	defer db.Close()

	user := &User{
		PhoneNumber:       *phone,
		Name:              *name,
		GoogleCalendarID:  *calendarID,
		GoogleCredentials: "",
	}

	if err := db.CreateUser(user); err != nil {
		log.Fatalf("Failed to create user: %v", err)
	}

	cal := NewCalendarClient(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURI)
	authURL := cal.AuthURL(*phone)

	fmt.Printf("User %s created (ID: %d)\n", *name, user.ID)
	fmt.Printf("\nSend this link to %s to authorize Google Calendar:\n%s\n", *name, authURL)
}
