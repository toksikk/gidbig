package gippity

import (
	"database/sql"
	"log/slog"
)

// AdminGetUserPrivacy returns true (privacy on/anonymized) for a user.
// Defaults to true if no explicit setting exists. Acquires dbMu for safe concurrent access.
func AdminGetUserPrivacy(userID string) bool {
	dbMu.Lock()
	defer dbMu.Unlock()
	var enabled int
	err := database.QueryRow(`SELECT privacy_enabled FROM user_privacy WHERE user_id = ?`, userID).Scan(&enabled)
	if err == sql.ErrNoRows {
		return true
	}
	if err != nil {
		slog.Error("admin: error querying user_privacy", "error", err)
		return true
	}
	return enabled != 0
}

// AdminGetAllUserPrivacy returns a map of userID -> privacy_enabled for all users
// with an explicit setting in the database.
func AdminGetAllUserPrivacy() (map[string]bool, error) {
	dbMu.Lock()
	defer dbMu.Unlock()
	rows, err := database.Query(`SELECT user_id, privacy_enabled FROM user_privacy ORDER BY user_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make(map[string]bool)
	for rows.Next() {
		var uid string
		var enabled int
		if err := rows.Scan(&uid, &enabled); err != nil {
			return nil, err
		}
		result[uid] = enabled != 0
	}
	return result, nil
}

// AdminHasConversationHistory returns true if the user has any stored chat messages.
func AdminHasConversationHistory(userID string) bool {
	dbMu.Lock()
	defer dbMu.Unlock()
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chat_history WHERE user_id = ?`, userID).Scan(&count); err != nil {
		return false
	}
	return count > 0
}

// AdminGetUsersWithHistory returns all distinct user IDs that have stored chat history.
func AdminGetUsersWithHistory() ([]string, error) {
	dbMu.Lock()
	defer dbMu.Unlock()
	rows, err := database.Query(`SELECT DISTINCT user_id FROM chat_history ORDER BY user_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var users []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		users = append(users, uid)
	}
	return users, nil
}
