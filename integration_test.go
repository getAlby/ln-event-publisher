package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/getAlby/ln-event-publisher/config"
	db2 "github.com/getAlby/ln-event-publisher/db"
	"github.com/getAlby/ln-event-publisher/service"
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

func createTestService(t *testing.T, cfg *config.Config, exchange, routingKey string) (svc *service.Service, mlnd *MockLND, msgs <-chan amqp091.Delivery) {

	svc = &service.Service{Cfg: cfg}
	mlnd = &MockLND{
		Sub: &MockSubscribeInvoices{invoiceChan: make(chan *lnrpc.Invoice)},
		PaymentSub: &MockSubscribePayments{
			paymentChan: make(chan *lnrpc.Payment, 10),
		},
		addIndexCounter: 0,
	}
	// - init Rabbit
	err := svc.InitRabbitMq()
	assert.NoError(t, err)

	//sub to the rabbit exchange ourselves to test e2e
	q, err := svc.RabbitChannel.QueueDeclare(
		"integration_test",
		true,
		false,
		false,
		false,
		nil,
	)
	assert.NoError(t, err)
	err = svc.RabbitChannel.QueueBind(q.Name, routingKey, exchange, false, nil)
	assert.NoError(t, err)

	// - init PG
	db, err := db2.OpenDB(cfg)
	assert.NoError(t, err)
	svc.Db = db
	svc.Lnd = mlnd

	//init rabbit channel
	//consume channel to check that invoice was published
	m, err := svc.RabbitChannel.Consume(
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
	cfg := &config.Config{
		DatabaseUri: os.Getenv("DATABASE_URI"),
		RabbitMQUri: os.Getenv("RABBITMQ_URI"),
	}
	svc, mlnd, m := createTestService(t, cfg, service.LNDInvoiceExchange, service.LNDInvoiceRoutingKey)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		svc.StartInvoiceSubscription(ctx)
	}()
	// - mock incoming invoice
	// the new invoice that will be saved will have addIndex + 1
	err := mlnd.mockPaidInvoice(100, "integration test")
	assert.NoError(t, err)
	//wait a bit for update to happen
	time.Sleep(100 * time.Millisecond)
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
	svc.RabbitChannel.Close()
	// - clean up database
	svc.Db.Exec("delete from invoices;")
}
func TestPaymentPublish(t *testing.T) {
	cfg := &config.Config{
		DatabaseUri:            os.Getenv("DATABASE_URI"),
		RabbitMQUri:            os.Getenv("RABBITMQ_URI"),
		RabbitMQTimeoutSeconds: 1,
	}
	svc, mlnd, m := createTestService(t, cfg, service.LNDPaymentExchange, "payment.outgoing.*")
	defer svc.Db.Exec("delete from payments;")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		err := svc.StartPaymentSubscription(ctx)
		assert.EqualError(t, err, context.Canceled.Error())
	}()
	// - mock outgoing payment
	index := uint64(1)
	//check that it gets published
	mlnd.mockPayment(lnrpc.Payment_SUCCEEDED, index, "hash1")
	timedOut, receivedPayment := timeoutOrNewPaymentFromRabbit(t, m)
	assert.False(t, timedOut)
	assert.Equal(t, lnrpc.Payment_SUCCEEDED, receivedPayment.Status)

	//mock the same payment again
	mlnd.mockPayment(lnrpc.Payment_SUCCEEDED, index, "hash2")
	timedOut, receivedPayment = timeoutOrNewPaymentFromRabbit(t, m)
	//should not get published
	assert.True(t, timedOut)
	// mock an in-flight payment
	index += 1
	mlnd.mockPayment(lnrpc.Payment_IN_FLIGHT, index, "hash3")
	timedOut, receivedPayment = timeoutOrNewPaymentFromRabbit(t, m)
	// should not get published
	assert.True(t, timedOut)

	//but now we get a failure for this in flight, it should get published
	mlnd.mockPayment(lnrpc.Payment_FAILED, index, "hash4")
	timedOut, receivedPayment = timeoutOrNewPaymentFromRabbit(t, m)
	// should get published
	assert.False(t, timedOut)
	assert.Equal(t, index, receivedPayment.PaymentIndex)
	assert.Equal(t, lnrpc.Payment_FAILED, receivedPayment.Status)
	// test restart:
	//   - add some inflights and a succes
	mlnd.mockPayment(lnrpc.Payment_IN_FLIGHT, uint64(100), "inflight_1")
	mlnd.mockPayment(lnrpc.Payment_SUCCEEDED, uint64(101), "success_1")
	mlnd.mockPayment(lnrpc.Payment_IN_FLIGHT, uint64(102), "inflight_2")
	timedOut, receivedPayment = timeoutOrNewPaymentFromRabbit(t, m)
	assert.False(t, timedOut)
	timedOut, receivedPayment = timeoutOrNewPaymentFromRabbit(t, m)
	//should time out now, queue empty
	assert.True(t, timedOut)
	//stop service
	//give it some time
	cancel()
	time.Sleep(10 * time.Millisecond)

	//   - inject payments (inflight-> success and new success/fail)
	mlnd.ListPaymentsResponse = []*lnrpc.Payment{
		{
			PaymentIndex: uint64(100),
			Status:       lnrpc.Payment_SUCCEEDED,
			PaymentHash:  "inflight_1",
		},
		{
			PaymentIndex: uint64(101),
			Status:       lnrpc.Payment_SUCCEEDED,
			PaymentHash:  "success_1",
		},
		{
			PaymentIndex: uint64(102),
			Status:       lnrpc.Payment_FAILED,
			PaymentHash:  "inflight_2",
		},
	}
	//   - start service again,
	ctx, cancel2 := context.WithCancel(context.Background())
	go func() {
		err := svc.StartPaymentSubscription(ctx)
		assert.EqualError(t, err, context.Canceled.Error())
	}()
	// test that all new updates are being published
	// but not the existing success
	timedOut, receivedPayment = timeoutOrNewPaymentFromRabbit(t, m)
	assert.False(t, timedOut)
	assert.Equal(t, "inflight_1", receivedPayment.PaymentHash)
	assert.Equal(t, lnrpc.Payment_SUCCEEDED, receivedPayment.Status)
	timedOut, receivedPayment = timeoutOrNewPaymentFromRabbit(t, m)
	assert.False(t, timedOut)
	assert.Equal(t, "inflight_2", receivedPayment.PaymentHash)
	assert.Equal(t, lnrpc.Payment_FAILED, receivedPayment.Status)
	//should time out now, queue empty
	timedOut, receivedPayment = timeoutOrNewPaymentFromRabbit(t, m)
	assert.True(t, timedOut)
	cancel2()
	svc.RabbitChannel.Close()
}

