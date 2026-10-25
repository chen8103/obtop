package service

import (
	"math"
	"testing"
	"time"

	"github.com/chen8103/obtop/obmodel"
)

func TestSQLRateCalculatorUsesPerNodeCounterDeltas(t *testing.T) {
	calculator := newSQLRateCalculator()
	startedAt := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)

	first := calculator.Update([]obmodel.SQLCounter{
		{ServerIP: "10.0.0.1", ServerPort: 2882, StatID: obmodel.StatSQLSelect, Value: 100},
		{ServerIP: "10.0.0.1", ServerPort: 2882, StatID: obmodel.StatSQLInsert, Value: 50},
		{ServerIP: "10.0.0.2", ServerPort: 2882, StatID: obmodel.StatSQLSelect, Value: 200},
	}, startedAt)
	if first.Ready {
		t.Fatalf("first sample must only establish the counter baseline")
	}

	second := calculator.Update([]obmodel.SQLCounter{
		{ServerIP: "10.0.0.1", ServerPort: 2882, StatID: obmodel.StatSQLSelect, Value: 120},
		{ServerIP: "10.0.0.1", ServerPort: 2882, StatID: obmodel.StatSQLInsert, Value: 56},
		{ServerIP: "10.0.0.2", ServerPort: 2882, StatID: obmodel.StatSQLSelect, Value: 210},
	}, startedAt.Add(2*time.Second))

	assertSQLRate(t, second, obmodel.StatSQLSelect, 15)
	assertSQLRate(t, second, obmodel.StatSQLInsert, 3)
}

func TestSQLRateCalculatorSkipsResetAndNewNodeCounters(t *testing.T) {
	calculator := newSQLRateCalculator()
	startedAt := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	calculator.Update([]obmodel.SQLCounter{
		{ServerIP: "10.0.0.1", ServerPort: 2882, StatID: obmodel.StatSQLSelect, Value: 100},
		{ServerIP: "10.0.0.2", ServerPort: 2882, StatID: obmodel.StatSQLSelect, Value: 200},
	}, startedAt)

	rates := calculator.Update([]obmodel.SQLCounter{
		{ServerIP: "10.0.0.1", ServerPort: 2882, StatID: obmodel.StatSQLSelect, Value: 5},
		{ServerIP: "10.0.0.2", ServerPort: 2882, StatID: obmodel.StatSQLSelect, Value: 212},
		{ServerIP: "10.0.0.3", ServerPort: 2882, StatID: obmodel.StatSQLSelect, Value: 50},
	}, startedAt.Add(time.Second))

	assertSQLRate(t, rates, obmodel.StatSQLSelect, 12)
}

func assertSQLRate(t *testing.T, rates obmodel.SQLRates, statID int64, want float64) {
	t.Helper()

	got, ok := rates.Rate(statID)
	if !ok {
		t.Fatalf("rate for stat ID %d is unavailable", statID)
	}
	if math.Abs(got-want) > 0.0001 {
		t.Fatalf("rate for stat ID %d = %.4f, want %.4f", statID, got, want)
	}
}
