package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/getAlby/lndhub.go/lnd"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/lnrpc/routerrpc"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
)

func TestLNEventPublisher(t *testing.T) {
	mlnd := &MockLND{
		Sub: &MockSubscribeInvoices{
			invoiceChan: make(chan *lnrpc.Invoice),
		},
		addIndexCounter: 0,
	}
	cfg := &Config{
		DatabaseUri:          os.Getenv("DATABASE_URI"),
		RabbitMQExchangeName: "lnd_invoice",
		RabbitMQUri:          os.Getenv("RABBITMQ_URI"),
	}
	// - init Rabbit
	svc := &Service{cfg: cfg}
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
	err = svc.rabbitChannel.QueueBind(q.Name, LNDInvoiceRoutingKey, svc.cfg.RabbitMQExchangeName, false, nil)
	assert.NoError(t, err)

	// - init PG
	db, err := OpenDB(cfg)
	assert.NoError(t, err)
	svc.db = db
	svc.lnd = mlnd
	addIndex, err := svc.lookupLastAddIndex(context.Background())
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
	newAddIndex, err := svc.lookupLastAddIndex(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, addIndex+1, newAddIndex)

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
	// - Add PG / Rabbit in CI to run tests on GH actions
}

type MockLND struct {
	Sub             *MockSubscribeInvoices
	addIndexCounter uint64
}
type MockSubscribeInvoices struct {
	invoiceChan chan (*lnrpc.Invoice)
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
	panic("not implemented") // TODO: Implement
}

func (mlnd *MockLND) DecodeBolt11(ctx context.Context, bolt11 string, options ...grpc.CallOption) (*lnrpc.PayReq, error) {
	panic("not implemented") // TODO: Implement
}
