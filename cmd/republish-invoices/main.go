package main

import (
	"context"
	"encoding/hex"
	"github.com/getAlby/ln-event-publisher/config"
	"github.com/getAlby/ln-event-publisher/lnd"
	"github.com/getAlby/ln-event-publisher/service"
	"github.com/getsentry/sentry-go"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/sirupsen/logrus"
	"os"
	"os/signal"
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
	svc := &service.Service{
		Cfg: c,
		Lnd: client,
	}
	err = svc.InitRabbitMq()
	if err != nil {
		sentry.CaptureException(err)
		logrus.Fatal(err)
	}
	backgroundCtx := context.Background()
	ctx, _ := signal.NotifyContext(backgroundCtx, os.Interrupt)

	for i := 0; i < len(c.RepublishInvoiceHashes); i++ {
		hashBytes, err := hex.DecodeString(c.RepublishInvoiceHashes[i])
		if err != nil {
			logrus.Error("Invalid Hash ", c.RepublishInvoiceHashes[i], " ", err)
			continue
		}

		// Create a PaymentHash struct
		paymentHash := &lnrpc.PaymentHash{
			RHash: hashBytes,
		}
		svc.RepublishInvoice(ctx, paymentHash)
	}
}
