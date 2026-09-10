package metrics

import "strings"

func IsValidator() bool {
	metricsMutex.Lock()
	defer metricsMutex.Unlock()
	return nodeIdentity.IsValidator
}

func GetValidatorStakes() map[string]float64 {
	metricsMutex.RLock()
	defer metricsMutex.RUnlock()

	stakes := make(map[string]float64)
	if values, exists := labeledValues[HLConsensusValidatorStakeGauge]; exists {
		for validator, value := range values {
			stakes[validator] = value.value
		}
	}
	return stakes
}

// returns the moniker registered for a validator address, or "" if unknown
func GetValidatorName(validatorAddr string) string {
	_, name, _ := GetValidatorInfo(strings.ToLower(validatorAddr))
	return name
}
