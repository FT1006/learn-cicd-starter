# Go-RabbitMQ Wrapper Usage Guide

## Overview
This wrapper provides a production-ready abstraction over the standard AMQP library with automatic reconnection, comprehensive error handling, and full RabbitMQ feature support.

## High-Level Patterns & Features

### 1. Connection Management Pattern
**Features:**
- Automatic reconnection with configurable intervals
- Cluster support via custom resolvers
- Thread-safe connection pooling
- Built-in monitoring capabilities

**Key Components:**
- `Conn`: Main connection manager
- `Resolver`: Interface for custom cluster resolution
- Automatic recovery from network failures

### 2. Publisher Pattern
**Features:**
- Flow control handling (automatic pausing when server requests)
- TCP blocking management
- Publisher confirms for guaranteed delivery
- Deferred confirmations for async processing
- Return handling for undeliverable messages

**Key Capabilities:**
- Publish to multiple routing keys atomically
- Full message property support
- Event notifications for returns and confirmations

### 3. Consumer Pattern
**Features:**
- Concurrent message processing with configurable workers
- Flexible acknowledgment strategies
- Graceful shutdown with handler completion
- QoS/prefetch management for load distribution

**Processing Actions:**
- `Ack`: Acknowledge and remove from queue
- `NackDiscard`: Reject and discard message
- `NackRequeue`: Reject and return to queue
- `Manual`: Handle acknowledgment manually

### 4. Error Handling Pattern
**Features:**
- Automatic reconnection at connection and channel levels
- Handler protection during reconnections
- Notification channels for monitoring events
- Graceful degradation under failure conditions

## Core Function Signatures

### Connection Management

```go
// Create a single-node connection
conn, err := rabbitmq.NewConn(
    "amqp://guest:guest@localhost:5672/",
    rabbitmq.WithConnectionOptionsLogging,
    rabbitmq.WithConnectionOptionsReconnectInterval(5*time.Second),
)

// Create a cluster connection
conn, err := rabbitmq.NewClusterConn(
    &rabbitmq.DefaultResolver{Addresses: []string{
        "amqp://node1:5672/",
        "amqp://node2:5672/",
    }},
    rabbitmq.WithConnectionOptionsLogging,
)

// Always close when done
defer conn.Close()
```

### Publisher Usage

```go
// Create a publisher
publisher, err := rabbitmq.NewPublisher(
    conn,
    rabbitmq.WithPublisherOptionsExchangeName("events"),
    rabbitmq.WithPublisherOptionsExchangeKind("topic"),
    rabbitmq.WithPublisherOptionsExchangeDeclare,
)
defer publisher.Close()

// Simple publish
err = publisher.Publish(
    []byte("Hello World"),
    []string{"user.created"},
    rabbitmq.WithPublishOptionsContentType("application/json"),
    rabbitmq.WithPublishOptionsPersistentDelivery,
)

// Publish with confirmation
confirmation, err := publisher.PublishWithDeferredConfirmWithContext(
    ctx,
    []byte("Important Message"),
    []string{"critical.event"},
    rabbitmq.WithPublishOptionsMandatory,
)

// Wait for confirmation
ack, err := confirmation.WaitForConfirm(ctx)

// Handle returns
publisher.NotifyReturn(func(r rabbitmq.Return) {
    log.Printf("Message returned: %s", string(r.Body))
})
```

### Consumer Usage

```go
// Create a consumer
consumer, err := rabbitmq.NewConsumer(
    conn,
    "my-queue",
    rabbitmq.WithConsumerOptionsConcurrency(10),
    rabbitmq.WithConsumerOptionsQueueDurable,
    rabbitmq.WithConsumerOptionsQOSPrefetch(5),
    rabbitmq.WithConsumerOptionsExchangeName("events"),
    rabbitmq.WithConsumerOptionsExchangeKind("topic"),
    rabbitmq.WithConsumerOptionsRoutingKey("user.*"),
)
defer consumer.Close()

// Run with handler
err = consumer.Run(func(d rabbitmq.Delivery) rabbitmq.Action {
    log.Printf("Received: %s", string(d.Body))
    
    // Process message
    if err := processMessage(d.Body); err != nil {
        return rabbitmq.NackRequeue
    }
    
    return rabbitmq.Ack
})
```

## Advanced Configuration Options

