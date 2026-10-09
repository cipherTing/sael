package store

import (
	"context"

	"github.com/cipherTing/sael/gateway/internal/gateway"
)

// RiskSources reads indexed metadata, never the retained prompt bodies. Each
// hit is one request; matching several scenes does not multiply its count.
func (s *PG) RiskSources(ctx context.Context, f gateway.AnalyticsFilter) (gateway.RiskSources, error) {
	out := gateway.RiskSources{Keys: []gateway.RiskKeySource{}, IPs: []gateway.RiskIPSource{}}
	rows, err := s.pool.Query(ctx, `WITH filtered AS MATERIALIZED (
 SELECT risk_credential_id, risk_client_ip FROM audit_events
 WHERE kind='hit' AND request_source='gateway' AND time >= $1 AND time < $2
 AND risk_protocol IN ('openai_chat','openai_responses','anthropic','openai_images','openai_images_generations','openai_images_edits')
 AND ($3='' OR risk_protocol=$3 OR ($3='openai_images' AND risk_protocol IN ('openai_images_generations','openai_images_edits')))
 AND ($4='' OR risk_model=$4)
 ) SELECT source_type, source, n FROM (
 SELECT 'key' AS source_type, source, n FROM (
 SELECT risk_credential_id AS source,count(*) AS n FROM filtered WHERE risk_credential_id<>'' GROUP BY 1 ORDER BY 2 DESC,1 LIMIT 10
 ) keys
 UNION ALL
 SELECT 'ip' AS source_type, source, n FROM (
 SELECT risk_client_ip AS source,count(*) AS n FROM filtered WHERE risk_client_ip<>'' GROUP BY 1 ORDER BY 2 DESC,1 LIMIT 10
 ) ips
 ) sources ORDER BY source_type,n DESC,source`, f.Since, f.Until, f.Endpoint, f.Model)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var kind, source string
		var n int64
		if err := rows.Scan(&kind, &source, &n); err != nil {
			rows.Close()
			return out, err
		}
		if kind == "key" {
			out.Keys = append(out.Keys, gateway.RiskKeySource{CredentialID: source, Count: n})
		} else {
			out.IPs = append(out.IPs, gateway.RiskIPSource{ClientIP: source, Count: n})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	events := make([]gateway.Event, len(out.Keys))
	for i, key := range out.Keys {
		events[i].CredentialID = key.CredentialID
	}
	if err := s.hydrateCredentials(ctx, events); err != nil {
		return out, err
	}
	for i := range out.Keys {
		out.Keys[i].MaskedKey = events[i].MaskedKey
	}
	return out, nil
}
