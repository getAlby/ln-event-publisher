package main

import (
	"context"
	"fmt"
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
	svc := &Service{
		cfg: c,
		lnd: client,
		db:  db,
	}
	err = svc.InitRabbitMq()
	if err != nil {
		sentry.CaptureException(err)
		logrus.Fatal(err)
	}
	backgroundCtx := context.Background()
	ctx, _ := signal.NotifyContext(backgroundCtx, os.Interrupt)

	//start both subscriptions
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		err = svc.startInvoiceSubscription(ctx)
		if err != nil && !strings.Contains(err.Error(), context.Canceled.Error()) {
			logrus.Fatal(err)
		}
		logrus.Info("invoice routine done")
		wg.Done()
	}()
	wg.Add(1)
	go func() {
		err = svc.startPaymentSubscription(ctx)
		if err != nil && !strings.Contains(err.Error(), context.Canceled.Error()) {
			logrus.Fatal(err)
		}
		logrus.Info("payment routine done")
		wg.Done()
	}()
	<-ctx.Done()
	// start goroutine that will exit program after 10 seconds
	// in case graceful shutdown fails
	go func() {
		time.Sleep(10 * time.Second)
		nonGracefulShutdownErr := fmt.Errorf("non-graceful shutdown because of timeout")
		sentry.CaptureException(nonGracefulShutdownErr)
		logrus.Fatal(nonGracefulShutdownErr)

	}()
	//wait for goroutines to finish
	wg.Wait()
	logrus.Info("Exited gracefully. Goodbye.")
}
