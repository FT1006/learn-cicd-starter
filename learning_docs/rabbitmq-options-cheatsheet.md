# RabbitMQ go-rabbitmq Wrapper Options Cheatsheet

⚠️ **VERIFIED OPTIONS ONLY** - All options listed here are verified from the actual source code.

## Connection Options

### Basic Connection
```go
conn, err := rabbitmq.NewConn(
    "amqp://guest:guest@localhost:5672/",
    rabbitmq.WithConnectionOptionsLogging,
)
```

**Note**: Only `WithConnectionOptionsLogging` is confirmed to exist. Other connection options need verification.

## Publisher Options (VERIFIED)

### Basic Publisher (Direct Exchange)
```go
publisher, err := rabbitmq.NewPublisher(
    conn,
    rabbitmq.WithPublisherOptionsExchangeName("events"),
    rabbitmq.WithPublisherOptionsExchangeDeclare,
)
```

### Topic Publisher
```go
publisher, err := rabbitmq.NewPublisher(
    conn,
    rabbitmq.WithPublisherOptionsExchangeName("notifications"),
    rabbitmq.WithPublisherOptionsExchangeKind("topic"),
    rabbitmq.WithPublisherOptionsExchangeDeclare,
    rabbitmq.WithPublisherOptionsLogging,
)
```

### Reliable Publisher (Confirmations)
```go
publisher, err := rabbitmq.NewPublisher(
    conn,
    rabbitmq.WithPublisherOptionsExchangeName("critical-events"),
    rabbitmq.WithPublisherOptionsExchangeDeclare,
    rabbitmq.WithPublisherOptionsConfirm,
    rabbitmq.WithPublisherOptionsLogging,
)

// Handle confirmations
publisher.NotifyPublish(func(c rabbitmq.Confirmation) {
    log.Printf("Message confirmed: %v", c.Ack)
})
```

### All Publisher Creation Options
- `WithPublisherOptionsLogging` - Enable logging
- `WithPublisherOptionsLogger(log Logger)` - Custom logger
- `WithPublisherOptionsExchangeName(name string)` - Set exchange name
- `WithPublisherOptionsExchangeKind(kind string)` - Set exchange type
- `WithPublisherOptionsExchangeDurable` - Make exchange durable
- `WithPublisherOptionsExchangeAutoDelete` - Make exchange auto-delete
- `WithPublisherOptionsExchangeInternal` - Make exchange internal
- `WithPublisherOptionsExchangeNoWait` - Set exchange no-wait
- `WithPublisherOptionsExchangeDeclare` - Auto-declare exchange
- `WithPublisherOptionsExchangePassive` - Make exchange passive
- `WithPublisherOptionsExchangeArgs(args Table)` - Set exchange arguments
- `WithPublisherOptionsConfirm` - Enable confirm mode

## Consumer Options (VERIFIED)

### Basic Consumer (Transient Queue)
```go
consumer, err := rabbitmq.NewConsumer(
    conn,
    "temp-queue",
    rabbitmq.WithConsumerOptionsExchangeName("events"),
    rabbitmq.WithConsumerOptionsRoutingKey("user.created"),
    rabbitmq.WithConsumerOptionsExchangeDeclare,
    rabbitmq.WithConsumerOptionsQueueAutoDelete,
    rabbitmq.WithConsumerOptionsQueueExclusive,
)
```

### Durable Consumer (Persistent Queue)
```go
consumer, err := rabbitmq.NewConsumer(
    conn,
    "email-service",
    rabbitmq.WithConsumerOptionsExchangeName("notifications"),
    rabbitmq.WithConsumerOptionsRoutingKey("email.*"),
    rabbitmq.WithConsumerOptionsExchangeDeclare,
    rabbitmq.WithConsumerOptionsQueueDurable,
    rabbitmq.WithConsumerOptionsQOSPrefetch(10),
)
```

### High-Throughput Consumer
```go
consumer, err := rabbitmq.NewConsumer(
    conn,
    "analytics-queue",
    rabbitmq.WithConsumerOptionsExchangeName("events"),
    rabbitmq.WithConsumerOptionsRoutingKey("analytics.*"),
    rabbitmq.WithConsumerOptionsExchangeDeclare,
    rabbitmq.WithConsumerOptionsQueueDurable,
    rabbitmq.WithConsumerOptionsQOSPrefetch(100),
    rabbitmq.WithConsumerOptionsConcurrency(5),
)
```

