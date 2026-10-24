package ui

import (
	"fmt"

	"github.com/chen8103/obtop/obmodel"
)

func formatSQLMetrics(rates obmodel.SQLRates, metricsErr error) string {
	if !rates.Ready {
		if metricsErr != nil {
			return "[yellow]OVERVIEW[-]  OPS unavailable\n" +
				"[yellow]STATUS[-]    Metrics error: " + metricsErr.Error() + "\n" +
				"[yellow]DETAIL[-]    SQL metrics waiting for a valid sample"
		}
		return "[yellow]OVERVIEW[-]  OPS warming up...\n" +
			"[yellow]READ[-]      SQL statement rates pending\n" +
			"[yellow]WRITE[-]     SQL statement rates pending"
	}

	sqlStatIDs := []int64{
		obmodel.StatSQLSelect,
		obmodel.StatSQLInsert,
		obmodel.StatSQLReplace,
		obmodel.StatSQLUpdate,
		obmodel.StatSQLDelete,
		obmodel.StatSQLOther,
	}
	opsStatIDs := append(append([]int64{}, sqlStatIDs...), obmodel.StatSQLCommit, obmodel.StatSQLRollback)
	ops, opsOK := rates.Sum(opsStatIDs...)
	qps, qpsOK := rates.Sum(sqlStatIDs...)

	lineOne := fmt.Sprintf(
		"[yellow]OVERVIEW[-]  OPS [green]%s/s[-]  |  SQL QPS [green]%s/s[-]  |  COMMIT [aqua]%s/s[-]  |  ROLLBACK [aqua]%s/s[-]",
		formatRate(ops, opsOK),
		formatRate(qps, qpsOK),
		formatMetricRate(rates, obmodel.StatSQLCommit),
		formatMetricRate(rates, obmodel.StatSQLRollback),
	)
	if metricsErr != nil {
		lineOne += "  [red]STALE[-]"
	}
	lineTwo := fmt.Sprintf(
		"[yellow]READ[-]      SELECT [green]%s/s[-]",
		formatMetricRate(rates, obmodel.StatSQLSelect),
	)
	lineThree := fmt.Sprintf(
		"[yellow]WRITE[-]     INSERT [aqua]%s/s[-]  |  UPDATE [aqua]%s/s[-]  |  DELETE [aqua]%s/s[-]  |  REPLACE [aqua]%s/s[-]  |  OTHER [white]%s/s[-]",
		formatMetricRate(rates, obmodel.StatSQLInsert),
		formatMetricRate(rates, obmodel.StatSQLUpdate),
		formatMetricRate(rates, obmodel.StatSQLDelete),
		formatMetricRate(rates, obmodel.StatSQLReplace),
		formatMetricRate(rates, obmodel.StatSQLOther),
	)
	return lineOne + "\n" + lineTwo + "\n" + lineThree
}

func formatMetricRate(rates obmodel.SQLRates, statID int64) string {
	value, ok := rates.Rate(statID)
	return formatRate(value, ok)
}

func formatRate(value float64, available bool) string {
	if !available {
		return "N/A"
	}
	switch {
	case value >= 1000000:
		return fmt.Sprintf("%.1fM", value/1000000)
	case value >= 1000:
		return fmt.Sprintf("%.1fk", value/1000)
	default:
		return fmt.Sprintf("%.1f", value)
	}
}
