package gateway

import (
	"encoding/json"
	"fmt"

	"github.com/cipherTing/sael/gateway/internal/policy"
)

// PolicyUpdate is either a full replacement or a set of fields to merge under
// the store's policy row lock. Fields omitted from a patch keep their saved value.
type PolicyUpdate struct {
	Replace *policy.Policy
	Fields  map[string]json.RawMessage
}

func (u PolicyUpdate) Apply(current policy.Policy) (policy.Policy, error) {
	var next policy.Policy
	if u.Replace != nil {
		next = *u.Replace
	} else {
		policy.UpgradeLegacy(&current)
		raw, err := json.Marshal(current)
		if err != nil {
			return next, err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return next, err
		}
		for name, value := range u.Fields {
			fields[name] = value
		}
		raw, err = json.Marshal(fields)
		if err != nil {
			return next, err
		}
		if err := json.Unmarshal(raw, &next); err != nil {
			return next, fmt.Errorf("%w: %v", ErrInvalidPolicy, err)
		}
	}
	policy.UpgradeLegacy(&next)
	if err := policy.Validate(next); err != nil {
		return next, fmt.Errorf("%w: %v", ErrInvalidPolicy, err)
	}
	return next, nil
}
