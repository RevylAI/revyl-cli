package backendheaders

import (
	"net/http"
	"strings"
	"testing"
)

const validCursorCloudAgentIDFixture = "bc-11111111-1111-4111-8111-111111111111"

func TestSetCloudAgentConversationContext(t *testing.T) {
	tests := []struct {
		name                   string
		conversationID         string
		expectedProvider       string
		expectedConversationID string
	}{
		{
			name: "missing",
		},
		{
			name:                   "cursor cloud agent id",
			conversationID:         validCursorCloudAgentIDFixture,
			expectedProvider:       cursorCloudProviderKey,
			expectedConversationID: validCursorCloudAgentIDFixture,
		},
		{
			name:           "desktop chat uuid",
			conversationID: "76110654-39cb-47d0-ab12-9a4522a15f9e",
		},
		{
			name:           "legacy loose token",
			conversationID: "bc_123-abc.def~ghi",
		},
		{
			name:           "leading whitespace",
			conversationID: " " + validCursorCloudAgentIDFixture,
		},
		{
			name:           "uppercase",
			conversationID: "BC-11111111-1111-4111-8111-111111111111",
		},
		{
			name:           "path separator",
			conversationID: "bc_123/child",
		},
		{
			name:           "parent traversal",
			conversationID: "bc_123..child",
		},
		{
			name:           "too long",
			conversationID: strings.Repeat("a", 256),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(cursorConversationIDEnv, test.conversationID)
			request, err := http.NewRequest(
				http.MethodGet,
				"https://backend.revyl.ai",
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}

			SetCloudAgentConversationContext(request)
			if got := request.Header.Get(cloudAgentProviderHeader); got != test.expectedProvider {
				t.Fatalf("provider header = %q, want %q", got, test.expectedProvider)
			}
			if got := request.Header.Get(cloudAgentProviderConversationIDHeader); got != test.expectedConversationID {
				t.Fatalf(
					"provider conversation ID header = %q, want %q",
					got,
					test.expectedConversationID,
				)
			}
		})
	}
}
