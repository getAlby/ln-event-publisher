package config

type Config struct {
	LNDAddress              string   `envconfig:"LND_ADDRESS" required:"true"`
	LNDMacaroonFile         string   `envconfig:"LND_MACAROON_FILE"`
	LNDCertFile             string   `envconfig:"LND_CERT_FILE"`
	DatabaseUri             string   `envconfig:"DATABASE_URI" required:"true"`
	DatabaseMaxConns        int      `envconfig:"DATABASE_MAX_CONNS" default:"10"`
	DatabaseMaxIdleConns    int      `envconfig:"DATABASE_MAX_IDLE_CONNS" default:"5"`
	DatabaseConnMaxLifetime int      `envconfig:"DATABASE_CONN_MAX_LIFETIME" default:"1800"` // 30 minutes
	RabbitMQUri             string   `envconfig:"RABBITMQ_URI" required:"true"`
	RabbitMQTimeoutSeconds  int      `envconfig:"RABBITMQ_TIMEOUT_SECONDS" default:"10"`
	SentryDSN               string   `envconfig:"SENTRY_DSN"`
	RepublishInvoiceHashes  []string `envconfig:"REPUBLISH_INVOICE_HASHES"`
}
