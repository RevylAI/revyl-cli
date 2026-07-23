package backendheaders

import (
	"net/http"
	"os"
	"regexp"
	"strings"
)

const (
	cloudAgentProviderHeader               = "X-Revyl-Cloud-Agent-Provider"
	cloudAgentProviderConversationIDHeader = "X-Revyl-Cloud-Agent-Provider-Conversation-Id"
	cursorConversationIDEnv                = "CURSOR_CONVERSATION_ID"
	cursorCloudProviderKey                 = "cursor_cloud"
)

var validCursorConversationID = regexp.MustCompile(
	`^[A-Za-z0-9][A-Za-z0-9._~-]{0,254}$`,
)

// SetCloudAgentConversationContext adds the available provider conversation context
// to a request using Revyl's provider-neutral Cloud Agent transport headers.
func SetCloudAgentConversationContext(request *http.Request) {
	if conversationID := os.Getenv(cursorConversationIDEnv); isValidCursorConversationID(conversationID) {
		request.Header.Set(cloudAgentProviderHeader, cursorCloudProviderKey)
		request.Header.Set(cloudAgentProviderConversationIDHeader, conversationID)
	}
}

func isValidCursorConversationID(conversationID string) bool {
	return validCursorConversationID.MatchString(conversationID) &&
		!strings.Contains(conversationID, "..")
}
