# whatsmeow Reference Manual

**Version:** `go.mau.fi/whatsmeow v0.0.0-20260327181659-02ec817e7cf4`
**Last Updated:** 2026-04-10
**Repository:** https://github.com/tulir/whatsmeow

---

## JID (User ID) Structure

```go
type JID struct {
    User       string    // Phone number, group ID, or LID
    RawAgent   uint8     // Domain type
    Device     uint16    // Device number (multi-device)
    Integrator uint16
    Server     string    // Always required
}
```

### Servers
| Constant | Value | Use |
|---|---|---|
| `DefaultUserServer` | `s.whatsapp.net` | Regular users (phone numbers) |
| `HiddenUserServer` | `lid` | LID (Hidden ID) |
| `GroupServer` | `g.us` | Group chats |
| `BroadcastServer` | `broadcast` | Broadcast lists |
| `NewsletterServer` | `newsletter` | Newsletter chats |
| `BotServer` | `bot` | Bot accounts |
| `MessengerServer` | `msgr` | Facebook Messenger |

### JID Creation
```go
jid, err := types.ParseJID("1234567890@s.whatsapp.net")
jid := types.NewJID("1234567890", types.DefaultUserServer)
```

---

## LID vs Phone Number

WhatsApp uses two addressing modes:
- **PN (Phone Number):** `5561981012927@s.whatsapp.net`
- **LID (Hidden ID):** `153029751906533@lid`

Messages may arrive with LID as sender. Convert using:

```go
if senderJID.Server == "lid" {
    resolved, err := client.Store.LIDs.GetPNForLID(ctx, senderJID)
    if err == nil {
        senderJID = resolved // Now has phone number
    }
}
```

### Store LID Mapping Interface
```go
PutLIDMapping(ctx, lid, pn)           // Map LID to phone
GetPNForLID(ctx, lid) types.JID       // LID -> Phone
GetLIDForPN(ctx, pn) types.JID        // Phone -> LID
```

### MessageInfo Addressing Fields
```go
msg.Info.Sender           // Primary address (could be LID or PN)
msg.Info.SenderAlt        // Alternative address (the other format)
msg.Info.AddressingMode   // "pn" or "lid"
```

---

## Client Initialization

```go
container, err := sqlstore.New(ctx, "sqlite3",
    "file:whatsmeow.db?_foreign_keys=on&_pragma=busy_timeout(10000)&_pragma=journal_mode(wal)",
    logger)
device, err := container.GetFirstDevice()
client := whatsmeow.NewClient(device, logger)
```

### Connection
```go
client.Connect()
client.IsConnected() bool
client.IsLoggedIn() bool
client.WaitForConnection(timeout) bool
client.Disconnect()
```

---

## Event System

```go
client.AddEventHandler(func(evt any) {
    switch v := evt.(type) {
    case *events.Message:       // Incoming message
    case *events.Receipt:       // Delivery/read receipt
    case *events.Connected:     // Logged in and ready
    case *events.Disconnected:  // WebSocket closed
    case *events.QR:            // QR code for pairing
    case *events.LoggedOut:     // Session invalidated
    case *events.ChatPresence:  // Typing indicator
    case *events.HistorySync:   // Historical messages
    case *events.OfflineSyncCompleted: // Offline sync done
    }
})
```

### Key Event Types
| Event | Description |
|---|---|
| `*events.Message` | Incoming message (text, audio, image, etc.) |
| `*events.Receipt` | Delivery/read receipts |
| `*events.Connected` | Successfully connected |
| `*events.Disconnected` | Connection lost |
| `*events.QR` | QR codes for pairing (v.Codes []string) |
| `*events.PairSuccess` | Pairing complete |
| `*events.LoggedOut` | Session invalidated |
| `*events.ChatPresence` | Typing/recording indicators |
| `*events.OfflineSyncCompleted` | Offline sync finished |
| `*events.KeepAliveTimeout` | Ping timed out |

---

## MessageInfo Structure

```go
type MessageInfo struct {
    ID        types.MessageID
    Type      string
    PushName  string      // Display name of sender
    Timestamp time.Time
    MediaType string
    Edit      EditAttribute

    // From MessageSource:
    Chat      types.JID   // Chat JID
    Sender    types.JID   // Sender JID (may be LID!)
    IsFromMe  bool
    IsGroup   bool
    AddressingMode types.AddressingMode // "pn" or "lid"
    SenderAlt types.JID   // Alternative sender address
}
```

