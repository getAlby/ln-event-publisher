package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/stretchr/testify/assert"
)

func TestLNEventPublisher(t *testing.T) {
	// todo
	// - mock LND
	mlnd := &MockLND{
		Sub: &MockSubscribeInvoices{
			invoiceChan: make(chan *lnrpc.Invoice),
		},
		addIndexCounter: 0,
	}
	// - init PG
	cfg := &Config{
		DatabaseUri:          os.Getenv("DATABASE_URI"),
		RabbitMQExchangeName: "lnd_invoice",
		RabbitMQUri:          os.Getenv("RABBITMQ_URI"),
	}
	// - init Rabbit
	svc := &Service{cfg: cfg}
	err := svc.InitRabbitMq()
	assert.NoError(t, err)
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
	err = mlnd.mockPaidInvoice(100)
	assert.NoError(t, err)
	//wait a bit for update to happen
	time.Sleep(100 * time.Millisecond)
	// - check if add index is saved correctly
	newAddIndex, err := svc.lookupLastAddIndex(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, addIndex+1, newAddIndex)
	//stop service
	cancel()
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
func (mlnd *MockLND) GetInfo(ctx context.Context, req *lnrpc.GetInfoRequest) (*lnrpc.GetInfoResponse, error) {
	return &lnrpc.GetInfoResponse{
		Alias:          "MOCK LND",
		IdentityPubkey: "MOCK LND",
	}, nil
}

func (mlnd *MockLND) SubscribeInvoices(ctx context.Context, req *lnrpc.InvoiceSubscription) (LNDChannelSubscriptionWrapper, error) {
	mlnd.addIndexCounter = req.AddIndex
	return mlnd.Sub, nil
}

func (mlnd *MockLND) mockPaidInvoice(amtPaid int64) error {
	mlnd.addIndexCounter += 1
	incoming := &lnrpc.Invoice{
		AddIndex:       mlnd.addIndexCounter,
		Memo:           "",
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
