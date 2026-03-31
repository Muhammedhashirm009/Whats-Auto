package db

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

var LocalDB *sql.DB

func InitDB(dsn string) {
	var err error
	LocalDB, err = sql.Open("mysql", dsn)
	if err != nil {
		log.Printf("Failed to open MySQL: %v — will retry", err)
		go retryDB(dsn)
		return
	}

	// Test connection with retry
	if err = LocalDB.Ping(); err != nil {
		log.Printf("Failed to connect to MySQL: %v — will retry in background", err)
		go retryDB(dsn)
		return
	}

	// Connection pool tuning for remote MySQL (reduces latency and prevents stalls)
	LocalDB.SetMaxOpenConns(10)
	LocalDB.SetMaxIdleConns(3)
	LocalDB.SetConnMaxLifetime(3 * time.Minute)
	LocalDB.SetConnMaxIdleTime(30 * time.Second)

	createTables()
	log.Println("MySQL database initialized successfully.")
}

func retryDB(dsn string) {
	for {
		time.Sleep(5 * time.Second)
		var err error
		LocalDB, err = sql.Open("mysql", dsn)
		if err != nil {
			log.Printf("MySQL retry: open failed: %v", err)
			continue
		}
		if err = LocalDB.Ping(); err != nil {
			log.Printf("MySQL retry: ping failed: %v", err)
			continue
		}
		createTables()
		log.Println("MySQL database initialized successfully (on retry).")
		return
	}
}

func createTables() {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS usage_logs (
			id INT AUTO_INCREMENT PRIMARY KEY,
			date VARCHAR(10) UNIQUE,
			messages_sent INT DEFAULT 0,
			messages_failed INT DEFAULT 0
		);`,
		`CREATE TABLE IF NOT EXISTS scheduled_messages (
			id INT AUTO_INCREMENT PRIMARY KEY,
			recipient VARCHAR(30) NOT NULL,
			message TEXT NOT NULL,
			scheduled_for DATETIME NOT NULL,
			status VARCHAR(20) DEFAULT 'pending'
		);`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(100) NOT NULL,
			key_hash VARCHAR(64) NOT NULL UNIQUE,
			key_prefix VARCHAR(8) NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_used_at DATETIME NULL,
			is_active TINYINT(1) DEFAULT 1
		);`,
		`CREATE TABLE IF NOT EXISTS auto_replies (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(100) NOT NULL,
			keywords TEXT NOT NULL,
			reply_text TEXT,
			media_url VARCHAR(500),
			media_filename VARCHAR(255),
			match_mode VARCHAR(10) DEFAULT 'all',
			rule_type VARCHAR(10) DEFAULT 'simple',
			is_active TINYINT(1) DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS auto_reply_items (
			id INT AUTO_INCREMENT PRIMARY KEY,
			rule_id INT NOT NULL,
			option_number INT NOT NULL,
			label VARCHAR(200) NOT NULL,
			reply_text TEXT,
			media_url VARCHAR(500),
			media_filename VARCHAR(255),
			FOREIGN KEY (rule_id) REFERENCES auto_replies(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS contacts (
			id INT AUTO_INCREMENT PRIMARY KEY,
			phone VARCHAR(30) NOT NULL UNIQUE,
			name VARCHAR(100) DEFAULT '',
			labels VARCHAR(200) DEFAULT '',
			last_message_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS messages (
			id INT AUTO_INCREMENT PRIMARY KEY,
			phone VARCHAR(30) NOT NULL,
			direction VARCHAR(3) NOT NULL,
			body TEXT NOT NULL,
			media_url VARCHAR(500) DEFAULT '',
			status VARCHAR(20) DEFAULT 'sent',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_phone_time (phone, created_at)
		);`,
	}

	for _, query := range queries {
		_, err := LocalDB.Exec(query)
		if err != nil {
			fmt.Printf("Error creating table: %v\n", err)
		}
	}

	// Migrations for existing installs
	LocalDB.Exec(`ALTER TABLE auto_replies ADD COLUMN rule_type VARCHAR(10) DEFAULT 'simple'`)
}

