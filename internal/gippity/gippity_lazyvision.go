package gippity

import (
	"log/slog"
	"strings"
)

// maxLazyImageDescriptions caps how many not-yet-described images one answer
// may send to the vision model.
const maxLazyImageDescriptions = 3

// describePendingImages describes images posted earlier without a bot mention
// when a question puts them in context: the replied-to message first, then the
// history window newest to oldest. Images of users with privacy on (including
// the default) are never analyzed. Descriptions are cached in
// gippity_chat_attachments. It returns messageID -> description for the images
// described in this call. Failures are logged and skipped.
func (m *Module) describePendingImages(currentID, replyToID string, history []LLMChatMessage) map[string]string {
	candidates := make([]string, 0, len(history)+1)
	seen := map[string]bool{currentID: true}
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		candidates = append(candidates, id)
	}
	add(replyToID)
	for i := len(history) - 1; i >= 0; i-- {
		add(history[i].MessageID)
	}

	rows, err := m.pendingAttachments(candidates)
	if err != nil {
		slog.Warn("gippity: could not load pending attachments", "error", err)
		return nil
	}
	if len(rows) == 0 {
		return nil
	}
	byID := make(map[string]pendingAttachment, len(rows))
	for _, row := range rows {
		byID[row.MessageID] = row
	}

	botUserID := ""
	if m.session != nil && m.session.State != nil && m.session.State.User != nil {
		botUserID = m.session.State.User.ID
	}

	described := map[string]string{}
	attempts := 0
	for _, id := range candidates {
		if attempts >= maxLazyImageDescriptions {
			break
		}
		row, ok := byID[id]
		if !ok || row.UserID == botUserID || m.getUserPrivacy(row.UserID) {
			continue
		}
		urls := m.refreshImageURLs(row)
		if len(urls) == 0 {
			slog.Warn("gippity: no image URL for pending attachment", "messageID", id)
			continue
		}
		attempts++
		desc, err := m.describeImagesFunc(urls)
		if err != nil || desc == "" {
			slog.Warn("gippity: lazy image description failed", "messageID", id, "error", err)
			continue
		}
		if err := m.setAttachmentDescription(id, desc); err != nil {
			slog.Warn("gippity: could not store image description", "messageID", id, "error", err)
		}
		described[id] = desc
	}
	return described
}

// refreshImageURLs re-fetches the message so Discord re-signs its CDN URLs,
// falling back to the stored URLs when the fetch fails.
func (m *Module) refreshImageURLs(row pendingAttachment) []string {
	if m.channelMessageFunc != nil {
		msg, err := m.channelMessageFunc(m.session, row.ChannelID, row.MessageID)
		if err == nil && msg != nil {
			if urls := extractImageURLs(msg.Attachments); len(urls) > 0 {
				return urls
			}
		} else if err != nil {
			slog.Debug("gippity: could not refresh attachment URLs", "messageID", row.MessageID, "error", err)
		}
	}
	if row.AttachmentURL == "" {
		return nil
	}
	return strings.Split(row.AttachmentURL, ",")
}

// imageDescriptions returns the stored, non-empty image descriptions of a message.
func (m *Module) imageDescriptions(messageID string) []string {
	var descs []string
	err := m.db.Model(&ChatAttachment{}).
		Where("message_id = ? AND image_description <> ''", messageID).
		Pluck("image_description", &descs).Error
	if err != nil {
		slog.Warn("gippity: could not load image descriptions", "messageID", messageID, "error", err)
		return nil
	}
	return descs
}
