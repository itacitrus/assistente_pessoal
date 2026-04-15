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
	"time"

	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: bot <command>")
		fmt.Println("Commands:")
		fmt.Println("  run            Start the WhatsApp bot")
		fmt.Println("  add-user       Add a new user")
		fmt.Println("  remove-user    Remove a user")
		fmt.Println("  update-user    Update a user's name")
		fmt.Println("  grant-access   Grant a user permission to schedule on another's calendar")
		fmt.Println("  revoke-access  Revoke a user's permission to schedule on another's calendar")
		fmt.Println("  list-access    List users a given user can schedule for")
		fmt.Println("  test-birthday  Create and delete a birthday event to validate Google API constraints")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		runBot()
	case "add-user":
		addUser()
	case "remove-user":
		removeUser()
	case "update-user":
		updateUser()
	case "grant-access":
		grantAccess()
	case "revoke-access":
		revokeAccess()
	case "list-access":
		listAccess()
	case "test-birthday":
		testBirthday()
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

// testBirthday creates a one-off birthday event and deletes it immediately.
// Lets us validate Google's eventType=birthday constraints without burning
// Anthropic tokens on agent retries. Usage:
//
//	bot test-birthday --phone=5561... --date=2026-12-31 --title="Teste"
//
// Prints the full Google API error on failure so we can see which constraint
// is missing. On success, deletes the event and confirms.
func testBirthday() {
	fs := flag.NewFlagSet("test-birthday", flag.ExitOnError)
	phone := fs.String("phone", "", "Phone number of a registered user (whose credentials we use)")
	date := fs.String("date", "", "YYYY-MM-DD (any date — the birthday will recur yearly)")
	title := fs.String("title", "TESTE - Aniversario Dummy", "Event title")
	keep := fs.Bool("keep", false, "If set, don't delete the event after creating (useful for inspecting in UI)")
	fs.Parse(os.Args[2:])

	if *phone == "" || *date == "" {
		fmt.Println("Usage: bot test-birthday --phone=5561... --date=2026-12-31 [--title=...] [--keep]")
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

	user, err := db.GetUserByPhone(*phone)
	if err != nil {
		log.Fatalf("User %s not found: %v", *phone, err)
	}
	refreshToken, err := Decrypt(user.GoogleCredentials, cfg.EncryptionKey)
	if err != nil {
		log.Fatalf("Decrypt credentials: %v", err)
	}

	bdayStart, err := time.ParseInLocation(dateLayout, *date, BRT())
	if err != nil {
		log.Fatalf("Parse date %q: %v", *date, err)
	}

	cal := NewCalendarClient(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURI)
	ev := CalendarEvent{
		Title:     *title,
		Start:     bdayStart,
		End:       bdayStart.AddDate(0, 0, 1),
		EventType: "birthday",
	}

	fmt.Printf("Creating birthday event for %s on %s...\n", user.Name, bdayStart.Format("2006-01-02"))
	created, err := cal.CreateEvent(context.Background(), refreshToken, user.GoogleCalendarID, ev)
	if err != nil {
		fmt.Printf("\n❌ FAILED: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Created event %s (%q)\n", created.ID, created.Title)

	if *keep {
		fmt.Println("--keep set, leaving event in calendar for inspection.")
		return
	}

	fmt.Printf("Deleting event %s...\n", created.ID)
	if err := cal.DeleteEvent(context.Background(), refreshToken, user.GoogleCalendarID, created.ID); err != nil {
		fmt.Printf("⚠️  Create worked, delete failed: %v\n", err)
		fmt.Printf("   The event is still in calendar — delete manually.\n")
		os.Exit(2)
	}
	fmt.Println("✅ Deleted. Birthday constraints are satisfied.")
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
	container, err := sqlstore.New(context.Background(), "sqlite", "file:data/whatsmeow.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(wal)", dbLog)
	if err != nil {
		log.Fatalf("Failed to init whatsmeow store: %v", err)
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		log.Fatalf("Failed to get device: %v", err)
	}

	clientLog := waLog.Stdout("Client", "WARN", true)
	waClient := whatsmeow.NewClient(deviceStore, clientLog)

	cal := NewCalendarClient(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURI)
	transcription := NewTranscriptionClient(cfg.TranscriptionURL)
	agent := NewAgent(cfg.AnthropicAPIKey, cal, db, cfg, nil)
	orchestrator := NewOrchestrator(agent, transcription, db)

	handler := NewHandler(waClient, db, orchestrator)
	agent.sendMsg = handler.SendTextToPhone
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
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
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

	// Start watchdog
	adminPhone := os.Getenv("ADMIN_PHONE")
	watchdog := NewWatchdog(waClient, handler.SendTextToPhone, adminPhone)
	watchdog.Start()

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

func openDBForCLI() *DB {
	db, err := NewDB("data/bot.db")
	if err != nil {
		log.Fatalf("Failed to init database: %v", err)
	}
	return db
}

func updateUser() {
	fs := flag.NewFlagSet("update-user", flag.ExitOnError)
	phone := fs.String("phone", "", "Phone number of user to update")
	name := fs.String("name", "", "New name")
	fs.Parse(os.Args[2:])

	if *phone == "" || *name == "" {
		fmt.Println("Usage: bot update-user --phone=5511... --name=\"New Name\"")
		os.Exit(1)
	}

	db := openDBForCLI()
	defer db.Close()

	user, err := db.GetUserByPhone(*phone)
	if err != nil {
		log.Fatalf("User not found: %v", err)
	}

	_, err = db.conn.Exec("UPDATE users SET name = ? WHERE id = ?", *name, user.ID)
	if err != nil {
		log.Fatalf("Failed to update: %v", err)
	}

	fmt.Printf("User %s renamed to %s\n", user.Name, *name)
}

func removeUser() {
	fs := flag.NewFlagSet("remove-user", flag.ExitOnError)
	phone := fs.String("phone", "", "Phone number of user to remove")
	fs.Parse(os.Args[2:])

	if *phone == "" {
		fmt.Println("Usage: bot remove-user --phone=5511...")
		os.Exit(1)
	}

	db := openDBForCLI()
	defer db.Close()

	user, err := db.GetUserByPhone(*phone)
	if err != nil {
		log.Fatalf("User not found: %v", err)
	}

	_, err = db.conn.Exec("DELETE FROM users WHERE id = ?", user.ID)
	if err != nil {
		log.Fatalf("Failed to remove user: %v", err)
	}

	fmt.Printf("User %s (%s) removed.\n", user.Name, user.PhoneNumber)
}

func grantAccess() {
	fs := flag.NewFlagSet("grant-access", flag.ExitOnError)
	grantee := fs.String("grantee", "", "Phone of user who gets access")
	grantor := fs.String("grantor", "", "Phone of user whose calendar is being shared")
	fs.Parse(os.Args[2:])

	if *grantee == "" || *grantor == "" {
		fmt.Println("Usage: bot grant-access --grantee=PHONE --grantor=PHONE")
		os.Exit(1)
	}

	db := openDBForCLI()
	defer db.Close()

	granteeUser, err := db.GetUserByPhone(*grantee)
	if err != nil {
		log.Fatalf("Grantee not found: %v", err)
	}
	grantorUser, err := db.GetUserByPhone(*grantor)
	if err != nil {
		log.Fatalf("Grantor not found: %v", err)
	}

	pm := NewPermissionManager(db)
	if err := pm.Grant(granteeUser.ID, grantorUser.ID); err != nil {
		log.Fatalf("Failed to grant access: %v", err)
	}

	fmt.Printf("Granted %s permission to schedule on %s's calendar.\n", granteeUser.Name, grantorUser.Name)
}

func revokeAccess() {
	fs := flag.NewFlagSet("revoke-access", flag.ExitOnError)
	grantee := fs.String("grantee", "", "Phone of user whose access is revoked")
	grantor := fs.String("grantor", "", "Phone of user whose calendar was being shared")
	fs.Parse(os.Args[2:])

	if *grantee == "" || *grantor == "" {
		fmt.Println("Usage: bot revoke-access --grantee=PHONE --grantor=PHONE")
		os.Exit(1)
	}

	db := openDBForCLI()
	defer db.Close()

	granteeUser, err := db.GetUserByPhone(*grantee)
	if err != nil {
		log.Fatalf("Grantee not found: %v", err)
	}
	grantorUser, err := db.GetUserByPhone(*grantor)
	if err != nil {
		log.Fatalf("Grantor not found: %v", err)
	}

	pm := NewPermissionManager(db)
	if err := pm.Revoke(granteeUser.ID, grantorUser.ID); err != nil {
		log.Fatalf("Failed to revoke access: %v", err)
	}

	fmt.Printf("Revoked %s's permission to schedule on %s's calendar.\n", granteeUser.Name, grantorUser.Name)
}

func listAccess() {
	fs := flag.NewFlagSet("list-access", flag.ExitOnError)
	phone := fs.String("phone", "", "Phone of the user to check access for")
	fs.Parse(os.Args[2:])

	if *phone == "" {
		fmt.Println("Usage: bot list-access --phone=PHONE")
		os.Exit(1)
	}

	db := openDBForCLI()
	defer db.Close()

	user, err := db.GetUserByPhone(*phone)
	if err != nil {
		log.Fatalf("User not found: %v", err)
	}

	pm := NewPermissionManager(db)
	targets, err := pm.ListTargetsFor(user.ID)
	if err != nil {
		log.Fatalf("Failed to list access: %v", err)
	}

	fmt.Print(pm.FormatAccessList(user.Name, targets))
}
