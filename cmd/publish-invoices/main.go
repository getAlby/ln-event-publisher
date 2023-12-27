package main

import (
	"context"
	"github.com/getAlby/ln-event-publisher/config"
	"github.com/getAlby/ln-event-publisher/db"
	"github.com/getAlby/ln-event-publisher/lnd"
	"github.com/getAlby/ln-event-publisher/service"
	"github.com/getsentry/sentry-go"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/sirupsen/logrus"
)

func main() {
	c := &config.Config{}
	logrus.SetFormatter(&logrus.JSONFormatter{})

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
	logrus.Info("Opening PG database")
	db, err := db.OpenDB(c)
	if err != nil {
		sentry.CaptureException(err)
		logrus.Fatal(err)
	}
	svc := &service.Service{
		Cfg: c,
		Lnd: client,
		Db:  db,
	}
	err = svc.InitRabbitMq()
	if err != nil {
		sentry.CaptureException(err)
		logrus.Fatal(err)
	}

}