### Dead Letter Queue Consumer
```go
consumer, err := rabbitmq.NewConsumer(
    conn,
    "orders-queue",
    rabbitmq.WithConsumerOptionsExchangeName("orders"),
    rabbitmq.WithConsumerOptionsRoutingKey("order.*"),
    rabbitmq.WithConsumerOptionsExchangeDeclare,
    rabbitmq.WithConsumerOptionsQueueDurable,
    rabbitmq.WithConsumerOptionsQueueArgs(rabbitmq.Table{
        "x-dead-letter-exchange": "failed-orders",
        "x-message-ttl":          300000, // 5 minutes
    }),
)
```

### All Consumer Creation Options
**Queue Options:**
- `WithConsumerOptionsQueueDurable` - Make queue durable
- `WithConsumerOptionsQueueAutoDelete` - Make queue auto-delete
- `WithConsumerOptionsQueueExclusive` - Make queue exclusive
- `WithConsumerOptionsQueueNoWait` - Set queue no-wait
- `WithConsumerOptionsQueuePassive` - Make queue passive
- `WithConsumerOptionsQueueNoDeclare` - Don't declare queue
- `WithConsumerOptionsQueueArgs(args Table)` - Set queue arguments
- `WithConsumerOptionsQueueQuorum` - Set queue as quorum type
- `WithConsumerOptionsQueueMessageExpiration(ttl time.Duration)` - Set message TTL

**Exchange Options:**
- `WithConsumerOptionsExchangeName(name string)` - Set exchange name
- `WithConsumerOptionsExchangeKind(kind string)` - Set exchange type
- `WithConsumerOptionsExchangeDurable` - Make exchange durable
- `WithConsumerOptionsExchangeAutoDelete` - Make exchange auto-delete
- `WithConsumerOptionsExchangeInternal` - Make exchange internal
- `WithConsumerOptionsExchangeNoWait` - Set exchange no-wait
- `WithConsumerOptionsExchangeDeclare` - Auto-declare exchange
- `WithConsumerOptionsExchangePassive` - Make exchange passive
- `WithConsumerOptionsExchangeArgs(args Table)` - Set exchange arguments

**Routing & Binding:**
- `WithConsumerOptionsRoutingKey(routingKey string)` - Bind to routing key
- `WithConsumerOptionsBinding(binding Binding)` - Custom binding options
- `WithConsumerOptionsExchangeOptions(exchangeOptions ExchangeOptions)` - Multiple exchanges

**Consumer Options:**
- `WithConsumerOptionsConcurrency(concurrency int)` - Set worker goroutines
- `WithConsumerOptionsConsumerName(consumerName string)` - Set consumer name
- `WithConsumerOptionsConsumerAutoAck(autoAck bool)` - Set auto-ack
- `WithConsumerOptionsConsumerExclusive` - Make consumer exclusive
- `WithConsumerOptionsConsumerNoWait` - Set consumer no-wait
- `WithConsumerOptionsLogging` - Enable logging
- `WithConsumerOptionsLogger(log logger.Logger)` - Custom logger
- `WithConsumerOptionsQOSPrefetch(prefetchCount int)` - Set prefetch count
- `WithConsumerOptionsQOSGlobal` - Set QOS global
- `WithConsumerOptionsForceShutdown` - Force shutdown without waiting

## Publish Options (VERIFIED)

### Basic JSON Message
```go
err = publisher.Publish(
    jsonData,
    []string{"user.created"},
    rabbitmq.WithPublishOptionsContentType("application/json"),
)
```

### Critical Message (Persistent + Mandatory)
```go
err = publisher.Publish(
    data,
    []string{"payment.processed"},
    rabbitmq.WithPublishOptionsContentType("application/json"),
    rabbitmq.WithPublishOptionsPersistentDelivery,
    rabbitmq.WithPublishOptionsMandatory,
)
```

### Message with TTL
```go
err = publisher.Publish(
    data,
    []string{"cache.invalidate"},
    rabbitmq.WithPublishOptionsContentType("application/json"),
    rabbitmq.WithPublishOptionsExpiration("60000"), // 1 minute
)
```

