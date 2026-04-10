# Assistente Pessoal WhatsApp + Google Calendar

## Architecture

Two services orchestrated via Docker Compose:
- `bot/` — Go service (whatsmeow + scheduler + orchestration)
- `transcription/` — Python/FastAPI service (AssemblyAI transcription)

## Development

```bash
# Run Go tests
cd bot && go test -v

# Build bot locally
cd bot && go build -o bot .

# Run transcription service locally
cd transcription && uvicorn main:app --reload

# Docker Compose
docker compose up --build
```

## Key Patterns

- All external API calls (Claude, Google Calendar, AssemblyAI) are in dedicated files
- User credentials are encrypted with AES-256-GCM before storing in SQLite
- Pending confirmations auto-confirm after user-configurable timeout (default: 2h)
- Scheduler runs cron checks every minute, respects per-user timezone/preferences

## Deploy

```bash
cd terraform
terraform init
terraform apply -var="admin_ip=YOUR_IP/32" -var="key_name=YOUR_KEY"
```
