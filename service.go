package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"

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
	RabbitMQUri             string `envconfig:"RABBITMQ_URI" required:"true"`
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
	//get last invoice from db
	inv := &Invoice{}
	tx := svc.db.WithContext(ctx).Last(inv)
	if tx.Error != nil && tx.Error != gorm.ErrRecordNotFound {
		return 0, 0, tx.Error
	}
	//get earliest non-final payment in db
	//or the last completed payment
	firstInflightOrLastCompleted := &Payment{}
	err = svc.db.Where(&Payment{
		Status: lnrpc.Payment_IN_FLIGHT,
	}).First(firstInflightOrLastCompleted).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			//look up last completed payment that we have instead
			//and use that one.
			err = svc.db.WithContext(ctx).Last(firstInflightOrLastCompleted).Error
			if err != nil {
				if err == gorm.ErrRecordNotFound {
					//if we get here there are no payment in the db:
					//first start, nothing found
					return inv.AddIndex, 0, nil
				}
				//real db error
				return 0, 0, err
			}
			// in this case we don't need to do -1
			//because we have already processsed this invoice
			return inv.AddIndex, uint64(firstInflightOrLastCompleted.ID), nil
		}
		logrus.Error(err)
		sentry.CaptureException(err)
		return
	}
	// in this case we need to subtract 1 from the payment index
	// because we do want an update about the in-flight payment
	// that we found
	return inv.AddIndex, uint64(firstInflightOrLastCompleted.ID - 1), nil
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
		PaymentHash: payment.PaymentHash,
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
	//so we didn't process it yet
	toUpdate.Status = payment.Status
	return false, svc.db.WithContext(ctx).Save(toUpdate).Error
}

func (svc *Service) CheckPaymentsSinceLastIndex(ctx context.Context, index uint64) error {

	if index == 0 {
		//no need to check anything
		return nil
	}
	logrus.Infof("Checking payments since last index: %d", index)
	//make LND listpayments request starting from the first payment that we might have missed
	paymentResponse, err := svc.lnd.ListPayments(ctx, &lnrpc.ListPaymentsRequest{
		IndexOffset: index,
	})
	if err != nil {
		return err
	}

	logrus.Infof("Found %d payments since last index", len(paymentResponse.Payments))
	//call process invoice on all of these
	//this call is idempotent: if we already had them in the database
	//in their current state, we won't republish them.
	for _, payment := range paymentResponse.Payments {
		err = svc.ProcessPayment(ctx, payment)
		if err != nil {
			return err
		}
	}
	logrus.Info("Processed all payments since last index")
	return nil
}

func (svc *Service) startPaymentSubscription(ctx context.Context, addIndex uint64) error {
	paymentSub, err := svc.lnd.SubscribePayments(ctx, &routerrpc.TrackPaymentsRequest{})
	if err != nil {
		sentry.CaptureException(err)
		return err
	}
	//check LND for payments we might have missed while offline
	//do this in a goroutine so we don't miss any new payments
	//(though it's possible that we publish duplicates)
	go func() {
		err = svc.CheckPaymentsSinceLastIndex(ctx, addIndex)
		if err != nil {
			logrus.Error(err)
			sentry.CaptureException(err)
		}
	}()
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
	alreadyPublished, err := svc.StorePayment(ctx, payment)
	if err != nil {
		return err
	}
	routingKey, notInflight := codeMappings[payment.Status]
	//if the payment was in the database as final then we already published it
	//and we only publish completed payments
	if notInflight && !alreadyPublished {
		logrus.Infof("Publishing payment status %v hash %s", payment.Status, payment.PaymentHash)
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
