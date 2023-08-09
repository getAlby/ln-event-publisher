package main

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/getAlby/ln-event-publisher/lnd"
	"github.com/getsentry/sentry-go"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/sirupsen/logrus"
)

func main() {
	c := &Config{}
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
	db, err := OpenDB(c)
	if err != nil {
		sentry.CaptureException(err)
		logrus.Fatal(err)
	}
	//create buffered channels to handle publisher confirms
	//so we don't block publishing if there is a peak in traffic
	cci := make(chan InvoiceConfirmation, 100)
	ccp := make(chan PaymentConfirmation, 100)
	svc := &Service{
		cfg:                    c,
		lnd:                    client,
		db:                     db,
		confirmChannelInvoices: cci,
		confirmChannelPayments: ccp,
	}
	err = svc.InitRabbitMq()
	if err != nil {
		sentry.CaptureException(err)
		logrus.Fatal(err)
	}
	backgroundCtx := context.Background()
	ctx, _ := signal.NotifyContext(backgroundCtx, os.Interrupt)

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		err = svc.startInvoiceSubscription(ctx)
		if err != nil && !strings.Contains(err.Error(), context.Canceled.Error()) {
			logrus.Fatal(err)
		}
		logrus.Info("invoice subscription loop done")
		wg.Done()
	}()
	wg.Add(1)
	go func() {
		//listen to confirm channel from invoices
		//and update apropriately in the database
		err = svc.StartInvoiceConfirmationLoop(ctx)
		if err != nil && !strings.Contains(err.Error(), context.Canceled.Error()) {
			logrus.Fatal(err)
		}
		logrus.Info("invoice confirmation loop done")
		wg.Done()
	}()
	wg.Add(1)
	go func() {
		err = svc.startPaymentSubscription(ctx)
		if err != nil && !strings.Contains(err.Error(), context.Canceled.Error()) {
			logrus.Fatal(err)
		}
		logrus.Info("payment subscription loop done")
		wg.Done()
	}()
	wg.Add(1)
	go func() {
		//listen to confirm channel from payments
		//and update apropriately in the database
		err = svc.StartPaymentConfirmationLoop(ctx)
		if err != nil && !strings.Contains(err.Error(), context.Canceled.Error()) {
			logrus.Fatal(err)
		}

		logrus.Info("payment confirmation loop done")
		wg.Done()
	}()
	<-ctx.Done()
	// start goroutine that will exit program after 10 seconds
	go func() {
		time.Sleep(10 * time.Second)
		logrus.Fatal("Exiting because of timeout. Goodbye")

	}()
	//wait for goroutines to finish
	wg.Wait()
	logrus.Info("Exited gracefully. Goodbye.")
}
