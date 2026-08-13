package backendheaders

import (
	"net/http"
	"os"
	"regexp"
)

const (
	cloudAgentProviderHeader               = "X-Revyl-Cloud-Agent-Provider"
	cloudAgentProviderConversationIDHeader = "X-Revyl-Cloud-Agent-Provider-Conversation-Id"
	cursorConversationIDEnv                = "CURSOR_CONVERSATION_ID"
	cursorCloudProviderKey                 = "cursor_cloud"
)

// validCursorCloudAgentID matches the backend Cursor Cloud Agent identity.
// Desktop Cursor chat UUIDs must not be forwarded as Cloud Agent correlation.
var validCursorCloudAgentID = regexp.MustCompile(
	`^bc-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
)

// SetCloudAgentConversationContext adds Cursor Cloud Agent correlation headers.
//
// Only a real Cloud Agent ID (`bc-<uuid>`) is forwarded. Desktop Cursor chats
// also set CURSOR_CONVERSATION_ID, but those values are chat UUIDs and must
// not be claimed as cursor_cloud context.
//
// Parameters:
//   - request: Outbound backend request that may receive correlation headers.
func SetCloudAgentConversationContext(request *http.Request) {
	if conversationID := os.Getenv(cursorConversationIDEnv); isValidCursorCloudAgentID(conversationID) {
		request.Header.Set(cloudAgentProviderHeader, cursorCloudProviderKey)
		request.Header.Set(cloudAgentProviderConversationIDHeader, conversationID)
	}
}

// isValidCursorCloudAgentID reports whether conversationID is a Cursor Cloud Agent ID.
//
// Parameters:
//   - conversationID: Value of CURSOR_CONVERSATION_ID.
//
// Returns:
//   - bool: True only for the backend-accepted `bc-<uuid>` form.
func isValidCursorCloudAgentID(conversationID string) bool {
	return validCursorCloudAgentID.MatchString(conversationID)
}
