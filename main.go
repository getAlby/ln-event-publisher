package main

import (
	"context"
	"os"
	"os/signal"

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
	client, err := NewLNDclient(LNDoptions{
		Address:     c.LNDAddress,
		CertHex:     c.LNDCertHex,
		MacaroonHex: c.LNDMacaroonHex,
	})
	if err != nil {
		logrus.Fatalf("Error loading environment variables: %v", err)
	}
	resp, err := client.client.GetInfo(context.Background(), &lnrpc.GetInfoRequest{})
	if err != nil {
		logrus.Fatal(err)
	}
	logrus.Infof("Connected to LND: %s - %s", resp.Alias, resp.IdentityPubkey)
	svc := &Service{
		cfg: c,
		lnd: client,
	}
	err = svc.InitRabbitMq()
	if err != nil {
		logrus.Fatal(err)
	}
	backgroundCtx := context.Background()
	ctx, _ := signal.NotifyContext(backgroundCtx, os.Interrupt)

	addIndex := uint64(0)
	if svc.cfg.DatabaseUri != "" {
		logrus.Info("Opening PG database")
		db, err := OpenDB(svc.cfg)
		if err != nil {
			logrus.Fatal(err)
		}
		svc.db = db
		addIndex, err = svc.lookupLastAddIndex(ctx)
		if err != nil {
			logrus.Fatal(err)
		}
		logrus.Infof("Found last add index in db: %d", addIndex)
	}
	switch svc.cfg.RabbitMQExchangeName {
	case LNDInvoiceExchange:
		logrus.Fatal(svc.startInvoiceSubscription(ctx, addIndex))
	case LNDChannelExchange:
		logrus.Fatal(svc.startChannelEventSubscription(ctx))
	case LNDPaymentExchange:
		logrus.Fatal(svc.startPaymentsSubscription(ctx))
	default:
		logrus.Fatalf("Did not recognize subscription type: %s", svc.cfg.RabbitMQExchangeName)
	}
}
