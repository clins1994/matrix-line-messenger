package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/highesttt/matrix-line-messenger/pkg/line"
)

// backfillCursor stores pagination state between FetchMessages calls.
type backfillCursor struct {
	MessageID     string `json:"message_id"`
	DeliveredTime string `json:"delivered_time"`
}

// fetchBackfillMessages fetches a batch of messages using the appropriate method based on cursor/anchor state.
func (lc *LineClient) fetchBackfillMessages(client *line.Client, chatMid string, count int, params bridgev2.FetchMessagesParams) ([]*line.Message, error) {
	if params.Cursor != "" {
		var cursor backfillCursor
		if err := json.Unmarshal([]byte(params.Cursor), &cursor); err != nil {
			return nil, fmt.Errorf("failed to parse backfill cursor: %w", err)
		}
		return client.GetPreviousMessagesV2WithRequest(line.PreviousMessagesRequest{
			MessageBoxID: chatMid,
			EndMessageID: line.PreviousMessagesAnchor{
				DeliveredTime: cursor.DeliveredTime,
				MessageID:     cursor.MessageID,
			},
			MessagesCount: count,
		})
	} else if params.AnchorMessage != nil {
		anchorID := string(params.AnchorMessage.ID)
		anchorTS := fmt.Sprintf("%d", params.AnchorMessage.Timestamp.UnixMilli())
		return client.GetPreviousMessagesV2WithRequest(line.PreviousMessagesRequest{
			MessageBoxID: chatMid,
			EndMessageID: line.PreviousMessagesAnchor{
				DeliveredTime: anchorTS,
				MessageID:     anchorID,
			},
			MessagesCount: count,
		})
	}
	return client.GetRecentMessagesV2(chatMid, count)
}

