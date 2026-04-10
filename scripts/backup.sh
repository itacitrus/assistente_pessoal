#!/bin/bash
set -euo pipefail

BACKUP_DIR="/tmp/assistente-backup"
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
S3_BUCKET="${S3_BACKUP_BUCKET:-assistente-backups}"
DATA_DIR="/opt/assistente/data"

mkdir -p "$BACKUP_DIR"

sqlite3 "$DATA_DIR/bot.db" ".backup '$BACKUP_DIR/bot-$TIMESTAMP.db'"
sqlite3 "$DATA_DIR/whatsmeow.db" ".backup '$BACKUP_DIR/whatsmeow-$TIMESTAMP.db'"

tar -czf "$BACKUP_DIR/backup-$TIMESTAMP.tar.gz" -C "$BACKUP_DIR" \
    "bot-$TIMESTAMP.db" "whatsmeow-$TIMESTAMP.db"

aws s3 cp "$BACKUP_DIR/backup-$TIMESTAMP.tar.gz" \
    "s3://$S3_BUCKET/backups/backup-$TIMESTAMP.tar.gz"

rm -rf "$BACKUP_DIR"

echo "Backup completed: backup-$TIMESTAMP.tar.gz"