### Connection Options
```go
rabbitmq.WithConnectionOptionsConfig(amqp.Config{
    TLSClientConfig: tlsConfig,
    Heartbeat:       10 * time.Second,
    Locale:          "en_US",
})
```

### Publisher Options
```go
// Exchange configuration
rabbitmq.WithPublisherOptionsExchangeName("my-exchange")
rabbitmq.WithPublisherOptionsExchangeKind("direct")
rabbitmq.WithPublisherOptionsExchangeDurable
rabbitmq.WithPublisherOptionsExchangeAutoDelete
rabbitmq.WithPublisherOptionsExchangeArgs(amqp.Table{
    "x-message-ttl": 60000,
})

// Enable confirmations
rabbitmq.WithPublisherOptionsConfirm
```

### Consumer Options
```go
// Queue configuration
rabbitmq.WithConsumerOptionsQueueDurable
rabbitmq.WithConsumerOptionsQueueAutoDelete
rabbitmq.WithConsumerOptionsQueueExclusive
rabbitmq.WithConsumerOptionsQueueArgs(amqp.Table{
    "x-max-length": 1000,
    "x-overflow":   "reject-publish",
})

// Binding configuration
rabbitmq.WithConsumerOptionsBindingExchangeName("exchange")
rabbitmq.WithConsumerOptionsBindingExchangeKind("topic")
rabbitmq.WithConsumerOptionsRoutingKey("events.#")

// Consumer behavior
rabbitmq.WithConsumerOptionsConcurrency(20)
rabbitmq.WithConsumerOptionsQOSPrefetch(10)
rabbitmq.WithConsumerOptionsConsumerName("my-service")
rabbitmq.WithConsumerOptionsConsumerAutoAck(false)
rabbitmq.WithConsumerOptionsCloseGracefully(30*time.Second)
```

### Publish Options
```go
// Message properties
rabbitmq.WithPublishOptionsContentType("application/json")
rabbitmq.WithPublishOptionsContentEncoding("gzip")
rabbitmq.WithPublishOptionsDeliveryMode(2) // Persistent
rabbitmq.WithPublishOptionsPriority(5)
rabbitmq.WithPublishOptionsExpiration("60000") // 60 seconds
rabbitmq.WithPublishOptionsMessageID("unique-123")
rabbitmq.WithPublishOptionsTimestamp(time.Now())
rabbitmq.WithPublishOptionsType("user.event")
rabbitmq.WithPublishOptionsUserID("service-account")
rabbitmq.WithPublishOptionsAppID("my-service")
rabbitmq.WithPublishOptionsCorrelationID("req-123")
rabbitmq.WithPublishOptionsReplyTo("response-queue")
rabbitmq.WithPublishOptionsHeaders(amqp.Table{
    "trace-id": "abc123",
    "version":  "1.0",
})

// Routing options
rabbitmq.WithPublishOptionsMandatory
rabbitmq.WithPublishOptionsImmediate
rabbitmq.WithPublishOptionsExchange("custom-exchange")
```

## Best Practices

### 1. Connection Lifecycle
```go
// Single connection per application
var globalConn *rabbitmq.Conn

func initRabbitMQ() error {
    conn, err := rabbitmq.NewConn(amqpURL)
    if err != nil {
        return err
    }
    globalConn = conn
    return nil
}

// Share across publishers/consumers
publisher1, _ := rabbitmq.NewPublisher(globalConn)
publisher2, _ := rabbitmq.NewPublisher(globalConn)
consumer1, _ := rabbitmq.NewConsumer(globalConn, "queue1")
consumer2, _ := rabbitmq.NewConsumer(globalConn, "queue2")
```

### 2. Error Handling
```go
// Monitor connection events
go func() {
    for {
        select {
        case <-conn.NotifyClose():
            log.Println("Connection closed")
        case <-conn.NotifyReconnect():
            log.Println("Connection reconnected")
        }
    }
}()

// Handle publish failures
publisher.NotifyReturn(func(r rabbitmq.Return) {
    // Re-publish to dead letter queue
    deadLetterPublisher.Publish(r.Body, []string{"dlq"})
})
```

