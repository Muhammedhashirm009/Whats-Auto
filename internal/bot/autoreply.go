package bot

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"whatsbridge/internal/db"

	"go.mau.fi/whatsmeow/types"
)

// ─── Reusable HTTP client for media downloads (opt #5) ──────

var mediaHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:       10,
		IdleConnTimeout:    90 * time.Second,
		DisableCompression: true,
	},
}

// ─── Pre-computed rule cache (opt #6) ───────────────────────

type cachedRule struct {
	db.AutoReply
	keywordsLower []string // pre-split, pre-lowered, pre-trimmed
}

var (
	autoReplyRules []cachedRule
	autoReplyMu    sync.RWMutex
)

// ─── Menu items cache (opt #3) ──────────────────────────────

var (
	menuItemsCache   = make(map[int][]db.AutoReplyItem) // key: rule ID
	menuItemsCacheMu sync.RWMutex
)

// ─── Conversation session tracking ──────────────────────────

type menuSession struct {
	RuleID    int
	Items     []db.AutoReplyItem
	ExpiresAt time.Time
}

var (
	menuSessions   = make(map[string]*menuSession) // key: sender JID user
	menuSessionsMu sync.RWMutex
	sessionTTL     = 5 * time.Minute
)

// ─── Session cleanup goroutine (opt #7) ─────────────────────

func init() {
	go func() {
		for {
			time.Sleep(60 * time.Second)
			now := time.Now()
			menuSessionsMu.Lock()
			for k, s := range menuSessions {
				if now.After(s.ExpiresAt) {
					delete(menuSessions, k)
				}
			}
			menuSessionsMu.Unlock()
		}
	}()
}

// ─── Rule cache management ──────────────────────────────────

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

	// Pre-compute keywords and cache menu items
	cached := make([]cachedRule, 0, len(rules))
	newMenuCache := make(map[int][]db.AutoReplyItem)

	for _, rule := range rules {
		cr := cachedRule{AutoReply: rule}

		// Pre-split and lowercase keywords (opt #6)
		rawKW := strings.Split(rule.Keywords, ",")
		for _, kw := range rawKW {
			kw = strings.TrimSpace(strings.ToLower(kw))
			if kw != "" {
				cr.keywordsLower = append(cr.keywordsLower, kw)
			}
		}

		// Pre-cache menu items (opt #3)
		if rule.RuleType == "menu" {
			items, err := db.GetMenuItems(rule.ID)
			if err == nil && len(items) > 0 {
				newMenuCache[rule.ID] = items
			}
		}

		cached = append(cached, cr)
	}

	autoReplyMu.Lock()
	autoReplyRules = cached
	autoReplyMu.Unlock()

	menuItemsCacheMu.Lock()
	menuItemsCache = newMenuCache
	menuItemsCacheMu.Unlock()

	log.Printf("AutoReply: loaded %d active rules", len(rules))
}

func RefreshAutoReplyCache() {
	LoadAutoReplyRules()
}

// ─── Main incoming message processor ────────────────────────

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

	senderKey := senderJID.User
	msgTrimmed := strings.TrimSpace(messageText)

	// Step 1: Check if sender has an active menu session
	menuSessionsMu.RLock()
	sess, hasSession := menuSessions[senderKey]
	menuSessionsMu.RUnlock()

	if hasSession && time.Now().Before(sess.ExpiresAt) {
		// Try to match the message as a numbered option
		optNum, err := strconv.Atoi(msgTrimmed)
		if err == nil {
			for _, item := range sess.Items {
				if item.OptionNumber == optNum {
					log.Printf("AutoReply: menu option %d selected by %s", optNum, senderKey)
					// Clear session after selection
					menuSessionsMu.Lock()
					delete(menuSessions, senderKey)
					menuSessionsMu.Unlock()
					// Send the option's reply
					sendMenuItemReply(senderJID, item)
					return
				}
			}
			// Number doesn't match any option — send "invalid option" hint
			SendTextMessageToJID(senderJID, fmt.Sprintf("❌ Invalid option. Please reply with a number between 1 and %d.", len(sess.Items)))
			return
		}
		// Not a number — clear session and fall through to normal matching
		menuSessionsMu.Lock()
		delete(menuSessions, senderKey)
		menuSessionsMu.Unlock()
	}

	// Step 2: Normal keyword matching (uses pre-computed keywords)
	autoReplyMu.RLock()
	rules := make([]cachedRule, len(autoReplyRules))
	copy(rules, autoReplyRules)
	autoReplyMu.RUnlock()

	msgLower := strings.ToLower(messageText)

	for _, rule := range rules {
		if matchRule(rule, msgLower) {
			log.Printf("AutoReply: rule '%s' (type=%s) matched for sender %s", rule.Name, rule.RuleType, senderKey)

			if rule.RuleType == "menu" {
				sendMenuReply(senderJID, rule)
			} else {
				sendSimpleReply(senderJID, rule)
			}
			return // First match wins
		}
	}
}

// ─── Keyword matching (uses pre-computed keywords) ──────────

