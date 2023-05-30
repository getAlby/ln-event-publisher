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

var codeMappings = map[lnrpc.Payment_PaymentStatus]string{
	lnrpc.Payment_FAILED:    LNDPaymentErrorRoutingKey,
	lnrpc.Payment_SUCCEEDED: LNDPaymentSuccessRoutingKey,
}

type Config struct {
	LNDAddress              string `envconfig:"LND_ADDRESS" required:"true"`
	LNDMacaroonFile         string `envconfig:"LND_MACAROON_FILE"`
	LNDCertFile             string `envconfig:"LND_CERT_FILE"`
	DatabaseUri             string `envconfig:"DATABASE_URI"`
	DatabaseMaxConns        int    `envconfig:"DATABASE_MAX_CONNS" default:"10"`
	DatabaseMaxIdleConns    int    `envconfig:"DATABASE_MAX_IDLE_CONNS" default:"5"`
	DatabaseConnMaxLifetime int    `envconfig:"DATABASE_CONN_MAX_LIFETIME" default:"1800"` // 30 minutes
	RabbitMQUri             string `envconfig:"RABBITMQ_URI"`
	SentryDSN               string `envconfig:"SENTRY_DSN"`
}

const (
	LNDInvoiceExchange          = "lnd_invoice"
	LNDChannelExchange          = "lnd_channel"
	LNDPaymentExchange          = "lnd_payment"
	LNDInvoiceRoutingKey        = "invoice.incoming.settled"
	LNDPaymentSuccessRoutingKey = "payment.outgoing.settled"
	LNDPaymentErrorRoutingKey   = "payment.outgoing.error"
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
		LNDPaymentExchange,
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
	err = ch.ExchangeDeclare(
		//TODO: review exchange config
		LNDInvoiceExchange,
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
func (svc *Service) lookupLastAddIndices(ctx context.Context) (invoiceIndex, paymentIndex uint64, err error) {
	//get last item from db
	inv := &Invoice{}
	tx := svc.db.WithContext(ctx).Last(inv)
	if tx.Error != nil && tx.Error != gorm.ErrRecordNotFound {
		return 0, 0, tx.Error
	}
	//todo: payment add index
	return inv.AddIndex, 0, nil
}

func (svc *Service) AddLastPublishedInvoice(ctx context.Context, invoice *lnrpc.Invoice) error {
	return svc.db.WithContext(ctx).Create(&Invoice{
		AddIndex: invoice.AddIndex,
	}).Error
}

func (svc *Service) StorePayment(ctx context.Context, payment *lnrpc.Payment) (alreadyProcessed bool, err error) {
	toUpdate := &Payment{
		Model: gorm.Model{
			ID: uint(payment.PaymentIndex),
		},
	}
	err = svc.db.FirstOrCreate(&toUpdate).Error
	if err != nil {
		return false, err
	}
	//no need to update, we already processed this payment
	if toUpdate.Status == payment.Status {
		return true, nil
	}
	//we didn't know about the last status of this payment
	//so we didn't process it yet (it was stored in the db as in flight)
	toUpdate.Status = payment.Status
	return false, svc.db.WithContext(ctx).Save(toUpdate).Error
}

func (svc *Service) CheckPaymentsSinceLastIndex(ctx context.Context) {
	//look up index of earliest non-final payment in db
	firstInflight := &Payment{}
	err := svc.db.Where(&Payment{
		Status: lnrpc.Payment_IN_FLIGHT,
	}).First(firstInflight).Error
	if err != nil {
		logrus.Error(err)
		sentry.CaptureException(err)
		return
	}
	//don't query lnd if there is nothing to query
	//(first start)
	if firstInflight.ID == 0 {
		return
	}
	//make LND listpayments request starting from the first payment that we might have missed
	paymentResponse, err := svc.lnd.ListPayments(ctx, &lnrpc.ListPaymentsRequest{
		IndexOffset: uint64(firstInflight.ID - 1),
	})
	//check all payments
	//call process invoice on all of these
	//this call is idempotent: if we already had them in the database
	//in their current state, we won't republish them.
	for _, payment := range paymentResponse.Payments {
		err = svc.ProcessPayment(ctx, payment)
	}
}

func (svc *Service) startPaymentSubscription(ctx context.Context, addIndex uint64) error {
	paymentSub, err := svc.lnd.SubscribePayments(ctx, &routerrpc.TrackPaymentsRequest{})
	if err != nil {
		sentry.CaptureException(err)
		return err
	}
	//check LND for payments we might have missed while offline
	go svc.CheckPaymentsSinceLastIndex(ctx)
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
			fmt.Println("canceled")
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
	alreadyPublished, err := svc.StorePayment(ctx, payment)
	if err != nil {
		return err
	}
	routingKey, notInflight := codeMappings[payment.Status]
	//if the payment was in the database as final then we already published it
	//and we only publish completed payments
	if notInflight && !alreadyPublished {
		//only publish if non-pending
		logrus.Infof("Publishing payment with hash %s", payment.PaymentHash)
		err := svc.PublishPayload(ctx, payment, LNDPaymentExchange, routingKey)
		if err != nil {
			//todo: rollback storepayment db transaction
			return err
		}
	}
	return nil
}

func (svc *Service) ProcessInvoice(ctx context.Context, invoice *lnrpc.Invoice) error {
	if invoice.State == lnrpc.Invoice_SETTLED {
		logrus.Infof("Publishing invoice with hash %s", hex.EncodeToString(invoice.RHash))
		err := svc.PublishPayload(ctx, invoice, LNDInvoiceExchange, LNDInvoiceRoutingKey)
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
