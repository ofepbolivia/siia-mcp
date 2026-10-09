package config

const redactedValue = "[REDACTED]"

func (cfg Config) Redacted() Config {
	out := cfg
	out.Audit.Path = redactedValue
	if cfg.Connection != nil {
		conn := *cfg.Connection
		conn.Password = redactedValue
		out.Connection = &conn
	}
	return out
}
