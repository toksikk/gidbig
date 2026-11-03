package react

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/bwmarrin/discordgo"
)

// historyLimit is how many recent channel messages are searched for the
// target user's newest message.
const historyLimit = 20

var (
	errOptionCount   = errors.New("give exactly one of `user` or `message_id`")
	errBadMessageID  = errors.New("`message_id` is not a valid message ID")
	errNoUserMessage = errors.New("no message from that user among the last 20 in this channel")
)

// validateOptions requires exactly one of user and messageID.
func validateOptions(userID, messageID string) error {
	if (userID == "") == (messageID == "") {
		return errOptionCount
	}
	if messageID != "" {
		if _, err := strconv.ParseUint(messageID, 10, 64); err != nil {
			return errBadMessageID
		}
	}
	return nil
}

// findLastMessage returns the newest message by userID. Discord returns
// channel history newest first.
func findLastMessage(msgs []*discordgo.Message, userID string) *discordgo.Message {
	for _, m := range msgs {
		if m != nil && m.Author != nil && m.Author.ID == userID {
			return m
		}
	}
	return nil
}

// resolveTarget returns the target message ID in channelID.
func resolveTarget(s *discordgo.Session, channelID, userID, messageID string) (string, error) {
	if messageID != "" {
		msg, err := s.ChannelMessage(channelID, messageID)
		if err != nil {
			return "", fmt.Errorf("react: fetch message: %w", err)
		}
		return msg.ID, nil
	}
	msgs, err := s.ChannelMessages(channelID, historyLimit, "", "", "")
	if err != nil {
		return "", fmt.Errorf("react: fetch messages: %w", err)
	}
	msg := findLastMessage(msgs, userID)
	if msg == nil {
		return "", errNoUserMessage
	}
	return msg.ID, nil
}
