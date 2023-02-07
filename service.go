package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/lnrpc/routerrpc"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/sirupsen/logrus"
)

type Config struct {
	LNDAddress              string `envconfig:"LND_ADDRESS" required:"true"`
	LNDMacaroonHex          string `envconfig:"LND_MACAROON_HEX"`
	LNDCertHex              string `envconfig:"LND_CERT_HEX"`
	RabbitMQInvoiceExchange string `envconfig:"RABBITMQ_INVOICE_EXCHANGE" default:"lnd_invoices"`
	RabbitMQUri             string `envconfig:"RABBITMQ_URI"`
}
type Service struct {
	cfg       *Config
	lnd       *LNDWrapper
	publisher *amqp.Channel
}

func (svc *Service) InitRabbitMq() (err error) {
	conn, err := amqp.Dial(svc.cfg.RabbitMQUri)
	if err != nil {
		return err
	}
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	err = ch.ExchangeDeclare(
		//TODO: review exchange config
		svc.cfg.RabbitMQInvoiceExchange,
		"topic", // type
		true,    // durable
		false,   // auto-deleted
		false,   // internal
		false,   // no-wait
		nil,     // arguments
	)
	if err != nil {
		return err
	}
	svc.publisher = ch
	return
}

func (svc *Service) startPaymentsSubscription(ctx context.Context) error {
	paymentsSub, err := svc.lnd.routerClient.TrackPayments(ctx, &routerrpc.TrackPaymentsRequest{
		NoInflightUpdates: true,
	})
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("Context canceled")
		default:
			payment, err := paymentsSub.Recv()
			if err != nil {
				return err
			}
			fmt.Println(payment.PaymentRequest)
		}
	}
}

func (svc *Service) startInvoiceSubscription(ctx context.Context) error {
	invoiceSub, err := svc.lnd.client.SubscribeInvoices(ctx, &lnrpc.InvoiceSubscription{})
	if err != nil {
		return err
	}
	logrus.Info("Starting invoice subscription")
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("Context canceled")
		default:
			inv, err := invoiceSub.Recv()
			if err != nil {
				return err
			}
			err = svc.ProcessInvoice(ctx, inv)
			if err != nil {
				return err
			}
		}
	}
}

func (svc *Service) ProcessInvoice(ctx context.Context, invoice *lnrpc.Invoice) error {
	payload := new(bytes.Buffer)
	err := json.NewEncoder(payload).Encode(invoice)
	if err != nil {
		return err
	}
	if invoice.State == lnrpc.Invoice_SETTLED {
		logrus.Infof("Publishing invoice with hash %s", hex.EncodeToString(invoice.RHash))
		return svc.publisher.PublishWithContext(
			ctx,
			//todo from config
			svc.cfg.RabbitMQInvoiceExchange, "lnd_invoices.incoming.settled", false, false, amqp.Publishing{
				ContentType: "application/json",
				Body:        payload.Bytes(),
			},
		)
	}
	return nil
}
