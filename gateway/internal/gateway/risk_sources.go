package gateway

import "context"

// RiskKeySource counts hit requests associated with one calling credential.
type RiskKeySource struct {
	CredentialID string `json:"credential_id"`
	MaskedKey    string `json:"masked_key"`
	Count        int64  `json:"count"`
}

// RiskIPSource counts hit requests from one client IP.
type RiskIPSource struct {
	ClientIP string `json:"client_ip"`
	Count    int64  `json:"count"`
}

// RiskSources contains the highest-volume credentials and IPs for hit requests.
type RiskSources struct {
	Keys []RiskKeySource `json:"keys"`
	IPs  []RiskIPSource  `json:"ips"`
}

// RiskSourceReader reads hit-source rankings for an analytics filter.
type RiskSourceReader interface {
	RiskSources(context.Context, AnalyticsFilter) (RiskSources, error)
}
