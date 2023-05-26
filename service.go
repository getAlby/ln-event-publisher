package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/getAlby/ln-event-publisher/lnd"
	"github.com/getsentry/sentry-go"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/lnrpc/routerrpc"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type Config struct {
	LNDAddress              string `envconfig:"LND_ADDRESS" required:"true"`
	LNDMacaroonFile         string `envconfig:"LND_MACAROON_FILE"`
	LNDCertFile             string `envconfig:"LND_CERT_FILE"`
	DatabaseUri             string `envconfig:"DATABASE_URI"`
	DatabaseMaxConns        int    `envconfig:"DATABASE_MAX_CONNS" default:"10"`
	DatabaseMaxIdleConns    int    `envconfig:"DATABASE_MAX_IDLE_CONNS" default:"5"`
	DatabaseConnMaxLifetime int    `envconfig:"DATABASE_CONN_MAX_LIFETIME" default:"1800"` // 30 minutes
	RabbitMQExchangeName    string `envconfig:"RABBITMQ_EXCHANGE_NAME" default:"lnd_invoice"`
	RabbitMQUri             string `envconfig:"RABBITMQ_URI"`
	SentryDSN               string `envconfig:"SENTRY_DSN"`
}

const (
	LNDInvoiceExchange   = "lnd_invoice"
	LNDChannelExchange   = "lnd_channel"
	LNDPaymentExchange   = "lnd_payment"
	LNDInvoiceRoutingKey = "invoice.incoming.settled"
)

type Service struct {
	cfg           *Config
	lnd           lnd.LightningClientWrapper
	rabbitChannel *amqp.Channel
	db            *gorm.DB
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
		svc.cfg.RabbitMQExchangeName,
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
	svc.rabbitChannel = ch
	return
}
func (svc *Service) lookupLastAddIndex(exchangeName string, ctx context.Context) (result uint64, err error) {
	switch exchangeName {
	case LNDInvoiceExchange:
		//get last item from db
		inv := &Invoice{}
		tx := svc.db.WithContext(ctx).Last(inv)
		if tx.Error != nil && tx.Error != gorm.ErrRecordNotFound {
			return 0, tx.Error
		}
		//return addIndex
		return inv.AddIndex, nil
	case LNDPaymentExchange:
		//todo
		return 0, nil
	default:
		return 0, fmt.Errorf("Unrecognized exchange name %s", exchangeName)
	}
}

func (svc *Service) AddLastPublishedInvoice(ctx context.Context, invoice *lnrpc.Invoice) error {
	return svc.db.WithContext(ctx).Create(&Invoice{
		AddIndex: invoice.AddIndex,
	}).Error
}

func (svc *Service) startPaymentSubscription(ctx context.Context, addIndex uint64) error {
	paymentSub, err := svc.lnd.SubscribePayments(ctx, &routerrpc.TrackPaymentsRequest{})
	if err != nil {
		sentry.CaptureException(err)
		return err
	}
	logrus.Infof("Starting payment subscription from index %d", addIndex)
	for {
		select {
		case <-ctx.Done():
			return context.Canceled
		default:
			payment, err := paymentSub.Recv()
			if err != nil {
				sentry.CaptureException(err)
				return err
			}
			err = svc.ProcessPayment(ctx, payment)
			if err != nil {
				sentry.CaptureException(err)
				return err
			}
		}
	}
}

func (svc *Service) startInvoiceSubscription(ctx context.Context, addIndex uint64) error {
	invoiceSub, err := svc.lnd.SubscribeInvoices(ctx, &lnrpc.InvoiceSubscription{
		AddIndex: addIndex,
	})
	if err != nil {
		sentry.CaptureException(err)
		return err
	}
	logrus.Infof("Starting invoice subscription from index %d", addIndex)
	for {
		select {
		case <-ctx.Done():
			return context.Canceled
		default:
			inv, err := invoiceSub.Recv()
			if err != nil {
				sentry.CaptureException(err)
				return err
			}
			err = svc.ProcessInvoice(ctx, inv)
			if err != nil {
				sentry.CaptureException(err)
				return err
			}
		}
	}
}

func (svc *Service) ProcessPayment(ctx context.Context, payment *lnrpc.Payment) error {
	fmt.Println(payment.Status)
	fmt.Println(payment.PaymentIndex)
	fmt.Println(payment.ValueSat)
	fmt.Println("----------")
	return nil
}

func (svc *Service) ProcessInvoice(ctx context.Context, invoice *lnrpc.Invoice) error {
	if invoice.State == lnrpc.Invoice_SETTLED {
		logrus.Infof("Publishing invoice with hash %s", hex.EncodeToString(invoice.RHash))
		err := svc.PublishPayload(ctx, invoice, svc.cfg.RabbitMQExchangeName, LNDInvoiceRoutingKey)
		if err != nil {
			return err
		}
		//add it to the database if we have one
		if svc.db != nil {
			return svc.AddLastPublishedInvoice(ctx, invoice)
		}
	}
	return nil
}

func (svc *Service) PublishPayload(ctx context.Context, payload interface{}, exchange, key string) error {
	payloadBytes := new(bytes.Buffer)
	err := json.NewEncoder(payloadBytes).Encode(payload)
	if err != nil {
		return err
	}
	return svc.rabbitChannel.PublishWithContext(
		ctx,
		//todo from config
		exchange, key, false, false, amqp.Publishing{
			ContentType: "application/json",
			Body:        payloadBytes.Bytes(),
		},
	)
}
