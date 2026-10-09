package postgres

import (
	"time"

	"github.com/siia/siia-mcp/internal/config"
)

// limits is the resolved, effective set of per-connection limits applied by
// the adapter. Global defaults are the config ceilings used when a connection
// ships no override.
type limits struct {
	RequestTimeout       time.Duration
	QueryBytes           int
	QueryRows            int
	SampleRows           int
	ValueBytes           int
	ResponseBytes        int
	MetadataPageSize     int
	ParameterCount       int
	ParameterBytes       int
	ParametersTotalBytes int
}

func resolveLimits(cfg config.Connection, global config.LimitsConfig) limits {
	l := limits{
		QueryBytes:           valueOr(global.QueryBytes, config.MaxQueryBytes),
		QueryRows:            valueOr(global.QueryRows, config.MaxQueryRows),
		SampleRows:           valueOr(global.SampleRows, config.MaxSampleRows),
		ValueBytes:           valueOr(global.ValueBytes, config.MaxValueBytes),
		ResponseBytes:        valueOr(global.ResponseBytes, config.MaxResponseBytes),
		MetadataPageSize:     valueOr(global.MetadataPageSize, config.MaxMetadataPageSize),
		ParameterCount:       valueOr(global.ParameterCount, config.MaxParameterCount),
		ParameterBytes:       valueOr(global.ParameterBytes, config.MaxParameterBytes),
		ParametersTotalBytes: valueOr(global.ParametersTotalBytes, config.MaxParametersTotalBytes),
	}
	if cfg.Limits != nil {
		l.RequestTimeout = cfg.Limits.RequestTimeout
		l.QueryRows = overrideIfSet(l.QueryRows, cfg.Limits.QueryRows)
		l.SampleRows = overrideIfSet(l.SampleRows, cfg.Limits.SampleRows)
		l.ValueBytes = overrideIfSet(l.ValueBytes, cfg.Limits.ValueBytes)
		l.ResponseBytes = overrideIfSet(l.ResponseBytes, cfg.Limits.ResponseBytes)
		l.MetadataPageSize = overrideIfSet(l.MetadataPageSize, cfg.Limits.MetadataPageSize)
	}
	return l
}

func valueOr(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func overrideIfSet(base, override int) int {
	if override > 0 {
		return override
	}
	return base
}
