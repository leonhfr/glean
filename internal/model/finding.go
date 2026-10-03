package model

// FindingStatus states what static evidence establishes about a requirement.
type FindingStatus string

// Static findings are not runtime approval.
const (
	FindingVerified    FindingStatus = "verified"
	FindingMissing     FindingStatus = "missing"
	FindingUnknown     FindingStatus = "unknown"
	FindingUnsupported FindingStatus = "unsupported"
)

// Finding describes a requirement or unsupported semantic without secret values.
// An empty Capability scopes the finding to the package. Reference is an identifier
// or payload-relative path, never a resolved credential or configuration value.
type Finding struct {
	Code       string
	Status     FindingStatus
	Capability CapabilityID
	Reference  string
	Message    string
	Locations  []Location
}
