# LN Event publisher

This service subscribes to certain LND gRPC subscriptions and publishes those events on a RabbitMQ topic exchange.

General configuration env vars (always needed):
- "LND_ADDRESS": `your-lnd-host:10009`
- "LND_MACAROON_HEX": LND macaroon in hex format
- "LND_CERT_HEX": LND certificate in hex format
- "RABBITMQ_URI": `amqp://user:password@host/vhost`

The service will do different things based on the environment variable `RABBITMQ_EXCHANGE_NAME`, which is also the exchange where the service will be publishing.

- `lnd_invoice`: publish lnd incoming invoices 
- `lnd_payment`: publish lnd outgoing payments
- `lnd_channel`: publish lnd channel events
# LND incoming invoices
- Payload [lnrpc.Invoice](https://github.com/lightningnetwork/lnd/blob/master/lnrpc/lightning.pb.go#L11597) struct.
- Routing key: `invoice.incoming.settled`
- `DATABASE_URI`: if specified, initialize and migrate a PG database table to store the add index of all published invoices. On startup, we can fetch the add_index of the last published invoice and ask LND for all invoices since that one, to ensure we don't miss any.
# LND outgoing payments
Not supported yet (waiting for new LND release)
# LND channel events
WIP, need to look into payload formats
