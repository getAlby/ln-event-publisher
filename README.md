# LN Event publisher

The service acts as a bridge between a gRPC subscription and a RabbitMQ topic exchange. It will 
pump both incoming and outgoing payments from LND to RabbitMQ. It keeps track of what payments have
been published to ensure it does not miss updates between restarts: at startup time, it will look in its database for what payments 
might have been possibly missed while it was offline (both incoming and outgoing), and it will query LND for updates on these payments as well and publish them if needed.

General configuration env vars (always needed):
- "LND_ADDRESS": `your-lnd-host:10009`
- "DATABASE_URI": PG database connection string
- "LND_MACAROON_HEX": LND macaroon in hex format
- "LND_CERT_HEX": LND certificate in hex format
- "RABBITMQ_URI": `amqp://user:password@host/vhost`
- Publishes incoming payments (= "invoices") to the `lnd_invoice` exchange
- Publishes outgoing payments to the`lnd_payment` exchange
# LND incoming invoices
- Payload [lnrpc.Invoice](https://github.com/lightningnetwork/lnd/blob/master/lnrpc/lightning.pb.go#L11597) struct.
- Routing key: `invoice.incoming.settled`
# LND outgoing payments
- Payload lnrpc.Payment
- Routing keys `payment.outgoing.settled`, `payment.outgoing.error`