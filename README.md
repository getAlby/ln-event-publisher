# LN Event publisher

The service acts as a bridge between a gRPC subscription and a RabbitMQ topic exchange. It will 
pump both incoming and outgoing payments from LND to RabbitMQ. It keeps track of what payments have
been published to ensure it does not miss updates between restarts: at startup time, it will look in its database for what payments 
might have been possibly missed while it was offline (both incoming and outgoing), and it will query LND for updates on these payments as well and publish them if needed.

General configuration env vars (always needed):
- "LND_ADDRESS": `your-lnd-host:10009`
- "DATABASE_URI": PG database connection string
- "LND_MACAROON_FILE": LND macaroon file
- "LND_CERT_FILE": LND certificate file
- "RABBITMQ_URI": `amqp://user:password@host/vhost`

- Publishes incoming payments (= "invoices") to the `lnd_invoice` exchange
- Publishes outgoing payments to the`lnd_payment` exchange

Possible missed-while-offline incoming invoices are handled by looking up the last invoice in the db and specifying the "AddIndex" when subscribing to invoices over grpc.
Possible missed-while-offline outgoing payments are handled by looking up the earliest incomplete (or last complete if none incomplete) payment in the database and making a "ListPayments" call to LND with a starting timestamp equal to the timestamp of this payment.
# LND incoming invoices
- Payload [lnrpc.Invoice](https://github.com/lightningnetwork/lnd/blob/master/lnrpc/lightning.pb.go#L11597) struct.
- Routing key: `invoice.incoming.settled`
# LND outgoing payments
- Payload [lnrpc.Payment](https://github.com/lightningnetwork/lnd/blob/master/lnrpc/lightning.pb.go#L12612)
- Routing keys `payment.outgoing.settled`, `payment.outgoing.error`

# Republish Invoices

If you need to republish settled invoices to update state in lndhub, you can use the cmd/republish-invoices by providing all payment hashes separated by commas:
- "REPUBLISH_INVOICE_HASHES" : `<hash_1>,<hash_2>....<hash_n>`

Use this in a job by setting:
```
command:
- /bin/republish-invoices
```
