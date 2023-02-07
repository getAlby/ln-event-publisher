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
	//start payment loop
	//lnd doesn't support this yet LOL
	//go func() {
	//	err = svc.startPaymentsSubscription(backgroundCtx)
	//	if err != nil {
	//		logrus.Error(err)
	//	}
	//}()
	//start invoice loop
	logrus.Error(svc.startInvoiceSubscription(ctx))
}