func LogMessageUsage(success bool) {
	if LocalDB == nil {
		return
	}

	today := time.Now().Format("2006-01-02")

	// Ensure row exists for today
	_, _ = LocalDB.Exec(`INSERT IGNORE INTO usage_logs (date) VALUES (?)`, today)

	if success {
		_, _ = LocalDB.Exec(`UPDATE usage_logs SET messages_sent = messages_sent + 1 WHERE date = ?`, today)
	} else {
		_, _ = LocalDB.Exec(`UPDATE usage_logs SET messages_failed = messages_failed + 1 WHERE date = ?`, today)
	}
}

type Metrics struct {
	TotalSent      int `json:"total_sent"`
	TotalFailed    int `json:"total_failed"`
	ScheduledCount int `json:"scheduled_count"`
}

func GetMetrics() (Metrics, error) {
	var m Metrics
	err := LocalDB.QueryRow(`SELECT IFNULL(SUM(messages_sent),0), IFNULL(SUM(messages_failed),0) FROM usage_logs`).Scan(&m.TotalSent, &m.TotalFailed)
	if err != nil {
		m.TotalSent = 0
		m.TotalFailed = 0
	}

	LocalDB.QueryRow(`SELECT COUNT(*) FROM scheduled_messages WHERE status = 'pending'`).Scan(&m.ScheduledCount)
	return m, nil
}

func AddScheduledMessage(recipient, message, scheduledFor string) error {
	_, err := LocalDB.Exec(`INSERT INTO scheduled_messages (recipient, message, scheduled_for) VALUES (?, ?, ?)`,
		recipient, message, scheduledFor)
	return err
}

type ScheduledMessage struct {
	ID        int
	Recipient string
	Message   string
}

func GetPendingMessages(now string) ([]ScheduledMessage, error) {
	rows, err := LocalDB.Query(`SELECT id, recipient, message FROM scheduled_messages WHERE status = 'pending' AND scheduled_for <= ?`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []ScheduledMessage
	for rows.Next() {
		var m ScheduledMessage
		if err := rows.Scan(&m.ID, &m.Recipient, &m.Message); err == nil {
			msgs = append(msgs, m)
		}
	}
	return msgs, nil
}

func UpdateScheduledMessageStatus(id int, status string) error {
	_, err := LocalDB.Exec(`UPDATE scheduled_messages SET status = ? WHERE id = ?`, status, id)
	return err
}

// ─── API Key Management ─────────────────────────────────────

type APIKey struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	KeyPrefix string    `json:"key_prefix"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  *string   `json:"last_used_at"`
	IsActive  bool      `json:"is_active"`
}

func hashKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// CreateAPIKey generates a new API key, stores its hash, and returns the raw key.
func CreateAPIKey(name string) (string, error) {
	// Generate 32-byte random key
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random key: %w", err)
	}
	rawKey := "wb_" + hex.EncodeToString(b) // wb_ prefix for easy identification
	keyHash := hashKey(rawKey)
	keyPrefix := rawKey[:11] // "wb_" + first 8 hex chars

	_, err := LocalDB.Exec(
		`INSERT INTO api_keys (name, key_hash, key_prefix) VALUES (?, ?, ?)`,
		name, keyHash, keyPrefix,
	)
	if err != nil {
		return "", err
	}

	log.Printf("API key '%s' created (prefix: %s...)", name, keyPrefix)
	return rawKey, nil
}

// ListAPIKeys returns all API keys (without the actual key, just metadata).
func ListAPIKeys() ([]APIKey, error) {
	rows, err := LocalDB.Query(`SELECT id, name, key_prefix, created_at, last_used_at, is_active FROM api_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []APIKey
	for rows.Next() {
		var k APIKey
		var createdStr string
		var lastUsedStr sql.NullString
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyPrefix, &createdStr, &lastUsedStr, &k.IsActive); err != nil {
			continue
		}
		k.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdStr)
		if lastUsedStr.Valid {
			k.LastUsed = &lastUsedStr.String
		}
		keys = append(keys, k)
	}
	return keys, nil
}

// DeleteAPIKey removes an API key by ID.
func DeleteAPIKey(id int) error {
	result, err := LocalDB.Exec(`DELETE FROM api_keys WHERE id = ?`, id)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("API key not found")
	}
	log.Printf("API key #%d deleted", id)
	return nil
}

