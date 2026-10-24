package service

import (
	"time"

	"github.com/chen8103/obtop/obmodel"
)

type sqlCounterKey struct {
	tenantID   int64
	serverIP   string
	serverPort int64
	statID     int64
}

type sqlRateCalculator struct {
	previous  map[sqlCounterKey]int64
	sampledAt time.Time
}

func newSQLRateCalculator() *sqlRateCalculator {
	return &sqlRateCalculator{}
}

func (c *sqlRateCalculator) Update(counters []obmodel.SQLCounter, sampledAt time.Time) obmodel.SQLRates {
	current := make(map[sqlCounterKey]int64, len(counters))
	for _, counter := range counters {
		key := sqlCounterKey{
			tenantID:   counter.TenantID,
			serverIP:   counter.ServerIP,
			serverPort: counter.ServerPort,
			statID:     counter.StatID,
		}
		current[key] = counter.Value
	}

	if c.previous == nil || !sampledAt.After(c.sampledAt) {
		c.previous = current
		c.sampledAt = sampledAt
		return obmodel.SQLRates{}
	}

	elapsedSeconds := sampledAt.Sub(c.sampledAt).Seconds()
	values := make(map[int64]float64)
	for key, currentValue := range current {
		previousValue, ok := c.previous[key]
		if !ok || currentValue < previousValue {
			continue
		}
		values[key.statID] += float64(currentValue-previousValue) / elapsedSeconds
	}

	c.previous = current
	c.sampledAt = sampledAt
	return obmodel.SQLRates{Ready: true, Values: values}
}