func timeoutOrNewPaymentFromRabbit(t *testing.T, m <-chan amqp091.Delivery) (timeout bool, payment *lnrpc.Payment) {
	select {
	case msg := <-m:
		var receivedPayment lnrpc.Payment
		r := bytes.NewReader(msg.Body)
		err := json.NewDecoder(r).Decode(&receivedPayment)
		assert.NoError(t, err)
		return false, &receivedPayment
	case <-time.After(500 * time.Millisecond):
		return true, nil
	}
}

type MockLND struct {
	Sub                  *MockSubscribeInvoices
	PaymentSub           *MockSubscribePayments
	addIndexCounter      uint64
	ListPaymentsResponse []*lnrpc.Payment
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

func (mlnd *MockLND) mockPayment(status lnrpc.Payment_PaymentStatus, index uint64, hash string) {
	//use time as hash
	mlnd.PaymentSub.paymentChan <- &lnrpc.Payment{
		PaymentHash:     hash,
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
		CreationTimeNs:  time.Now().UnixNano(),
		Htlcs:           []*lnrpc.HTLCAttempt{},
		PaymentIndex:    index,
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
		Htlcs: []*lnrpc.InvoiceHTLC{
			{
				ChanId:          0,
				HtlcIndex:       0,
				AmtMsat:         0,
				AcceptHeight:    0,
				AcceptTime:      0,
				ResolveTime:     0,
				ExpiryHeight:    0,
				State:           0,
				CustomRecords:   map[uint64][]byte{},
				MppTotalAmtMsat: 0,
				Amp:             &lnrpc.AMP{},
			},
		},
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
		Payments: mlnd.ListPaymentsResponse,
	}, nil
}
