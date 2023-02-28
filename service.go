package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/lightningnetwork/lnd/lnrpc"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type Config struct {
	LNDAddress           string `envconfig:"LND_ADDRESS" required:"true"`
	LNDMacaroonHex       string `envconfig:"LND_MACAROON_HEX"`
	LNDCertHex           string `envconfig:"LND_CERT_HEX"`
	DatabaseUri          string `envconfig:"DATABASE_URI"`
	RabbitMQExchangeName string `envconfig:"RABBITMQ_EXCHANGE_NAME" default:"lnd_invoice"`
	RabbitMQUri          string `envconfig:"RABBITMQ_URI"`
}

const (
	LNDInvoiceExchange   = "lnd_invoice"
	LNDChannelExchange   = "lnd_channel"
	LNDPaymentExchange   = "lnd_payment"
	LNDInvoiceRoutingKey = "invoice.incoming.settled"
)

type Service struct {
	cfg       *Config
	lnd       LNDWrapper
	publisher *amqp.Channel
	db        *gorm.DB
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
	svc.publisher = ch
	return
}
func (svc *Service) lookupLastAddIndex(ctx context.Context) (result uint64, err error) {
	//get last item from db
	inv := &Invoice{}
	tx := svc.db.Last(inv)
	if tx.Error != nil && tx.Error != gorm.ErrRecordNotFound {
		return 0, tx.Error
	}
	//return addIndex
	return inv.AddIndex, nil
}

func (svc *Service) AddLastPublishedInvoice(ctx context.Context, invoice *lnrpc.Invoice) error {
	return svc.db.Create(&Invoice{
		AddIndex: invoice.AddIndex,
	}).Error
}

func (svc *Service) startInvoiceSubscription(ctx context.Context, addIndex uint64) error {
	invoiceSub, err := svc.lnd.SubscribeInvoices(ctx, &lnrpc.InvoiceSubscription{
		AddIndex: addIndex,
	})
	if err != nil {
		return err
	}
	logrus.Infof("Starting invoice subscription from index %d", addIndex)
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
	if invoice.State == lnrpc.Invoice_SETTLED {
		logrus.Infof("Publishing invoice with hash %s", hex.EncodeToString(invoice.RHash))
		err := svc.PublishPayload(ctx, invoice, svc.cfg.RabbitMQExchangeName, LNDInvoiceRoutingKey)
		if err != nil {
			return err
		}
		//save last published invoice
		return svc.AddLastPublishedInvoice(ctx, invoice)
	}
	return nil
}

func (svc *Service) PublishPayload(ctx context.Context, payload interface{}, exchange, key string) error {
	payloadBytes := new(bytes.Buffer)
	err := json.NewEncoder(payloadBytes).Encode(payload)
	if err != nil {
		return err
	}
	return svc.publisher.PublishWithContext(
		ctx,
		//todo from config
		exchange, key, false, false, amqp.Publishing{
			ContentType: "application/json",
			Body:        payloadBytes.Bytes(),
		},
	)
}
