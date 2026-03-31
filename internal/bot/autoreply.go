package bot

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"whatsbridge/internal/db"

	"go.mau.fi/whatsmeow/types"
)

// In-memory cache of active auto-reply rules
var (
	autoReplyRules []db.AutoReply
	autoReplyMu    sync.RWMutex
)

// LoadAutoReplyRules fetches active rules from MySQL and caches them.
func LoadAutoReplyRules() {
	if db.LocalDB == nil {
		log.Println("AutoReply: DB not ready, skipping rule load")
		return
	}

	rules, err := db.GetActiveAutoReplies()
	if err != nil {
		log.Printf("AutoReply: failed to load rules: %v", err)
		return
	}

	autoReplyMu.Lock()
	autoReplyRules = rules
	autoReplyMu.Unlock()

	log.Printf("AutoReply: loaded %d active rules", len(rules))
}

// RefreshAutoReplyCache reloads the rules from DB. Call after any CRUD operation.
func RefreshAutoReplyCache() {
	LoadAutoReplyRules()
}

// ProcessIncomingMessage checks the message against auto-reply rules and sends a reply if matched.
// Should be called from EventHandler in a goroutine.
func ProcessIncomingMessage(senderJID types.JID, messageText string) {
	if messageText == "" {
		return
	}

	// Don't reply to own messages
	if GlobalClient != nil && GlobalClient.Store.ID != nil {
		if senderJID.User == GlobalClient.Store.ID.User {
			return
		}
	}

	// Don't reply to group messages (groups use @g.us server)
	if senderJID.Server == types.GroupServer {
		return
	}

	autoReplyMu.RLock()
	rules := make([]db.AutoReply, len(autoReplyRules))
	copy(rules, autoReplyRules)
	autoReplyMu.RUnlock()

	msgLower := strings.ToLower(messageText)

	for _, rule := range rules {
		if matchRule(rule, msgLower) {
			log.Printf("AutoReply: rule '%s' matched for sender %s", rule.Name, senderJID.User)
			sendAutoReply(senderJID.User, rule)
			return // First match wins
		}
	}
}

// matchRule checks if a message matches a rule's keywords.
func matchRule(rule db.AutoReply, msgLower string) bool {
	keywords := strings.Split(rule.Keywords, ",")

	switch rule.MatchMode {
	case "any":
		for _, kw := range keywords {
			kw = strings.TrimSpace(strings.ToLower(kw))
			if kw != "" && strings.Contains(msgLower, kw) {
				return true
			}
		}
		return false
	default: // "all"
		for _, kw := range keywords {
			kw = strings.TrimSpace(strings.ToLower(kw))
			if kw == "" {
				continue
			}
			if !strings.Contains(msgLower, kw) {
				return false
			}
		}
		return true
	}
}

// sendAutoReply dispatches the reply (text or media) to the sender.
func sendAutoReply(phone string, rule db.AutoReply) {
	if GlobalClient == nil || !GlobalClient.IsConnected() || !GlobalClient.IsLoggedIn() {
		log.Println("AutoReply: bot not connected, skipping reply")
		return
	}

	// If rule has a media URL, download and send as media
	if rule.MediaURL != "" {
		go func() {
			err := sendAutoReplyMedia(phone, rule)
			if err != nil {
				log.Printf("AutoReply: media send failed for rule '%s': %v", rule.Name, err)
				// Fallback to text if media fails and there's reply text
				if rule.ReplyText != "" {
					SendTextMessage(phone, rule.ReplyText)
				}
			}
		}()
		return
	}

	// Send text reply
	if rule.ReplyText != "" {
		err := SendTextMessage(phone, rule.ReplyText)
		if err != nil {
			log.Printf("AutoReply: text send failed for rule '%s': %v", rule.Name, err)
		}
	}
}

// sendAutoReplyMedia downloads a file from URL and sends it.
func sendAutoReplyMedia(phone string, rule db.AutoReply) error {
	resp, err := http.Get(rule.MediaURL)
	if err != nil {
		return fmt.Errorf("failed to download media: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("media URL returned status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read media body: %v", err)
	}

	filename := rule.MediaFilename
	if filename == "" {
		filename = "attachment"
	}

	tmpFile := filepath.Join(os.TempDir(), "autoreply_"+filename)
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("failed to save temp file: %v", err)
	}
	defer os.Remove(tmpFile)

	caption := rule.ReplyText // Use reply_text as caption for media
	return SendMediaMessage(phone, tmpFile, caption)
}
