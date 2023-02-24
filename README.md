# LN Event publisher

This service subscribes to certain LND gRPC subscriptions and publishes those events on a RabbitMQ topic exchange.

General configuration env vars (always needed):
- "LND_ADDRESS"
- "LND_MACAROON_HEX"
- "LND_CERT_HEX"
- "RABBITMQ_URI"

The service will do different things based on the environment variable `RABBITMQ_EXCHANGE_NAME`, which is also the exchange where the service will be publishing.

- `lnd_invoice`: publish lnd incoming invoices 
- `lnd_payment`: publish lnd outgoing payments
- `lnd_channel`: publish lnd channel events
# LND incoming invoices
- Payload [lnrpc.Invoice](https://github.com/lightningnetwork/lnd/blob/master/lnrpc/lightning.pb.go#L11597) struct.
- Routing key: `invoice.incoming.settled`
- `INVOICE_ADD_INDEX`: if specified, ask LND about historical invoices starting _after_ this add_index. **WARNING**: asking LND for too many invoices at once will cause it to use a lot of memory. 
# LND outgoing payments
Not supported yet (waiting for new LND release)
# LND channel events
WIP, need to look into payload formats
