package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/getAlby/lndhub.go/lnd"
	"github.com/getsentry/sentry-go"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/sirupsen/logrus"
)

func main() {
	c := &Config{}

	// Load configruation from environment variables
	err := godotenv.Load(".env")
	if err != nil {
		logrus.Warn("Failed to load .env file")
	}
	err = envconfig.Process("", c)
	if err != nil {
		logrus.Fatalf("Error loading environment variables: %v", err)
	}

	// Setup exception tracking with Sentry if configured
	if c.SentryDSN != "" {
		if err = sentry.Init(sentry.ClientOptions{
			Dsn: c.SentryDSN,
		}); err != nil {
			logrus.Error(err)
		}
	}
	client, err := lnd.NewLNDclient(lnd.LNDoptions{
		Address:      c.LNDAddress,
		MacaroonFile: c.LNDMacaroonFile,
		CertFile:     c.LNDCertFile,
	})
	if err != nil {
		sentry.CaptureException(err)
		logrus.Fatalf("Error loading environment variables: %v", err)
	}
	resp, err := client.GetInfo(context.Background(), &lnrpc.GetInfoRequest{})
	if err != nil {
		sentry.CaptureException(err)
		logrus.Fatal(err)
	}
	logrus.Infof("Connected to LND: %s - %s", resp.Alias, resp.IdentityPubkey)
	svc := &Service{
		cfg: c,
		lnd: client,
	}
	err = svc.InitRabbitMq()
	if err != nil {
		sentry.CaptureException(err)
		logrus.Fatal(err)
	}
	backgroundCtx := context.Background()
	ctx, _ := signal.NotifyContext(backgroundCtx, os.Interrupt)

	addIndex := uint64(0)
	if svc.cfg.DatabaseUri != "" {
		logrus.Info("Opening PG database")
		db, err := OpenDB(svc.cfg)
		if err != nil {
			sentry.CaptureException(err)
			logrus.Fatal(err)
		}
		svc.db = db
		addIndex, err = svc.lookupLastAddIndex(ctx)
		if err != nil {
			sentry.CaptureException(err)
			logrus.Fatal(err)
		}
		logrus.Infof("Found last add index in db: %d", addIndex)
	} else {
		logrus.Info("Starting without a PG database")
	}
	switch svc.cfg.RabbitMQExchangeName {
	case LNDInvoiceExchange:
		logrus.Fatal(svc.startInvoiceSubscription(ctx, addIndex))
	default:
		logrus.Fatalf("Did not recognize subscription type: %s", svc.cfg.RabbitMQExchangeName)
	}
}