### RPC Message
```go
err = publisher.Publish(
    request,
    []string{"rpc.calculate"},
    rabbitmq.WithPublishOptionsContentType("application/json"),
    rabbitmq.WithPublishOptionsReplyTo("rpc.response.queue"),
    rabbitmq.WithPublishOptionsCorrelationID("req-123"),
)
```

### All Publish Runtime Options
- `WithPublishOptionsExchange(exchange string)` - Set exchange to publish to
- `WithPublishOptionsMandatory` - Make publishing mandatory
- `WithPublishOptionsImmediate` - Make publishing immediate
- `WithPublishOptionsContentType(contentType string)` - Set content type
- `WithPublishOptionsPersistentDelivery` - Make message persistent
- `WithPublishOptionsExpiration(expiration string)` - Set message TTL (in milliseconds)
- `WithPublishOptionsHeaders(headers Table)` - Set message headers
- `WithPublishOptionsContentEncoding(contentEncoding string)` - Set content encoding
- `WithPublishOptionsPriority(priority uint8)` - Set message priority (0-9)
- `WithPublishOptionsCorrelationID(correlationID string)` - Set correlation ID
- `WithPublishOptionsReplyTo(replyTo string)` - Set reply-to queue
- `WithPublishOptionsMessageID(messageID string)` - Set message ID
- `WithPublishOptionsTimestamp(timestamp time.Time)` - Set message timestamp
- `WithPublishOptionsType(messageType string)` - Set message type
- `WithPublishOptionsUserID(userID string)` - Set user ID
- `WithPublishOptionsAppID(appID string)` - Set application ID

## Quick Reference

| Use Case | Exchange Type | Queue Type | Key Options |
|----------|---------------|------------|-------------|
| **HTTP Events** | `direct` | `durable` | `WithPublishOptionsPersistentDelivery` |
| **Background Jobs** | `direct` | `durable` | `WithConsumerOptionsQOSPrefetch(1)` |
| **Notifications** | `topic` | `auto-delete` | `WithConsumerOptionsRoutingKey("*.important")` |
| **Logs** | `topic` | `durable` | `WithConsumerOptionsQOSPrefetch(100)` |
| **Cache Events** | `fanout` | `auto-delete` | `WithPublishOptionsExpiration("30000")` |
| **RPC** | `direct` | `exclusive` | `WithPublishOptionsReplyTo`, `WithPublishOptionsCorrelationID` |

## Performance Tips

- **High Throughput**: Use `WithConsumerOptionsConcurrency(N)` and `WithConsumerOptionsQOSPrefetch(100+)`
- **Reliability**: Use `WithPublishOptionsPersistentDelivery` and `WithConsumerOptionsQueueDurable`
- **Memory Usage**: Use `WithConsumerOptionsQueueAutoDelete` for temporary queues
- **Error Handling**: Set up dead letter exchanges with `WithConsumerOptionsQueueArgs`

## Action Types (Consumer Return Values)

When consuming messages, return one of these actions:
- `rabbitmq.Ack` - Acknowledge message (remove from queue)
- `rabbitmq.NackRequeue` - Reject message and requeue for retry
- `rabbitmq.NackDiscard` - Reject message and discard (send to DLQ if configured)

## Basic Usage Pattern

```go
// 1. Create connection
conn, err := rabbitmq.NewConn("amqp://localhost")

// 2. Create publisher
publisher, err := rabbitmq.NewPublisher(
    conn,
    rabbitmq.WithPublisherOptionsExchangeName("events"),
    rabbitmq.WithPublisherOptionsExchangeDeclare,
)

// 3. Create consumer
consumer, err := rabbitmq.NewConsumer(
    conn,
    "my-queue",
    rabbitmq.WithConsumerOptionsExchangeName("events"),
    rabbitmq.WithConsumerOptionsRoutingKey("my.key"),
    rabbitmq.WithConsumerOptionsExchangeDeclare,
)

// 4. Publish message
err = publisher.Publish(
    []byte("hello"),
    []string{"my.key"},
    rabbitmq.WithPublishOptionsContentType("text/plain"),
)

// 5. Consume messages
err = consumer.Run(func(d rabbitmq.Delivery) rabbitmq.Action {
    log.Printf("Received: %s", string(d.Body))
    return rabbitmq.Ack
})
```