func (lc *LineClient) FetchMessages(ctx context.Context, params bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
	client := line.NewClient(lc.AccessToken)
	chatMid := string(params.Portal.PortalKey.ID)

	count := params.Count
	if count <= 0 {
		count = 50
	}

	msgs, err := lc.fetchBackfillMessages(client, chatMid, count, params)

	// Retry with refreshed token on auth errors
	if err != nil && (lc.isRefreshRequired(err) || lc.isLoggedOut(err)) {
		if errRecover := lc.recoverToken(ctx); errRecover == nil {
			client = line.NewClient(lc.AccessToken)
			msgs, err = lc.fetchBackfillMessages(client, chatMid, count, params)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to fetch messages for backfill: %w", err)
	}

	if len(msgs) == 0 {
		return &bridgev2.FetchMessagesResponse{
			Messages: []*bridgev2.BackfillMessage{},
			HasMore:  false,
		}, nil
	}

	// Convert LINE messages to BackfillMessages (oldest first)
	backfillMsgs := make([]*bridgev2.BackfillMessage, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- {
		msg := msgs[i]
		bm := lc.convertToBackfillMessage(ctx, msg, chatMid)
		if bm != nil {
			backfillMsgs = append(backfillMsgs, bm)
		}
	}

	// Build cursor from the oldest message in the batch (first in the original order, last after reversal)
	var nextCursor networkid.PaginationCursor
	oldestMsg := msgs[len(msgs)-1]
	cursorData, _ := json.Marshal(backfillCursor{
		MessageID:     oldestMsg.ID,
		DeliveredTime: oldestMsg.CreatedTime.String(),
	})
	nextCursor = networkid.PaginationCursor(cursorData)

	return &bridgev2.FetchMessagesResponse{
		Messages: backfillMsgs,
		Cursor:   nextCursor,
		HasMore:  len(msgs) >= count,
	}, nil
}

// convertToBackfillMessage converts a single LINE message into a BackfillMessage
// with a pre-populated ConvertedMessage. For text and sticker messages, the full
// content is included. For media messages (image, video, audio, file), a placeholder
// notice is used since media download requires portal/intent context that is not
// available during backfill fetching.
func (lc *LineClient) convertToBackfillMessage(ctx context.Context, msg *line.Message, chatMid string) *bridgev2.BackfillMessage {
	// Filter to supported content types only
	switch ContentType(msg.ContentType) {
	case ContentText, ContentImage, ContentVideo, ContentAudio, ContentSticker, ContentFile:
		// supported
	default:
		return nil
	}

	senderID := makeUserID(msg.From)
	isFromMe := msg.From == lc.Mid || msg.From == string(lc.UserLogin.ID)

	var ts time.Time
	if tsInt, err := msg.CreatedTime.Int64(); err != nil || tsInt == 0 {
		ts = time.Now()
	} else {
		ts = time.UnixMilli(tsInt)
	}

	converted := lc.convertMessageContent(ctx, msg, chatMid)
	if converted == nil {
		return nil
	}

	return &bridgev2.BackfillMessage{
		ConvertedMessage: converted,
		Sender: bridgev2.EventSender{
			Sender:   senderID,
			IsFromMe: isFromMe,
		},
		ID:        networkid.MessageID(msg.ID),
		Timestamp: ts,
	}
}

// convertMessageContent produces a ConvertedMessage for a LINE message.
// Text messages are fully converted with decryption support. Media messages
// produce placeholder notices since full media download/upload requires
// the portal and intent which are not available during backfill fetching.
func (lc *LineClient) convertMessageContent(ctx context.Context, msg *line.Message, chatMid string) *bridgev2.ConvertedMessage {
	bodyText := msg.Text

	// Handle E2EE decryption for text
	if bodyText == "" && len(msg.Chunks) > 0 {
		bodyText = "[Unable to decrypt message]"
		if lc.E2EE != nil {
			lc.ensurePeerKeyForMessage(ctx, msg)

			if ToType(msg.ToType) == ToRoom || ToType(msg.ToType) == ToGroup {
				pt, _, err := lc.E2EE.DecryptGroupMessage(msg, chatMid)
				if err == nil {
					bodyText = pt
				}
			} else {
				if pt, err := lc.E2EE.DecryptMessageV2(msg); err == nil {
					bodyText = pt
				}
			}
		}
	}

	// Unwrap JSON payload
	unwrappedText := bodyText
	if strings.HasPrefix(bodyText, "{") {
		var wrapper map[string]any
		if err := json.Unmarshal([]byte(bodyText), &wrapper); err == nil {
			if t, ok := wrapper["text"].(string); ok {
				unwrappedText = t
			}
		}
	}

	switch ContentType(msg.ContentType) {
	case ContentText:
		if strings.TrimSpace(unwrappedText) == "" {
			return nil
		}
		return &bridgev2.ConvertedMessage{
			Parts: []*bridgev2.ConvertedMessagePart{
				{
					Type: event.EventMessage,
					Content: &event.MessageEventContent{
						MsgType: event.MsgText,
						Body:    unwrappedText,
					},
				},
			},
		}

	case ContentSticker:
		stkTxt := msg.ContentMetadata["STKTXT"]
		if stkTxt == "" {
			stkTxt = "[Sticker]"
		}
		return &bridgev2.ConvertedMessage{
			Parts: []*bridgev2.ConvertedMessagePart{
				{
					Type: event.EventMessage,
					Content: &event.MessageEventContent{
						MsgType: event.MsgText,
						Body:    stkTxt,
					},
				},
			},
		}

	case ContentImage:
		return &bridgev2.ConvertedMessage{
			Parts: []*bridgev2.ConvertedMessagePart{
				{
					Type: event.EventMessage,
					Content: &event.MessageEventContent{
						MsgType: event.MsgNotice,
						Body:    "[Image]",
					},
				},
			},
		}

	case ContentVideo:
		return &bridgev2.ConvertedMessage{
			Parts: []*bridgev2.ConvertedMessagePart{
				{
					Type: event.EventMessage,
					Content: &event.MessageEventContent{
						MsgType: event.MsgNotice,
						Body:    "[Video]",
					},
				},
			},
		}

	case ContentAudio:
		return &bridgev2.ConvertedMessage{
			Parts: []*bridgev2.ConvertedMessagePart{
				{
					Type: event.EventMessage,
					Content: &event.MessageEventContent{
						MsgType: event.MsgNotice,
						Body:    "[Audio message]",
					},
				},
			},
		}

	case ContentFile:
		fileName := msg.ContentMetadata["FILE_NAME"]
		if fileName == "" {
			fileName = "file"
		}
		return &bridgev2.ConvertedMessage{
			Parts: []*bridgev2.ConvertedMessagePart{
				{
					Type: event.EventMessage,
					Content: &event.MessageEventContent{
						MsgType: event.MsgNotice,
						Body:    fmt.Sprintf("[File: %s]", fileName),
					},
				},
			},
		}

	default:
		return nil
	}
}
