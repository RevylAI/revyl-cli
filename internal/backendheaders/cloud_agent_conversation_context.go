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
	revylCloudAgentProviderEnv             = "REVYL_CLOUD_AGENT_PROVIDER"
	revylCloudAgentConversationIDEnv       = "REVYL_CLOUD_AGENT_CONVERSATION_ID"
	capyProviderKey                        = "capy"
)

// validCursorCloudAgentID matches the backend Cursor Cloud Agent identity.
// Desktop Cursor chat UUIDs must not be forwarded as Cloud Agent correlation.
var validCursorCloudAgentID = regexp.MustCompile(
	`^bc-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
)

// validRevylConversationID matches the canonical Revyl conversation UUID the
// backend accepts from a Revyl-launched Capy thread.
var validRevylConversationID = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
)

// SetCloudAgentConversationContext adds Cloud Agent correlation headers.
//
// A Capy thread Revyl launched carries REVYL_CLOUD_AGENT_PROVIDER=capy and
// its Revyl conversation UUID in REVYL_CLOUD_AGENT_CONVERSATION_ID, because
// Capy exposes no thread identity of its own. Otherwise only a real Cursor
// Cloud Agent ID (`bc-<uuid>`) from CURSOR_CONVERSATION_ID is forwarded; desktop
// Cursor chats also set that variable, but their chat UUIDs must not be
// claimed as cursor_cloud context.
//
// Parameters:
//   - request: Outbound backend request that may receive correlation headers.
func SetCloudAgentConversationContext(request *http.Request) {
	provider, conversationID, ok := cloudAgentConversationContext()
	if !ok {
		return
	}
	request.Header.Set(cloudAgentProviderHeader, provider)
	request.Header.Set(cloudAgentProviderConversationIDHeader, conversationID)
}

// cloudAgentConversationContext reads the provider key and conversation ID
// this process may present, preferring the pair Revyl set at launch.
//
// Returns:
//   - string: Provider key.
//   - string: Provider conversation ID.
//   - bool: False when the environment names no valid conversation.
func cloudAgentConversationContext() (string, string, bool) {
	if os.Getenv(revylCloudAgentProviderEnv) == capyProviderKey {
		conversationID := os.Getenv(revylCloudAgentConversationIDEnv)
		if validRevylConversationID.MatchString(conversationID) {
			return capyProviderKey, conversationID, true
		}
	}
	if conversationID := os.Getenv(cursorConversationIDEnv); isValidCursorCloudAgentID(conversationID) {
		return cursorCloudProviderKey, conversationID, true
	}
	return "", "", false
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
