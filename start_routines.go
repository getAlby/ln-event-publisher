package main

import (
	"context"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
)

// starts the 4 routines:
//   - subscribe invoices
//   - handle invoice confirmations
//   - subscribe payments
//   - handle payment confirmations
func (svc *Service) StartRoutines(ctx context.Context) (wg *sync.WaitGroup) {
	wg = &sync.WaitGroup{}

	wg.Add(1)
	go func() {
		err := svc.startInvoiceSubscription(ctx)
		if err != nil && !strings.Contains(err.Error(), context.Canceled.Error()) {
			logrus.Fatal(err)
		}
		logrus.Info("invoice subscription loop done")
		wg.Done()
	}()
	wg.Add(1)
	go func() {
		//listen to confirm channel from invoices
		//and update apropriately in the database
		err := svc.StartInvoiceConfirmationLoop(ctx)
		if err != nil && !strings.Contains(err.Error(), context.Canceled.Error()) {
			logrus.Fatal(err)
		}
		logrus.Info("invoice confirmation loop done")
		wg.Done()
	}()
	wg.Add(1)
	go func() {
		err := svc.startPaymentSubscription(ctx)
		if err != nil && !strings.Contains(err.Error(), context.Canceled.Error()) {
			logrus.Fatal(err)
		}
		logrus.Info("payment subscription loop done")
		wg.Done()
	}()
	wg.Add(1)
	go func() {
		//listen to confirm channel from payments
		//and update apropriately in the database
		err := svc.StartPaymentConfirmationLoop(ctx)
		if err != nil && !strings.Contains(err.Error(), context.Canceled.Error()) {
			logrus.Fatal(err)
		}

		logrus.Info("payment confirmation loop done")
		wg.Done()
	}()
	return wg
}