func matchRule(rule cachedRule, msgLower string) bool {
	switch rule.MatchMode {
	case "any":
		for _, kw := range rule.keywordsLower {
			if strings.Contains(msgLower, kw) {
				return true
			}
		}
		return false
	default: // "all"
		for _, kw := range rule.keywordsLower {
			if !strings.Contains(msgLower, kw) {
				return false
			}
		}
		return len(rule.keywordsLower) > 0
	}
}

// ─── Menu reply: sends formatted menu + creates session ─────

func sendMenuReply(senderJID types.JID, rule cachedRule) {
	// Try cache first, fallback to DB
	menuItemsCacheMu.RLock()
	items, ok := menuItemsCache[rule.ID]
	menuItemsCacheMu.RUnlock()

	if !ok || len(items) == 0 {
		var err error
		items, err = db.GetMenuItems(rule.ID)
		if err != nil || len(items) == 0 {
			log.Printf("AutoReply: menu rule '%s' has no items, skipping", rule.Name)
			return
		}
	}

	// Build formatted menu text
	var sb strings.Builder
	if rule.ReplyText != "" {
		sb.WriteString(rule.ReplyText)
		sb.WriteString("\n")
	} else {
		sb.WriteString(fmt.Sprintf("📋 *%s*\n", rule.Name))
	}
	sb.WriteString("━━━━━━━━━━━━━━━━━\n")
	for _, item := range items {
		sb.WriteString(fmt.Sprintf("%s %s\n", numberEmoji(item.OptionNumber), item.Label))
	}
	sb.WriteString("\n_Reply with a number to continue._")

	// Send menu
	err := SendTextMessageToJID(senderJID, sb.String())
	if err != nil {
		log.Printf("AutoReply: failed to send menu for rule '%s': %v", rule.Name, err)
		return
	}

	// Create session
	menuSessionsMu.Lock()
	menuSessions[senderJID.User] = &menuSession{
		RuleID:    rule.ID,
		Items:     items,
		ExpiresAt: time.Now().Add(sessionTTL),
	}
	menuSessionsMu.Unlock()

	log.Printf("AutoReply: menu session started for %s (rule '%s', %d items, expires in %v)",
		senderJID.User, rule.Name, len(items), sessionTTL)
}

// numberEmoji returns a number emoji like 1️⃣, 2️⃣, etc.
func numberEmoji(n int) string {
	digits := []string{"0️⃣", "1️⃣", "2️⃣", "3️⃣", "4️⃣", "5️⃣", "6️⃣", "7️⃣", "8️⃣", "9️⃣"}
	if n >= 0 && n < len(digits) {
		return digits[n]
	}
	return fmt.Sprintf("(%d)", n)
}

// ─── Menu item reply: handles selected option ───────────────

func sendMenuItemReply(senderJID types.JID, item db.AutoReplyItem) {
	if GlobalClient == nil || !GlobalClient.IsConnected() || !GlobalClient.IsLoggedIn() {
		log.Println("AutoReply: bot not connected, skipping menu item reply")
		return
	}

	if item.MediaURL != "" {
		go func() {
			err := sendMediaFromURL(senderJID, item.MediaURL, item.MediaFilename, item.ReplyText)
			if err != nil {
				log.Printf("AutoReply: menu item media send failed: %v", err)
				if item.ReplyText != "" {
					SendTextMessageToJID(senderJID, item.ReplyText)
				}
			}
		}()
		return
	}

	if item.ReplyText != "" {
		err := SendTextMessageToJID(senderJID, item.ReplyText)
		if err != nil {
			log.Printf("AutoReply: menu item text send failed: %v", err)
		}
	}
}

// ─── Simple reply (existing behavior) ───────────────────────

func sendSimpleReply(senderJID types.JID, rule cachedRule) {
	if GlobalClient == nil || !GlobalClient.IsConnected() || !GlobalClient.IsLoggedIn() {
		log.Println("AutoReply: bot not connected, skipping reply")
		return
	}

	if rule.MediaURL != "" {
		go func() {
			err := sendMediaFromURL(senderJID, rule.MediaURL, rule.MediaFilename, rule.ReplyText)
			if err != nil {
				log.Printf("AutoReply: media send failed for rule '%s': %v", rule.Name, err)
				if rule.ReplyText != "" {
					SendTextMessageToJID(senderJID, rule.ReplyText)
				}
			}
		}()
		return
	}

	if rule.ReplyText != "" {
		err := SendTextMessageToJID(senderJID, rule.ReplyText)
		if err != nil {
			log.Printf("AutoReply: text send failed for rule '%s': %v", rule.Name, err)
		}
	}
}

// ─── Shared media helper (uses reusable HTTP client) ────────

func sendMediaFromURL(recipientJID types.JID, mediaURL, mediaFilename, caption string) error {
	resp, err := mediaHTTPClient.Get(mediaURL)
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

	filename := mediaFilename
	if filename == "" {
		filename = "attachment"
	}

	tmpFile := filepath.Join(os.TempDir(), "autoreply_"+filename)
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("failed to save temp file: %v", err)
	}
	defer os.Remove(tmpFile)

	// Build the media message using SendMediaMessage's logic but send via JID
	return SendMediaMessageFromFile(recipientJID, tmpFile, caption)
}