### 3. Performance Optimization
```go
// High-throughput consumer
consumer, _ := rabbitmq.NewConsumer(
    conn,
    "high-volume-queue",
    rabbitmq.WithConsumerOptionsConcurrency(50),
    rabbitmq.WithConsumerOptionsQOSPrefetch(20),
)

// Batch publishing with confirmations
var confirmations []rabbitmq.PublisherConfirmation
for _, msg := range messages {
    conf, _ := publisher.PublishWithDeferredConfirmWithContext(
        ctx, msg, []string{"bulk.data"},
    )
    confirmations = append(confirmations, conf)
}

// Wait for all confirmations
for _, conf := range confirmations {
    ack, err := conf.WaitForConfirm(ctx)
    if !ack || err != nil {
        // Handle failure
    }
}
```

### 4. Graceful Shutdown
```go
// Setup signal handling
sigChan := make(chan os.Signal, 1)
signal.Notify(sigChan, os.Interrupt)

// Graceful consumer with timeout
consumer, _ := rabbitmq.NewConsumer(
    conn,
    "my-queue",
    rabbitmq.WithConsumerOptionsCloseGracefully(30*time.Second),
)

go func() {
    <-sigChan
    log.Println("Shutting down...")
    consumer.Close() // Waits for handlers
    conn.Close()
    os.Exit(0)
}()
```

## Common Patterns

### RPC Pattern
```go
// RPC Client
rpcClient := func(request []byte) ([]byte, error) {
    correlationID := uuid.New().String()
    responseQueue := "rpc.responses." + correlationID
    
    // Create temporary response queue
    respConsumer, _ := rabbitmq.NewConsumer(
        conn,
        responseQueue,
        rabbitmq.WithConsumerOptionsQueueAutoDelete,
        rabbitmq.WithConsumerOptionsQueueExclusive,
    )
    
    // Publish request
    publisher.Publish(
        request,
        []string{"rpc.requests"},
        rabbitmq.WithPublishOptionsCorrelationID(correlationID),
        rabbitmq.WithPublishOptionsReplyTo(responseQueue),
    )
    
    // Wait for response
    responseChan := make(chan []byte)
    respConsumer.Run(func(d rabbitmq.Delivery) rabbitmq.Action {
        if d.CorrelationId == correlationID {
            responseChan <- d.Body
        }
        return rabbitmq.Ack
    })
    
    return <-responseChan, nil
}
```

### Work Queue Pattern
```go
// Distribute tasks among workers
taskPublisher, _ := rabbitmq.NewPublisher(
    conn,
    rabbitmq.WithPublisherOptionsExchangeName(""),
)

// Publish tasks to queue directly
for _, task := range tasks {
    taskPublisher.Publish(
        task,
        []string{"work-queue"},
        rabbitmq.WithPublishOptionsPersistentDelivery,
    )
}

// Workers consume with fair dispatch
for i := 0; i < numWorkers; i++ {
    worker, _ := rabbitmq.NewConsumer(
        conn,
        "work-queue",
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsQOSPrefetch(1), // Fair dispatch
    )
    
    go worker.Run(func(d rabbitmq.Delivery) rabbitmq.Action {
        // Process task
        return rabbitmq.Ack
    })
}
```

### Pub/Sub Pattern
```go
// Publisher for events
eventPublisher, _ := rabbitmq.NewPublisher(
    conn,
    rabbitmq.WithPublisherOptionsExchangeName("events"),
    rabbitmq.WithPublisherOptionsExchangeKind("fanout"),
)

// Multiple subscribers
for _, service := range services {
    subscriber, _ := rabbitmq.NewConsumer(
        conn,
        "", // Auto-generated queue name
        rabbitmq.WithConsumerOptionsBindingExchangeName("events"),
        rabbitmq.WithConsumerOptionsQueueAutoDelete,
        rabbitmq.WithConsumerOptionsQueueExclusive,
    )
    
    go subscriber.Run(handleEvent)
}
```

## Troubleshooting

### Connection Issues
- Check RabbitMQ server accessibility
- Verify credentials and vhost permissions
- Monitor reconnection events
- Enable connection logging

### Publishing Issues
- Enable publisher confirms for reliability
- Handle mandatory/immediate returns
- Check exchange existence and bindings
- Monitor flow control events

### Consumer Issues
- Adjust prefetch for performance
- Handle poison messages appropriately
- Monitor consumer cancelation
- Check queue permissions

### Performance Issues
- Increase consumer concurrency
- Adjust prefetch count
- Use deferred confirmations
- Consider message batching