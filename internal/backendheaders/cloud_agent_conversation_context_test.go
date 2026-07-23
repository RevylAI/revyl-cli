package backendheaders

import (
	"net/http"
	"strings"
	"testing"
)

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
			name:                   "valid",
			conversationID:         "bc_123-abc.def~ghi",
			expectedProvider:       cursorCloudProviderKey,
			expectedConversationID: "bc_123-abc.def~ghi",
		},
		{
			name:           "leading whitespace",
			conversationID: " bc_123",
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
