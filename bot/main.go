package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/api"
	"github.com/giovannirambo/assistente_pessoal/bot/llm"
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

	// Fase 4 (idosos): provider abstraction. operacional sempre Anthropic;
	// companion roteado para DeepSeek se DEEPSEEK_API_KEY setada — fallback
	// pra Anthropic em todos os papeis se nao configurado.
	opChat := llm.NewAnthropicChat(cfg.AnthropicAPIKey, "")
	var companionChat llm.ChatProvider = opChat
	switch cfg.LLMProviderCompanion {
	case "deepseek":
		if cfg.DeepSeekAPIKey == "" {
			log.Printf("LLM_PROVIDER_COMPANION=deepseek mas DEEPSEEK_API_KEY vazia — fallback para Anthropic.")
		} else {
			companionChat = llm.NewDeepSeekChat(cfg.DeepSeekAPIKey, cfg.DeepSeekBaseURL, "")
		}
	case "anthropic", "":
		// Mantem opChat como companion — nao troca.
	default:
		log.Printf("LLM_PROVIDER_COMPANION valor desconhecido %q — fallback para Anthropic.", cfg.LLMProviderCompanion)
	}
	analysis := llm.NewAnthropicAnalysis(cfg.AnthropicAPIKey, "")
	report := llm.NewAnthropicReport(cfg.AnthropicAPIKey, "")
	vision := llm.NewAnthropicVision(cfg.AnthropicAPIKey, "")
	agent.WithProviders(opChat, companionChat, analysis, report, vision)

	orchestrator := NewOrchestrator(agent, transcription, db)

	handler := NewHandler(waClient, db, orchestrator)
	agent.sendMsg = handler.SendTextToPhone
	waClient.AddEventHandler(handler.HandleEvent)

	// Fase 5 (idosos): substitui o noopSnapshotWriter pelo adapter concreto
	// que chama Haiku via AnalysisProvider. handler.flushBuffer chama
	// MaybeUpdateSnapshot pos-conversa significativa; scheduler de catchup
	// reusa a mesma impl via SetSnapshotWriterForCatchup. Construido depois
	// do handler porque precisa do sendMsg pra disparar safety_alert downstream.
	snapshotWriter := NewSnapshotWriter(db, agent.audit, analysis, handler.SendTextToPhone)
	agent.WithSnapshotWriter(snapshotWriter)
	SetSnapshotWriterForCatchup(snapshotWriter)

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

	// Fase 3 (idosos): notifier abstrai canal de envio para escalacao;
	// engine de escalacao decide quando insistir/avisar familia.
	notifier := NewWhatsAppNotifier(handler.SendTextToPhone)
	escEng := NewEscalationEngine(db, notifier)

	scheduler := NewScheduler(db, cal, cfg, handler.SendTextToPhone, notifier, escEng)
	scheduler.WithAgent(agent)
	scheduler.Start()
	defer scheduler.Stop()

	// Start watchdog
	adminPhone := os.Getenv("ADMIN_PHONE")
	watchdog := NewWatchdog(waClient, handler.SendTextToPhone, adminPhone)
	watchdog.Start()

	// Fase 2 (web/UI): API REST do painel sobe no mesmo http.Server. Adapter
	// implementa api.Store delegando pra db/audit/report/sendMsg. Origens
	// e base URL controlam CORS e o link do magic.
	apiAdapter := newAPIAdapter(db, agent.audit, report, cal, cfg.EncryptionKey, handler.SendTextToPhone)
	apiServer := api.NewServer(api.Config{
		Store:          apiAdapter,
		WebBaseURL:     resolveWebBaseURL(),
		PathPrefix:     resolveAPIPathPrefix(),
		AllowedOrigins: resolveWebOrigins(),
		CookieSecure:   resolveCookieSecure(),
		ReportClient:   report,
	})

	go startHTTPServer(cal, db, cfg, apiServer)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	waClient.Disconnect()
}

// startHTTPServer agora hospeda o callback OAuth + a API REST (Fase 2).
// Mantemos o nome `startOAuthServer` deprecated abaixo via wrapper pra evitar
// quebrar callers internos — apenas main.go chamava.
func startHTTPServer(cal *CalendarClient, db *DB, cfg *Config, apiServer *api.Server) {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})

	mux.HandleFunc("/assistente/oauth/callback", func(w http.ResponseWriter, r *http.Request) {
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

		writeOAuthSuccess(w, user.Name)
		log.Printf("OAuth completed for %s (%s)", user.Name, state)
	})

	if apiServer != nil {
		apiServer.Mount(mux)
	}

	log.Println("HTTP server listening on :8080 (oauth + api/v1)")
	http.ListenAndServe(":8080", mux)
}

// resolveWebBaseURL retorna a URL publica do painel web (frontend Next.js).
// Em prod, set WEB_BASE_URL=https://app.lurch.com.br. Em dev local, default
// http://localhost:3000 (mesmo do Next.js dev server).
func resolveWebBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("WEB_BASE_URL")); v != "" {
		return v
	}
	return "http://localhost:3000"
}

// resolveAPIPathPrefix retorna o prefixo de path sob o qual a API REST eh
// montada. Em prod, atras do ALB compartilhado que so roteia /assistente/*
// pra esta instancia, set API_PATH_PREFIX=/assistente — as rotas viram
// /assistente/api/v1/*. Em dev local, vazio (rotas /api/v1/*).
func resolveAPIPathPrefix() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("API_PATH_PREFIX")), "/")
}

// resolveWebOrigins retorna a lista de origins CORS permitidos. Aceita
// CSV em WEB_ORIGIN, default usa WEB_BASE_URL como unico origin.
func resolveWebOrigins() []string {
	raw := strings.TrimSpace(os.Getenv("WEB_ORIGIN"))
	if raw == "" {
		return []string{resolveWebBaseURL()}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return []string{resolveWebBaseURL()}
	}
	return out
}

// resolveCookieSecure decide o atributo Secure do cookie de sessao. Em prod
// (https) o frontend manda Origin https — checamos pelo prefixo do WebBaseURL.
// COOKIE_SECURE override permite forcar via env (ex: dev com tunnel https).
func resolveCookieSecure() bool {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("COOKIE_SECURE"))); v != "" {
		return v == "true" || v == "1" || v == "yes"
	}
	return strings.HasPrefix(strings.ToLower(resolveWebBaseURL()), "https://")
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
