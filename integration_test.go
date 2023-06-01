package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/getAlby/ln-event-publisher/lnd"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/lnrpc/routerrpc"
	"github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
)

func createTestService(t *testing.T, cfg *Config, exchange, routingKey string) (svc *Service, mlnd *MockLND, msgs <-chan amqp091.Delivery) {

	svc = &Service{cfg: cfg}
	mlnd = &MockLND{
		Sub: &MockSubscribeInvoices{invoiceChan: make(chan *lnrpc.Invoice)},
		PaymentSub: &MockSubscribePayments{
			paymentChan: make(chan *lnrpc.Payment),
		},
		addIndexCounter:     0,
		paymentIndexCounter: 0,
	}
	// - init Rabbit
	err := svc.InitRabbitMq()
	assert.NoError(t, err)

	//sub to the rabbit exchange ourselves to test e2e
	q, err := svc.rabbitChannel.QueueDeclare(
		"integration_test",
		true,
		false,
		false,
		false,
		nil,
	)
	assert.NoError(t, err)
	err = svc.rabbitChannel.QueueBind(q.Name, routingKey, exchange, false, nil)
	assert.NoError(t, err)

	// - init PG
	db, err := OpenDB(cfg)
	assert.NoError(t, err)
	svc.db = db
	svc.lnd = mlnd

	//init rabbit channel
	//consume channel to check that invoice was published
	m, err := svc.rabbitChannel.Consume(
		q.Name,
		"",
		true,
		false,
		false,
		false,
		nil,
	)
	assert.NoError(t, err)
	return svc, mlnd, m
}
func TestInvoicePublish(t *testing.T) {
	cfg := &Config{
		DatabaseUri: os.Getenv("DATABASE_URI"),
		RabbitMQUri: os.Getenv("RABBITMQ_URI"),
	}
	svc, mlnd, m := createTestService(t, cfg, LNDInvoiceExchange, LNDInvoiceRoutingKey)
	addIndex, _, err := svc.lookupLastAddIndices(context.Background())
	assert.NoError(t, err)
	//the first time, add index should be 0
	assert.Equal(t, uint64(0), addIndex)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		err = svc.startInvoiceSubscription(ctx, addIndex)
		assert.EqualError(t, err, context.Canceled.Error())
	}()
	// - mock incoming invoice
	// the new invoice that will be saved will have addIndex + 1
	err = mlnd.mockPaidInvoice(100, "integration test")
	assert.NoError(t, err)
	//wait a bit for update to happen
	time.Sleep(100 * time.Millisecond)
	// - check if add index is saved correctly
	newAddIndex, _, err := svc.lookupLastAddIndices(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, addIndex+1, newAddIndex)

	assert.NoError(t, err)
	msg := <-m
	var receivedInvoice lnrpc.Invoice
	r := bytes.NewReader(msg.Body)
	err = json.NewDecoder(r).Decode(&receivedInvoice)
	assert.NoError(t, err)
	assert.Equal(t, "integration test", receivedInvoice.Memo)
	assert.Equal(t, int64(100), receivedInvoice.Value)

	//stop service
	cancel()
	svc.rabbitChannel.Close()
	// - clean up database
	svc.db.Exec("delete from invoices;")
}
func TestPaymentPublish(t *testing.T) {
	cfg := &Config{
		DatabaseUri: os.Getenv("DATABASE_URI"),
		RabbitMQUri: os.Getenv("RABBITMQ_URI"),
	}
	svc, mlnd, m := createTestService(t, cfg, LNDPaymentExchange, LNDPaymentSuccessRoutingKey)
	_, paymentIndex, err := svc.lookupLastAddIndices(context.Background())
	assert.NoError(t, err)
	//the first time, add index should be 0
	assert.Equal(t, uint64(0), paymentIndex)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		err = svc.startPaymentSubscription(ctx, paymentIndex)
		assert.EqualError(t, err, context.Canceled.Error())
	}()
	// - mock outgoing payment
	// the new payment that will be saved will have addIndex + 1
	mlnd.mockPayment(lnrpc.Payment_SUCCEEDED)
	assert.NoError(t, err)
	//wait a bit for update to happen
	time.Sleep(100 * time.Millisecond)

	msg := <-m
	var receivedPayment lnrpc.Payment
	r := bytes.NewReader(msg.Body)
	err = json.NewDecoder(r).Decode(&receivedPayment)
	assert.NoError(t, err)
	assert.Equal(t, lnrpc.Payment_SUCCEEDED, receivedPayment.Status)
	//stop service
	cancel()
	svc.rabbitChannel.Close()
	// - clean up database
	svc.db.Exec("delete from payments;")
}

