package main

import (
	"testing"

	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/stretchr/testify/assert"
)

func TestCheckInvoice(t *testing.T) {
	//test non keysend
	assert.True(t, shouldPublishInvoice(&lnrpc.Invoice{
		State:     lnrpc.Invoice_SETTLED,
		IsKeysend: false,
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
				MppTotalAmtMsat: 0,
				Amp:             &lnrpc.AMP{},
			},
		},
	}))
	//test keysend with wallet id tlv
	assert.True(t, shouldPublishInvoice(&lnrpc.Invoice{
		State:     lnrpc.Invoice_SETTLED,
		IsKeysend: true,
		Htlcs: []*lnrpc.InvoiceHTLC{
			{
				ChanId:       0,
				HtlcIndex:    0,
				AmtMsat:      0,
				AcceptHeight: 0,
				AcceptTime:   0,
				ResolveTime:  0,
				ExpiryHeight: 0,
				State:        0,
				CustomRecords: map[uint64][]byte{
					696969: {69, 69, 69},
				},
				MppTotalAmtMsat: 0,
				Amp:             &lnrpc.AMP{},
			},
		},
	}))
	//test keysend without wallet id tlv
	assert.False(t, shouldPublishInvoice(&lnrpc.Invoice{
		State:     lnrpc.Invoice_SETTLED,
		IsKeysend: true,
		Htlcs: []*lnrpc.InvoiceHTLC{
			{
				ChanId:       0,
				HtlcIndex:    0,
				AmtMsat:      0,
				AcceptHeight: 0,
				AcceptTime:   0,
				ResolveTime:  0,
				ExpiryHeight: 0,
				State:        0,
				CustomRecords: map[uint64][]byte{
					696970: {69, 69, 70},
				},
				MppTotalAmtMsat: 0,
				Amp:             &lnrpc.AMP{},
			},
		},
	}))
}
