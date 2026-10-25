package obmodel

// SQLRates holds per-stat_id rates derived from adjacent GV$SYSSTAT samples.
// Ready is false during the first baseline sample (warming up).
// Unknown rates must not be treated as zero: use Rate/Sum which return ok=false.
type SQLRates struct {
	Ready  bool
	Values map[int64]float64
}

func (r SQLRates) Rate(statID int64) (float64, bool) {
	if !r.Ready {
		return 0, false
	}
	value, ok := r.Values[statID]
	return value, ok
}

func (r SQLRates) Sum(statIDs ...int64) (float64, bool) {
	if !r.Ready {
		return 0, false
	}

	var total float64
	for _, statID := range statIDs {
		value, ok := r.Values[statID]
		if !ok {
			return 0, false
		}
		total += value
	}
	return total, true
}

func CloneSQLRates(rates SQLRates) SQLRates {
	values := make(map[int64]float64, len(rates.Values))
	for statID, value := range rates.Values {
		values[statID] = value
	}
	rates.Values = values
	return rates
}