type MockLND struct {
	Sub                 *MockSubscribeInvoices
	PaymentSub          *MockSubscribePayments
	addIndexCounter     uint64
	paymentIndexCounter uint64
}
type MockSubscribeInvoices struct {
	invoiceChan chan (*lnrpc.Invoice)
}
type MockSubscribePayments struct {
	paymentChan chan (*lnrpc.Payment)
}

func (mockSub *MockSubscribePayments) Recv() (*lnrpc.Payment, error) {
	payment := <-mockSub.paymentChan
	return payment, nil
}

func (mockSub *MockSubscribeInvoices) Recv() (*lnrpc.Invoice, error) {
	inv := <-mockSub.invoiceChan
	return inv, nil
}
func (mlnd *MockLND) GetInfo(ctx context.Context, req *lnrpc.GetInfoRequest, options ...grpc.CallOption) (*lnrpc.GetInfoResponse, error) {
	return &lnrpc.GetInfoResponse{
		Alias:          "MOCK LND",
		IdentityPubkey: "MOCK LND",
	}, nil
}

func (mlnd *MockLND) SubscribeInvoices(ctx context.Context, req *lnrpc.InvoiceSubscription, options ...grpc.CallOption) (lnd.SubscribeInvoicesWrapper, error) {
	mlnd.addIndexCounter = req.AddIndex
	return mlnd.Sub, nil
}

func (mlnd *MockLND) mockPayment(status lnrpc.Payment_PaymentStatus) {
	mlnd.paymentIndexCounter += 1
	mlnd.PaymentSub.paymentChan <- &lnrpc.Payment{
		PaymentHash:     "",
		Value:           0,
		CreationDate:    0,
		Fee:             0,
		PaymentPreimage: "",
		ValueSat:        0,
		ValueMsat:       0,
		PaymentRequest:  "",
		Status:          status,
		FeeSat:          0,
		FeeMsat:         0,
		CreationTimeNs:  0,
		Htlcs:           []*lnrpc.HTLCAttempt{},
		PaymentIndex:    mlnd.paymentIndexCounter,
		FailureReason:   0,
	}
}

func (mlnd *MockLND) mockPaidInvoice(amtPaid int64, memo string) error {
	mlnd.addIndexCounter += 1
	incoming := &lnrpc.Invoice{
		AddIndex:       mlnd.addIndexCounter,
		Memo:           memo,
		Value:          amtPaid,
		ValueMsat:      1000 * amtPaid,
		Settled:        true,
		CreationDate:   time.Now().Unix(),
		SettleDate:     time.Now().Unix(),
		PaymentRequest: "",
		AmtPaid:        amtPaid,
		AmtPaidSat:     amtPaid,
		AmtPaidMsat:    1000 * amtPaid,
		State:          lnrpc.Invoice_SETTLED,
	}
	mlnd.Sub.invoiceChan <- incoming
	return nil
}
func (mlnd *MockLND) ListChannels(ctx context.Context, req *lnrpc.ListChannelsRequest, options ...grpc.CallOption) (*lnrpc.ListChannelsResponse, error) {
	panic("not implemented") // TODO: Implement
}

func (mlnd *MockLND) SendPaymentSync(ctx context.Context, req *lnrpc.SendRequest, options ...grpc.CallOption) (*lnrpc.SendResponse, error) {
	panic("not implemented") // TODO: Implement
}

func (mlnd *MockLND) AddInvoice(ctx context.Context, req *lnrpc.Invoice, options ...grpc.CallOption) (*lnrpc.AddInvoiceResponse, error) {
	panic("not implemented") // TODO: Implement
}

func (mlnd *MockLND) SubscribePayment(ctx context.Context, req *routerrpc.TrackPaymentRequest, options ...grpc.CallOption) (lnd.SubscribePaymentWrapper, error) {
	return mlnd.PaymentSub, nil
}

func (mlnd *MockLND) SubscribePayments(ctx context.Context, req *routerrpc.TrackPaymentsRequest, options ...grpc.CallOption) (lnd.SubscribePaymentWrapper, error) {
	return mlnd.PaymentSub, nil
}

func (mlnd *MockLND) DecodeBolt11(ctx context.Context, bolt11 string, options ...grpc.CallOption) (*lnrpc.PayReq, error) {
	panic("not implemented") // TODO: Implement
}

func (mlnd *MockLND) ListPayments(ctx context.Context, req *lnrpc.ListPaymentsRequest, options ...grpc.CallOption) (*lnrpc.ListPaymentsResponse, error) {
	return &lnrpc.ListPaymentsResponse{
		Payments: []*lnrpc.Payment{},
	}, nil
}