// ValidateAPIKey checks if a raw API key is valid and active.
func ValidateAPIKey(rawKey string) bool {
	if LocalDB == nil || rawKey == "" {
		return false
	}

	keyHash := hashKey(rawKey)
	var isActive bool
	err := LocalDB.QueryRow(`SELECT is_active FROM api_keys WHERE key_hash = ?`, keyHash).Scan(&isActive)
	if err != nil {
		return false
	}

	if isActive {
		// Update last_used_at
		go func() {
			LocalDB.Exec(`UPDATE api_keys SET last_used_at = NOW() WHERE key_hash = ?`, keyHash)
		}()
	}

	return isActive
}

// HasAnyAPIKeys checks if there are any API keys configured.
func HasAnyAPIKeys() bool {
	if LocalDB == nil {
		return false
	}
	var count int
	err := LocalDB.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE is_active = 1`).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

// ─── Auto Reply Management ──────────────────────────────────

type AutoReply struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	Keywords      string `json:"keywords"`
	ReplyText     string `json:"reply_text"`
	MediaURL      string `json:"media_url"`
	MediaFilename string `json:"media_filename"`
	MatchMode     string `json:"match_mode"`
	RuleType      string `json:"rule_type"`
	IsActive      bool   `json:"is_active"`
	CreatedAt     string `json:"created_at"`
}

type AutoReplyItem struct {
	ID            int    `json:"id"`
	RuleID        int    `json:"rule_id"`
	OptionNumber  int    `json:"option_number"`
	Label         string `json:"label"`
	ReplyText     string `json:"reply_text"`
	MediaURL      string `json:"media_url"`
	MediaFilename string `json:"media_filename"`
}

func AddAutoReply(name, keywords, replyText, mediaURL, mediaFilename, matchMode, ruleType string) (int64, error) {
	if matchMode == "" {
		matchMode = "all"
	}
	if ruleType == "" {
		ruleType = "simple"
	}
	result, err := LocalDB.Exec(
		`INSERT INTO auto_replies (name, keywords, reply_text, media_url, media_filename, match_mode, rule_type) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		name, keywords, replyText, mediaURL, mediaFilename, matchMode, ruleType,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func ListAutoReplies() ([]AutoReply, error) {
	rows, err := LocalDB.Query(`SELECT id, name, keywords, IFNULL(reply_text,''), IFNULL(media_url,''), IFNULL(media_filename,''), match_mode, IFNULL(rule_type,'simple'), is_active, created_at FROM auto_replies ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []AutoReply
	for rows.Next() {
		var r AutoReply
		var createdStr string
		if err := rows.Scan(&r.ID, &r.Name, &r.Keywords, &r.ReplyText, &r.MediaURL, &r.MediaFilename, &r.MatchMode, &r.RuleType, &r.IsActive, &createdStr); err != nil {
			continue
		}
		r.CreatedAt = createdStr
		rules = append(rules, r)
	}
	return rules, nil
}

func GetAutoReplyByID(id int) (*AutoReply, error) {
	var r AutoReply
	var createdStr string
	err := LocalDB.QueryRow(
		`SELECT id, name, keywords, IFNULL(reply_text,''), IFNULL(media_url,''), IFNULL(media_filename,''), match_mode, IFNULL(rule_type,'simple'), is_active, created_at FROM auto_replies WHERE id = ?`, id,
	).Scan(&r.ID, &r.Name, &r.Keywords, &r.ReplyText, &r.MediaURL, &r.MediaFilename, &r.MatchMode, &r.RuleType, &r.IsActive, &createdStr)
	if err != nil {
		return nil, err
	}
	r.CreatedAt = createdStr
	return &r, nil
}

func GetActiveAutoReplies() ([]AutoReply, error) {
	rows, err := LocalDB.Query(`SELECT id, name, keywords, IFNULL(reply_text,''), IFNULL(media_url,''), IFNULL(media_filename,''), match_mode, IFNULL(rule_type,'simple'), is_active, created_at FROM auto_replies WHERE is_active = 1 ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []AutoReply
	for rows.Next() {
		var r AutoReply
		var createdStr string
		if err := rows.Scan(&r.ID, &r.Name, &r.Keywords, &r.ReplyText, &r.MediaURL, &r.MediaFilename, &r.MatchMode, &r.RuleType, &r.IsActive, &createdStr); err != nil {
			continue
		}
		r.CreatedAt = createdStr
		rules = append(rules, r)
	}
	return rules, nil
}

func UpdateAutoReply(id int, name, keywords, replyText, mediaURL, mediaFilename, matchMode, ruleType string, isActive bool) error {
	if ruleType == "" {
		ruleType = "simple"
	}
	_, err := LocalDB.Exec(
		`UPDATE auto_replies SET name=?, keywords=?, reply_text=?, media_url=?, media_filename=?, match_mode=?, rule_type=?, is_active=? WHERE id=?`,
		name, keywords, replyText, mediaURL, mediaFilename, matchMode, ruleType, isActive, id,
	)
	return err
}

func DeleteAutoReply(id int) error {
	result, err := LocalDB.Exec(`DELETE FROM auto_replies WHERE id = ?`, id)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("auto-reply rule not found")
	}
	log.Printf("Auto-reply rule #%d deleted", id)
	return nil
}

// ─── Menu Item Management ───────────────────────────────────

func AddMenuItem(ruleID, optionNumber int, label, replyText, mediaURL, mediaFilename string) (int64, error) {
	result, err := LocalDB.Exec(
		`INSERT INTO auto_reply_items (rule_id, option_number, label, reply_text, media_url, media_filename) VALUES (?, ?, ?, ?, ?, ?)`,
		ruleID, optionNumber, label, replyText, mediaURL, mediaFilename,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func GetMenuItems(ruleID int) ([]AutoReplyItem, error) {
	rows, err := LocalDB.Query(
		`SELECT id, rule_id, option_number, label, IFNULL(reply_text,''), IFNULL(media_url,''), IFNULL(media_filename,'') FROM auto_reply_items WHERE rule_id = ? ORDER BY option_number ASC`,
		ruleID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []AutoReplyItem
	for rows.Next() {
		var item AutoReplyItem
		if err := rows.Scan(&item.ID, &item.RuleID, &item.OptionNumber, &item.Label, &item.ReplyText, &item.MediaURL, &item.MediaFilename); err != nil {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

func UpdateMenuItem(id, optionNumber int, label, replyText, mediaURL, mediaFilename string) error {
	_, err := LocalDB.Exec(
		`UPDATE auto_reply_items SET option_number=?, label=?, reply_text=?, media_url=?, media_filename=? WHERE id=?`,
		optionNumber, label, replyText, mediaURL, mediaFilename, id,
	)
	return err
}

func DeleteMenuItem(id int) error {
	result, err := LocalDB.Exec(`DELETE FROM auto_reply_items WHERE id = ?`, id)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("menu item not found")
	}
	return nil
}

// ─── Contact Management ────────────────────────────────────

type Contact struct {
	ID            int    `json:"id"`
	Phone         string `json:"phone"`
	Name          string `json:"name"`
	Labels        string `json:"labels"`
	LastMessageAt string `json:"last_message_at"`
	CreatedAt     string `json:"created_at"`
}

// SaveContact creates or updates a contact. Upserts on phone.
func SaveContact(phone, name, labels string) error {
	_, err := LocalDB.Exec(
		`INSERT INTO contacts (phone, name, labels) VALUES (?, ?, ?)
		 ON DUPLICATE KEY UPDATE name = IF(? = '', name, ?), labels = IF(? = '', labels, ?), last_message_at = NOW()`,
		phone, name, labels, name, name, labels, labels,
	)
	return err
}

// TouchContact updates last_message_at, creating the contact if needed.
func TouchContact(phone string) {
	if LocalDB == nil {
		return
	}
	LocalDB.Exec(
		`INSERT INTO contacts (phone) VALUES (?) ON DUPLICATE KEY UPDATE last_message_at = NOW()`,
		phone,
	)
}

// UpdateContactName updates the name of a contact by phone.
func UpdateContactName(phone, name string) error {
	_, err := LocalDB.Exec(`UPDATE contacts SET name = ? WHERE phone = ?`, name, phone)
	return err
}

// ListContacts returns all contacts ordered by most recent message.
func ListContacts() ([]Contact, error) {
	rows, err := LocalDB.Query(
		`SELECT id, phone, IFNULL(name,''), IFNULL(labels,''), last_message_at, created_at
		 FROM contacts ORDER BY last_message_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var contacts []Contact
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.ID, &c.Phone, &c.Name, &c.Labels, &c.LastMessageAt, &c.CreatedAt); err != nil {
			continue
		}
		contacts = append(contacts, c)
	}
	return contacts, nil
}

// SearchContacts searches contacts by phone or name.
func SearchContacts(query string) ([]Contact, error) {
	q := "%" + query + "%"
	rows, err := LocalDB.Query(
		`SELECT id, phone, IFNULL(name,''), IFNULL(labels,''), last_message_at, created_at
		 FROM contacts WHERE phone LIKE ? OR name LIKE ? ORDER BY last_message_at DESC LIMIT 20`,
		q, q,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var contacts []Contact
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.ID, &c.Phone, &c.Name, &c.Labels, &c.LastMessageAt, &c.CreatedAt); err != nil {
			continue
		}
		contacts = append(contacts, c)
	}
	return contacts, nil
}

// DeleteContact deletes a contact by phone.
func DeleteContact(phone string) error {
	result, err := LocalDB.Exec(`DELETE FROM contacts WHERE phone = ?`, phone)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("contact not found")
	}
	return nil
}

// ─── Message Storage ────────────────────────────────────────

type Message struct {
	ID        int    `json:"id"`
	Phone     string `json:"phone"`
	Direction string `json:"direction"` // "in" or "out"
	Body      string `json:"body"`
	MediaURL  string `json:"media_url"`
	Status    string `json:"status"` // "sent", "failed", "delivered", "received"
	CreatedAt string `json:"created_at"`
}

// SaveMessage stores a message (incoming or outgoing).
func SaveMessage(phone, direction, body, mediaURL, status string) {
	if LocalDB == nil {
		return
	}
	go func() {
		_, err := LocalDB.Exec(
			`INSERT INTO messages (phone, direction, body, media_url, status) VALUES (?, ?, ?, ?, ?)`,
			phone, direction, body, mediaURL, status,
		)
		if err != nil {
			log.Printf("SaveMessage error: %v", err)
		}
		// Also touch the contact
		TouchContact(phone)
	}()
}

// GetMessages returns messages for a phone, newest last, with limit.
func GetMessages(phone string, limit int) ([]Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := LocalDB.Query(
		`SELECT id, phone, direction, body, IFNULL(media_url,''), status, created_at
		 FROM messages WHERE phone = ? ORDER BY created_at DESC, id DESC LIMIT ?`,
		phone, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.Phone, &m.Direction, &m.Body, &m.MediaURL, &m.Status, &m.CreatedAt); err != nil {
			continue
		}
		messages = append(messages, m)
	}

	// Reverse so oldest is first (chat order)
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, nil
}

// Conversation holds a contact with their last message for the sidebar.
type Conversation struct {
	Contact     Contact `json:"contact"`
	LastMessage Message `json:"last_message"`
	UnreadCount int     `json:"unread_count"`
}

// GetRecentConversations returns contacts that have messages, with last message.
func GetRecentConversations() ([]Conversation, error) {
	rows, err := LocalDB.Query(
		`SELECT c.id, c.phone, IFNULL(c.name,''), IFNULL(c.labels,''), c.last_message_at, c.created_at,
		        m.id, m.direction, m.body, m.status, m.created_at
		 FROM contacts c
		 INNER JOIN messages m ON m.id = (
		     SELECT MAX(id) FROM messages WHERE phone = c.phone
		 )
		 ORDER BY m.created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var convos []Conversation
	for rows.Next() {
		var cv Conversation
		if err := rows.Scan(
			&cv.Contact.ID, &cv.Contact.Phone, &cv.Contact.Name, &cv.Contact.Labels,
			&cv.Contact.LastMessageAt, &cv.Contact.CreatedAt,
			&cv.LastMessage.ID, &cv.LastMessage.Direction, &cv.LastMessage.Body,
			&cv.LastMessage.Status, &cv.LastMessage.CreatedAt,
		); err != nil {
			continue
		}
		cv.LastMessage.Phone = cv.Contact.Phone
		convos = append(convos, cv)
	}
	return convos, nil
}
