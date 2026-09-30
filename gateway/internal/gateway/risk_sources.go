package gateway

import "context"

type RiskKeySource struct {
	CredentialID string `json:"credential_id"`
	MaskedKey    string `json:"masked_key"`
	Count        int64  `json:"count"`
}
type RiskIPSource struct {
	ClientIP string `json:"client_ip"`
	Count    int64  `json:"count"`
}
type RiskSources struct {
	Keys []RiskKeySource `json:"keys"`
	IPs  []RiskIPSource  `json:"ips"`
}
type RiskSourceReader interface {
	RiskSources(context.Context, AnalyticsFilter) (RiskSources, error)
}
