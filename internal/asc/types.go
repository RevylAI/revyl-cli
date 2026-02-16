package asc

import "time"

// ASC API uses JSON:API format: { "data": { "type": "...", "id": "...", "attributes": {...} } }

// --- Generic JSON:API wrapper types ---

// Response is a generic JSON:API response with a single resource.
type Response[T any] struct {
	Data     T              `json:"data"`
	Included []ResourceData `json:"included,omitempty"`
	Links    PageLinks      `json:"links,omitempty"`
}

// ListResponse is a generic JSON:API response with multiple resources.
type ListResponse[T any] struct {
	Data     []T            `json:"data"`
	Included []ResourceData `json:"included,omitempty"`
	Links    PageLinks      `json:"links,omitempty"`
	Meta     ListMeta       `json:"meta,omitempty"`
}

// ResourceData is a raw JSON:API resource used for included resources.
type ResourceData struct {
	Type       string                 `json:"type"`
	ID         string                 `json:"id"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
}

// PageLinks contains pagination links.
type PageLinks struct {
	Self string `json:"self,omitempty"`
	Next string `json:"next,omitempty"`
}

// ListMeta contains pagination metadata.
type ListMeta struct {
	Paging PagingInfo `json:"paging,omitempty"`
}

// PagingInfo contains total count and limit.
type PagingInfo struct {
	Total int `json:"total,omitempty"`
	Limit int `json:"limit,omitempty"`
}

// RelationshipData is a JSON:API relationship reference.
type RelationshipData struct {
	Data ResourceRef `json:"data"`
}

// RelationshipListData is a JSON:API relationship reference with multiple targets.
type RelationshipListData struct {
	Data []ResourceRef `json:"data"`
}

// ResourceRef is a minimal JSON:API resource reference (type + id).
type ResourceRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// --- App ---

// App represents an App Store Connect app resource.
type App struct {
	Type       string        `json:"type"`
	ID         string        `json:"id"`
	Attributes AppAttributes `json:"attributes"`
}

// AppAttributes contains the attributes of an App resource.
type AppAttributes struct {
	Name     string `json:"name"`
	BundleID string `json:"bundleId"`
	SKU      string `json:"sku"`
}

// --- Build ---

// Build represents an App Store Connect build resource.
type Build struct {
	Type          string             `json:"type"`
	ID            string             `json:"id"`
	Attributes    BuildAttributes    `json:"attributes"`
	Relationships BuildRelationships `json:"relationships,omitempty"`
}

// BuildAttributes contains the attributes of a Build resource.
type BuildAttributes struct {
	Version                 string     `json:"version"`
	UploadedDate            *time.Time `json:"uploadedDate,omitempty"`
	ExpirationDate          *time.Time `json:"expirationDate,omitempty"`
	Expired                 bool       `json:"expired"`
	MinOsVersion            string     `json:"minOsVersion,omitempty"`
	ProcessingState         string     `json:"processingState"`
	BuildAudienceType       string     `json:"buildAudienceType,omitempty"`
	IconAssetToken          *string    `json:"iconAssetToken,omitempty"`
	UsesNonExemptEncryption *bool      `json:"usesNonExemptEncryption,omitempty"`
}

// BuildRelationships contains the relationships of a Build resource.
type BuildRelationships struct {
	App        *RelationshipData `json:"app,omitempty"`
	PreRelease *RelationshipData `json:"preReleaseVersion,omitempty"`
}

// --- Beta Group ---

// BetaGroup represents a TestFlight beta group.
type BetaGroup struct {
	Type       string              `json:"type"`
	ID         string              `json:"id"`
	Attributes BetaGroupAttributes `json:"attributes"`
}

// BetaGroupAttributes contains the attributes of a BetaGroup resource.
type BetaGroupAttributes struct {
	Name              string `json:"name"`
	IsInternalGroup   bool   `json:"isInternalGroup"`
	PublicLinkEnabled *bool  `json:"publicLinkEnabled,omitempty"`
}

// --- Pre-Release Version ---

// PreReleaseVersion represents a pre-release version.
type PreReleaseVersion struct {
	Type       string                      `json:"type"`
	ID         string                      `json:"id"`
	Attributes PreReleaseVersionAttributes `json:"attributes"`
}

// PreReleaseVersionAttributes contains the attributes of a PreReleaseVersion resource.
type PreReleaseVersionAttributes struct {
	Version  string `json:"version"`
	Platform string `json:"platform"`
}

// --- App Store Version ---

// AppStoreVersion represents an App Store version.
type AppStoreVersion struct {
	Type       string                    `json:"type"`
	ID         string                    `json:"id"`
	Attributes AppStoreVersionAttributes `json:"attributes"`
}

// AppStoreVersionAttributes contains the attributes of an AppStoreVersion resource.
type AppStoreVersionAttributes struct {
	VersionString string  `json:"versionString"`
	AppStoreState string  `json:"appStoreState"`
	Platform      string  `json:"platform"`
	ReleaseType   *string `json:"releaseType,omitempty"`
}

// --- Beta Build Localization ---

// BetaBuildLocalization represents "What to Test" text for a build.
type BetaBuildLocalization struct {
	Type       string                          `json:"type"`
	ID         string                          `json:"id"`
	Attributes BetaBuildLocalizationAttributes `json:"attributes"`
}

// BetaBuildLocalizationAttributes contains the attributes of a BetaBuildLocalization.
type BetaBuildLocalizationAttributes struct {
	WhatsNew string `json:"whatsNew,omitempty"`
	Locale   string `json:"locale"`
}

// --- API Error ---

// APIError represents an error response from the App Store Connect API.
type APIError struct {
	Errors []APIErrorDetail `json:"errors"`
}

// APIErrorDetail contains the detail of a single API error.
type APIErrorDetail struct {
	Status string `json:"status"`
	Code   string `json:"code"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// Error implements the error interface for APIError.
func (e *APIError) Error() string {
	if len(e.Errors) == 0 {
		return "unknown API error"
	}
	return e.Errors[0].Detail
}

// --- Request bodies ---

// AddBuildToBetaGroupRequest is the request body for adding a build to a beta group.
type AddBuildToBetaGroupRequest struct {
	Data []ResourceRef `json:"data"`
}

// CreateBetaBuildLocalizationRequest is the request body for creating "What to Test" text.
type CreateBetaBuildLocalizationRequest struct {
	Data CreateBetaBuildLocalizationData `json:"data"`
}

// CreateBetaBuildLocalizationData is the data for creating a beta build localization.
type CreateBetaBuildLocalizationData struct {
	Type          string                                   `json:"type"`
	Attributes    CreateBetaBuildLocalizationAttributes    `json:"attributes"`
	Relationships CreateBetaBuildLocalizationRelationships `json:"relationships"`
}

// CreateBetaBuildLocalizationAttributes are the attributes for a beta build localization.
type CreateBetaBuildLocalizationAttributes struct {
	WhatsNew string `json:"whatsNew"`
	Locale   string `json:"locale"`
}

// CreateBetaBuildLocalizationRelationships are the relationships for a beta build localization.
type CreateBetaBuildLocalizationRelationships struct {
	Build RelationshipData `json:"build"`
}

// --- Build Upload Types (REST API upload) ---

// BuildDelivery represents a build delivery reservation.
type BuildDelivery struct {
	Type       string                  `json:"type"`
	ID         string                  `json:"id"`
	Attributes BuildDeliveryAttributes `json:"attributes,omitempty"`
}

// BuildDeliveryAttributes contains the attributes of a build delivery.
type BuildDeliveryAttributes struct {
	CfBundleShortVersionString string `json:"cfBundleShortVersionString,omitempty"`
	CfBundleVersion            string `json:"cfBundleVersion,omitempty"`
}

// --- Processing state constants ---

const (
	// ProcessingStateProcessing means the build is still being processed.
	ProcessingStateProcessing = "PROCESSING"

	// ProcessingStateFailed means processing has failed.
	ProcessingStateFailed = "FAILED"

	// ProcessingStateInvalid means the build is invalid.
	ProcessingStateInvalid = "INVALID"

	// ProcessingStateValid means the build is ready.
	ProcessingStateValid = "VALID"
)

// --- App Store state constants ---

const (
	AppStoreStatePrepareForSubmission     = "PREPARE_FOR_SUBMISSION"
	AppStoreStateWaitingForReview         = "WAITING_FOR_REVIEW"
	AppStoreStateInReview                 = "IN_REVIEW"
	AppStoreStateReadyForSale             = "READY_FOR_SALE"
	AppStoreStateRejected                 = "REJECTED"
	AppStoreStateDeveloperRemovedFromSale = "DEVELOPER_REMOVED_FROM_SALE"
)