---

## Sending Messages

```go
resp, err := client.SendMessage(ctx, targetJID, &waE2E.Message{
    Conversation: proto.String("Hello!"),
})
```

### SendResponse
```go
type SendResponse struct {
    Timestamp time.Time
    ID        types.MessageID
    Sender    types.JID
}
```

### Message Types
```go
// Text
&waE2E.Message{Conversation: proto.String("text")}

// Image (after upload)
&waE2E.Message{ImageMessage: &waE2E.ImageMessage{...}}

// Audio
&waE2E.Message{AudioMessage: &waE2E.AudioMessage{...}}

// Document
&waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{...}}
```

### Message Operations
```go
client.RevokeMessage(ctx, chatJID, msgID)
client.BuildEdit(chatJID, msgID, newContent)
client.BuildReaction(chatJID, senderJID, msgID, "👍")
client.MarkRead(ctx, msgIDs, ts, chatJID, senderJID)
client.GenerateMessageID() // Always use this for new IDs
```

---

## Media Handling

### Download
```go
data, err := client.Download(ctx, msg.Message.GetAudioMessage())
```

### Upload
```go
resp, err := client.Upload(ctx, data, whatsmeow.MediaImage)
// MediaTypes: MediaImage, MediaVideo, MediaAudio, MediaDocument
```

---

## Reading Messages

### Text
```go
if text := msg.Message.GetConversation(); text != "" {
    // Plain text
}
if ext := msg.Message.GetExtendedTextMessage(); ext != nil {
    text = ext.GetText() // Text with formatting/links
}
```

### Audio
```go
if audio := msg.Message.GetAudioMessage(); audio != nil {
    data, err := client.Download(ctx, audio)
    // data is the decoded audio bytes (opus/ogg)
}
```

### Image/Video/Document
```go
if img := msg.Message.GetImageMessage(); img != nil {
    data, err := client.Download(ctx, img)
    caption := img.GetCaption()
}
```

---

## Groups

```go
groups, err := client.GetJoinedGroups(ctx)
info, err := client.GetGroupInfo(ctx, groupJID)
client.SetGroupName(ctx, groupJID, "New Name")
client.UpdateGroupParticipants(ctx, groupJID, participants, "add")
```

---

## Presence & Privacy

```go
client.SendPresence(ctx, types.PresenceAvailable)
client.SendChatPresence(ctx, chatJID, types.ChatPresenceTyping, types.ChatPresenceMediaNone)
client.SubscribePresence(ctx, userJID)
settings, err := client.GetPrivacySettings(ctx)
```

---

## User Info

```go
responses, err := client.IsOnWhatsApp(ctx, []string{"+5561981012927"})
users, err := client.GetUserInfo(ctx, []types.JID{userJID})
devices, err := client.GetUserDevices(ctx, []types.JID{userJID})
pic, err := client.GetProfilePictureInfo(ctx, userJID, nil)
```

---

## Important Gotchas

1. **LID vs Phone:** Messages may arrive with LID sender. Always resolve with `Store.LIDs.GetPNForLID()`.
2. **Brazilian numbers:** WhatsApp may deliver with or without the 9th digit. Normalize both formats.
3. **SQLite busy_timeout:** Use `?_pragma=busy_timeout(10000)` to prevent `database is locked` errors.
4. **IsFromMe:** Messages from the bot's own number have `IsFromMe=true`. Filter these to avoid loops.
5. **Download needs context:** `client.Download(ctx, audioMsg)` — the context is required.
6. **Proto pointers:** All message fields use `proto.String()`, `proto.Uint64()`, etc.
7. **Message IDs:** Always use `client.GenerateMessageID()`, never craft manually.
8. **Auto-reconnect:** Enabled by default. Check `events.LoggedOut` for permanent disconnects.
9. **Newsletter JIDs:** IDs like `153029751906533@newsletter` are not people. Filter by server type.
10. **Group JIDs:** Use `jid.Server == "g.us"` to detect group messages.
