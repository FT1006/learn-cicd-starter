# Production-Ready RabbitMQ Configurations

This guide provides production-ready configurations for the go-rabbitmq library, focusing on durability, persistence, performance tuning, and high availability patterns.

## Table of Contents

1. [Production Requirements Overview](#production-requirements-overview)
2. [Durability Configurations](#durability-configurations)
3. [Performance Tuning](#performance-tuning)
4. [High Availability Patterns](#high-availability-patterns)
5. [Monitoring and Observability](#monitoring-and-observability)
6. [Complete Production Examples](#complete-production-examples)

## Production Requirements Overview

Production RabbitMQ deployments require careful consideration of:

- **Durability**: Messages and queues survive broker restarts
- **Persistence**: Messages are written to disk for crash recovery
- **Performance**: Optimal throughput and resource utilization
- **High Availability**: Fault tolerance and cluster resilience
- **Monitoring**: Observability and alerting capabilities

### Key Production Principles

1. **Message Durability**: Enable durable queues and exchanges
2. **Message Persistence**: Use persistent delivery mode for critical messages
3. **Acknowledgments**: Disable auto-ack for guaranteed processing
4. **Prefetch Optimization**: Balance throughput and memory usage
5. **Publisher Confirms**: Ensure message delivery guarantees
6. **Quorum Queues**: Use for high availability requirements

## Durability Configurations

### Queue Durability

Durable queues survive broker restarts and are essential for production:

```go
package main

import (
    "log"
    "time"
    
    "github.com/wagslane/go-rabbitmq"
)

func createDurableConsumer() {
    conn, err := rabbitmq.NewConn("amqp://localhost")
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()

    consumer, err := rabbitmq.NewConsumer(
        conn,
        "production.orders",
        rabbitmq.WithConsumerOptionsQueueDurable,           // Queue survives restarts
        rabbitmq.WithConsumerOptionsConsumerAutoAck(false), // Manual acknowledgments
        rabbitmq.WithConsumerOptionsQOSPrefetch(10),        // Prefetch optimization
    )
    if err != nil {
        log.Fatal(err)
    }
    defer consumer.Close()

    // Consumer handler with proper acknowledgment
    consumer.Consume(func(d rabbitmq.Delivery) (action rabbitmq.Action) {
        // Process message
        log.Printf("Processing order: %s", string(d.Body))
        
        // Simulate processing time
        time.Sleep(100 * time.Millisecond)
        
        // Acknowledge successful processing
        return rabbitmq.Ack
    })
}
```

### Exchange Durability

Durable exchanges persist across broker restarts:

```go
func createDurablePublisher() {
    conn, err := rabbitmq.NewConn("amqp://localhost")
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()

    publisher, err := rabbitmq.NewPublisher(
        conn,
        rabbitmq.WithPublisherOptionsExchangeName("production.orders"),
        rabbitmq.WithPublisherOptionsExchangeDurable,   // Exchange survives restarts
        rabbitmq.WithPublisherOptionsExchangeDeclare,   // Create if doesn't exist
        rabbitmq.WithPublisherOptionsConfirm,           // Enable publisher confirms
    )
    if err != nil {
        log.Fatal(err)
    }
    defer publisher.Close()

    // Publish with persistence
    err = publisher.Publish(
        []byte(`{"order_id": "12345", "amount": 99.99}`),
        []string{"order.created"},
        rabbitmq.WithPublishOptionsPersistentDelivery, // Messages survive restarts
        rabbitmq.WithPublishOptionsContentType("application/json"),
    )
    if err != nil {
        log.Fatal(err)
    }
}
```

### Message Persistence

Configure persistent delivery for critical messages:

```go
func publishPersistentMessage(publisher *rabbitmq.Publisher) error {
    orderData := `{
        "order_id": "ORD-2024-001",
        "customer_id": "CUST-456",
        "amount": 299.99,
        "timestamp": "2024-01-15T10:30:00Z"
    }`

    return publisher.Publish(
        []byte(orderData),
        []string{"order.created"},
        rabbitmq.WithPublishOptionsPersistentDelivery,     // Message survives restarts
        rabbitmq.WithPublishOptionsContentType("application/json"),
        rabbitmq.WithPublishOptionsMandatory,              // Fail if no queue bound
        rabbitmq.WithPublishOptionsExpiration("3600000"),  // 1 hour TTL
    )
}
```

## Performance Tuning

### Prefetch Optimization

Balance throughput and memory usage with proper prefetch settings:

```go
func createOptimizedConsumer() {
    conn, err := rabbitmq.NewConn("amqp://localhost")
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()

    consumer, err := rabbitmq.NewConsumer(
        conn,
        "high-throughput.queue",
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsQOSPrefetch(50),        // Higher prefetch for throughput
        rabbitmq.WithConsumerOptionsConcurrency(10),        // Multiple goroutines
        rabbitmq.WithConsumerOptionsConsumerAutoAck(false), // Manual ack for reliability
    )
    if err != nil {
        log.Fatal(err)
    }
    defer consumer.Close()

    // Fast message processing
    consumer.Consume(func(d rabbitmq.Delivery) (action rabbitmq.Action) {
        // Process message quickly
        processMessage(d.Body)
        return rabbitmq.Ack
    })
}

// Different prefetch settings for different workloads
func createLowLatencyConsumer() {
    // Low latency, low prefetch
    consumer, err := rabbitmq.NewConsumer(
        conn,
        "low-latency.queue",
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsQOSPrefetch(1),         // Low prefetch for immediate processing
        rabbitmq.WithConsumerOptionsConcurrency(1),         // Single threaded for order
        rabbitmq.WithConsumerOptionsConsumerAutoAck(false),
    )
    // ... rest of implementation
}
```

### Concurrency Settings

Optimize concurrency based on workload characteristics:

```go
func createConcurrentConsumer() {
    conn, err := rabbitmq.NewConn("amqp://localhost")
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()

    // High concurrency for I/O bound tasks
    consumer, err := rabbitmq.NewConsumer(
        conn,
        "io-bound.queue",
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsQOSPrefetch(100),       // High prefetch
        rabbitmq.WithConsumerOptionsConcurrency(50),        // Many goroutines for I/O
        rabbitmq.WithConsumerOptionsConsumerAutoAck(false),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer consumer.Close()

    consumer.Consume(func(d rabbitmq.Delivery) (action rabbitmq.Action) {
        // I/O bound processing (API calls, database operations)
        err := processWithExternalAPI(d.Body)
        if err != nil {
            log.Printf("Processing failed: %v", err)
            return rabbitmq.Retry
        }
        return rabbitmq.Ack
    })
}
```

### Connection Pooling

While this library manages connections internally, you can create multiple publishers/consumers for scaling:

```go
type MessageProcessor struct {
    publishers []*rabbitmq.Publisher
    consumers  []*rabbitmq.Consumer
    conn       *rabbitmq.Conn
}

func NewMessageProcessor(amqpURL string, poolSize int) (*MessageProcessor, error) {
    conn, err := rabbitmq.NewConn(amqpURL)
    if err != nil {
        return nil, err
    }

    mp := &MessageProcessor{
        publishers: make([]*rabbitmq.Publisher, poolSize),
        consumers:  make([]*rabbitmq.Consumer, poolSize),
        conn:       conn,
    }

    // Create multiple publishers for high throughput
    for i := 0; i < poolSize; i++ {
        pub, err := rabbitmq.NewPublisher(
            conn,
            rabbitmq.WithPublisherOptionsExchangeName("production.events"),
            rabbitmq.WithPublisherOptionsExchangeDurable,
            rabbitmq.WithPublisherOptionsConfirm,
        )
        if err != nil {
            return nil, err
        }
        mp.publishers[i] = pub
    }

    return mp, nil
}
```

## High Availability Patterns

### Quorum Queues

Enable quorum queues for high availability in clusters:

```go
func createQuorumConsumer() {
    conn, err := rabbitmq.NewConn("amqp://localhost")
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()

    consumer, err := rabbitmq.NewConsumer(
        conn,
        "ha.orders",
        rabbitmq.WithConsumerOptionsQueueDurable,          // Durable queue
        rabbitmq.WithConsumerOptionsQueueQuorum,           // Quorum queue for HA
        rabbitmq.WithConsumerOptionsQOSPrefetch(10),       // Moderate prefetch
        rabbitmq.WithConsumerOptionsConcurrency(5),        // Moderate concurrency
        rabbitmq.WithConsumerOptionsConsumerAutoAck(false), // Manual ack
    )
    if err != nil {
        log.Fatal(err)
    }
    defer consumer.Close()

    consumer.Consume(func(d rabbitmq.Delivery) (action rabbitmq.Action) {
        // Process message with proper error handling
        if err := processOrder(d.Body); err != nil {
            log.Printf("Failed to process order: %v", err)
            return rabbitmq.Retry
        }
        return rabbitmq.Ack
    })
}
```

### Publisher Confirms

Ensure message delivery with publisher confirms:

```go
func publishWithConfirms(publisher *rabbitmq.Publisher) error {
    // Publisher must be created with WithPublisherOptionsConfirm
    
    message := `{"event": "user.created", "user_id": "12345"}`
    
    err := publisher.Publish(
        []byte(message),
        []string{"user.created"},
        rabbitmq.WithPublishOptionsPersistentDelivery,
        rabbitmq.WithPublishOptionsMandatory, // Fail if no queue bound
        rabbitmq.WithPublishOptionsContentType("application/json"),
    )
    
    if err != nil {
        log.Printf("Failed to publish message: %v", err)
        return err
    }
    
    log.Println("Message published successfully with confirm")
    return nil
}
```

### Message TTL and Dead Letter Queues

Configure message expiration with dead letter handling:

```go
func createConsumerWithDLQ() {
    conn, err := rabbitmq.NewConn("amqp://localhost")
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()

    // Main queue with TTL and dead letter exchange
    consumer, err := rabbitmq.NewConsumer(
        conn,
        "main.processing",
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsQueueArgs(rabbitmq.Table{
            "x-message-ttl":             30000,  // 30 seconds TTL
            "x-dead-letter-exchange":    "dlx",  // Dead letter exchange
            "x-dead-letter-routing-key": "failed",
        }),
        rabbitmq.WithConsumerOptionsQOSPrefetch(10),
        rabbitmq.WithConsumerOptionsConsumerAutoAck(false),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer consumer.Close()

    consumer.Consume(func(d rabbitmq.Delivery) (action rabbitmq.Action) {
        // Process with retry logic
        if err := processWithRetry(d.Body); err != nil {
            log.Printf("Processing failed, message will go to DLQ: %v", err)
            return rabbitmq.Reject
        }
        return rabbitmq.Ack
    })
}
```

## Monitoring and Observability

### Logging Configuration

Enable comprehensive logging for production monitoring:

```go
import (
    "log"
    "os"
    
    "github.com/wagslane/go-rabbitmq"
)

type ProductionLogger struct {
    logger *log.Logger
}

func (l *ProductionLogger) Fatalf(format string, v ...interface{}) {
    l.logger.Printf("[FATAL] "+format, v...)
    os.Exit(1)
}

func (l *ProductionLogger) Errorf(format string, v ...interface{}) {
    l.logger.Printf("[ERROR] "+format, v...)
}

func (l *ProductionLogger) Warnf(format string, v ...interface{}) {
    l.logger.Printf("[WARN] "+format, v...)
}

func (l *ProductionLogger) Infof(format string, v ...interface{}) {
    l.logger.Printf("[INFO] "+format, v...)
}

func (l *ProductionLogger) Debugf(format string, v ...interface{}) {
    l.logger.Printf("[DEBUG] "+format, v...)
}

func createMonitoredConsumer() {
    conn, err := rabbitmq.NewConn("amqp://localhost")
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()

    prodLogger := &ProductionLogger{
        logger: log.New(os.Stdout, "[RABBITMQ] ", log.LstdFlags|log.Lshortfile),
    }

    consumer, err := rabbitmq.NewConsumer(
        conn,
        "monitored.queue",
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsLogger(prodLogger),    // Custom logger
        rabbitmq.WithConsumerOptionsQOSPrefetch(10),
        rabbitmq.WithConsumerOptionsConsumerAutoAck(false),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer consumer.Close()

    consumer.Consume(func(d rabbitmq.Delivery) (action rabbitmq.Action) {
        start := time.Now()
        
        // Process message
        err := processMessage(d.Body)
        
        // Log processing metrics
        duration := time.Since(start)
        prodLogger.Infof("Message processed in %v", duration)
        
        if err != nil {
            prodLogger.Errorf("Processing failed: %v", err)
            return rabbitmq.Retry
        }
        
        return rabbitmq.Ack
    })
}
```

### Health Checks and Metrics

Implement health checks for production deployments:

```go
func createHealthCheckConsumer() {
    conn, err := rabbitmq.NewConn("amqp://localhost")
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()

    consumer, err := rabbitmq.NewConsumer(
        conn,
        "health.check",
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsQOSPrefetch(1),
        rabbitmq.WithConsumerOptionsConsumerAutoAck(false),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer consumer.Close()

    // Track metrics
    var processedCount int64
    var errorCount int64
    
    consumer.Consume(func(d rabbitmq.Delivery) (action rabbitmq.Action) {
        // Process message
        if err := processMessage(d.Body); err != nil {
            atomic.AddInt64(&errorCount, 1)
            return rabbitmq.Retry
        }
        
        atomic.AddInt64(&processedCount, 1)
        return rabbitmq.Ack
    })
    
    // Expose metrics endpoint
    http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprintf(w, "processed_messages: %d\n", atomic.LoadInt64(&processedCount))
        fmt.Fprintf(w, "error_count: %d\n", atomic.LoadInt64(&errorCount))
    })
}
```

## Complete Production Examples

### High-Throughput Order Processing System

```go
package main

import (
    "context"
    "encoding/json"
    "log"
    "os"
    "os/signal"
    "sync"
    "syscall"
    "time"

    "github.com/wagslane/go-rabbitmq"
)

type OrderProcessor struct {
    conn      *rabbitmq.Conn
    consumer  *rabbitmq.Consumer
    publisher *rabbitmq.Publisher
    logger    *log.Logger
}

type Order struct {
    ID        string    `json:"id"`
    CustomerID string   `json:"customer_id"`
    Amount    float64   `json:"amount"`
    Status    string    `json:"status"`
    CreatedAt time.Time `json:"created_at"`
}

func NewOrderProcessor(amqpURL string) (*OrderProcessor, error) {
    conn, err := rabbitmq.NewConn(amqpURL)
    if err != nil {
        return nil, err
    }

    logger := log.New(os.Stdout, "[ORDER-PROCESSOR] ", log.LstdFlags|log.Lshortfile)

    // Create consumer with production settings
    consumer, err := rabbitmq.NewConsumer(
        conn,
        "orders.processing",
        rabbitmq.WithConsumerOptionsQueueDurable,          // Survive restarts
        rabbitmq.WithConsumerOptionsQueueQuorum,           // High availability
        rabbitmq.WithConsumerOptionsQOSPrefetch(20),       // Optimal prefetch
        rabbitmq.WithConsumerOptionsConcurrency(10),       // Concurrent processing
        rabbitmq.WithConsumerOptionsConsumerAutoAck(false), // Manual ack
        rabbitmq.WithConsumerOptionsExchangeName("orders"),
        rabbitmq.WithConsumerOptionsExchangeDurable,
        rabbitmq.WithConsumerOptionsExchangeDeclare,
        rabbitmq.WithConsumerOptionsRoutingKey("order.created"),
        rabbitmq.WithConsumerOptionsQueueArgs(rabbitmq.Table{
            "x-dead-letter-exchange":    "orders.dlx",
            "x-dead-letter-routing-key": "failed",
        }),
    )
    if err != nil {
        return nil, err
    }

    // Create publisher for order status updates
    publisher, err := rabbitmq.NewPublisher(
        conn,
        rabbitmq.WithPublisherOptionsExchangeName("orders.status"),
        rabbitmq.WithPublisherOptionsExchangeDurable,
        rabbitmq.WithPublisherOptionsExchangeDeclare,
        rabbitmq.WithPublisherOptionsConfirm, // Publisher confirms
    )
    if err != nil {
        return nil, err
    }

    return &OrderProcessor{
        conn:      conn,
        consumer:  consumer,
        publisher: publisher,
        logger:    logger,
    }, nil
}

func (op *OrderProcessor) Start(ctx context.Context) error {
    op.logger.Println("Starting order processor...")

    return op.consumer.Consume(func(d rabbitmq.Delivery) (action rabbitmq.Action) {
        var order Order
        if err := json.Unmarshal(d.Body, &order); err != nil {
            op.logger.Printf("Failed to unmarshal order: %v", err)
            return rabbitmq.Reject
        }

        op.logger.Printf("Processing order: %s", order.ID)

        // Process the order
        if err := op.processOrder(&order); err != nil {
            op.logger.Printf("Failed to process order %s: %v", order.ID, err)
            return rabbitmq.Retry
        }

        // Publish status update
        if err := op.publishStatusUpdate(&order); err != nil {
            op.logger.Printf("Failed to publish status update: %v", err)
            // Don't retry the entire message, just log the error
        }

        op.logger.Printf("Order %s processed successfully", order.ID)
        return rabbitmq.Ack
    })
}

func (op *OrderProcessor) processOrder(order *Order) error {
    // Simulate order processing
    time.Sleep(50 * time.Millisecond)
    
    // Update order status
    order.Status = "processed"
    
    return nil
}

func (op *OrderProcessor) publishStatusUpdate(order *Order) error {
    statusUpdate := map[string]interface{}{
        "order_id": order.ID,
        "status":   order.Status,
        "updated_at": time.Now(),
    }

    data, err := json.Marshal(statusUpdate)
    if err != nil {
        return err
    }

    return op.publisher.Publish(
        data,
        []string{"order.status.updated"},
        rabbitmq.WithPublishOptionsPersistentDelivery,
        rabbitmq.WithPublishOptionsContentType("application/json"),
        rabbitmq.WithPublishOptionsMandatory,
    )
}

func (op *OrderProcessor) Close() error {
    op.logger.Println("Shutting down order processor...")
    
    var wg sync.WaitGroup
    wg.Add(2)
    
    go func() {
        defer wg.Done()
        if err := op.consumer.Close(); err != nil {
            op.logger.Printf("Error closing consumer: %v", err)
        }
    }()
    
    go func() {
        defer wg.Done()
        if err := op.publisher.Close(); err != nil {
            op.logger.Printf("Error closing publisher: %v", err)
        }
    }()
    
    wg.Wait()
    return op.conn.Close()
}

func main() {
    processor, err := NewOrderProcessor("amqp://localhost")
    if err != nil {
        log.Fatal(err)
    }

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Handle graceful shutdown
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

    go func() {
        <-sigChan
        log.Println("Received shutdown signal")
        cancel()
    }()

    // Start processing
    if err := processor.Start(ctx); err != nil {
        log.Fatal(err)
    }

    // Cleanup
    if err := processor.Close(); err != nil {
        log.Printf("Error during cleanup: %v", err)
    }
}
```

### Critical Message Publisher with Retry Logic

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/wagslane/go-rabbitmq"
)

type CriticalPublisher struct {
    publisher *rabbitmq.Publisher
    logger    *log.Logger
}

func NewCriticalPublisher(amqpURL string) (*CriticalPublisher, error) {
    conn, err := rabbitmq.NewConn(amqpURL)
    if err != nil {
        return nil, err
    }

    publisher, err := rabbitmq.NewPublisher(
        conn,
        rabbitmq.WithPublisherOptionsExchangeName("critical.events"),
        rabbitmq.WithPublisherOptionsExchangeDurable,      // Survive restarts
        rabbitmq.WithPublisherOptionsExchangeDeclare,      // Create if needed
        rabbitmq.WithPublisherOptionsConfirm,              // Publisher confirms
    )
    if err != nil {
        return nil, err
    }

    return &CriticalPublisher{
        publisher: publisher,
        logger:    log.New(os.Stdout, "[CRITICAL-PUBLISHER] ", log.LstdFlags),
    }, nil
}

func (cp *CriticalPublisher) PublishCritical(ctx context.Context, message []byte, routingKey string) error {
    maxRetries := 3
    retryDelay := time.Second

    for attempt := 1; attempt <= maxRetries; attempt++ {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
        }

        cp.logger.Printf("Publishing message (attempt %d/%d)", attempt, maxRetries)

        err := cp.publisher.Publish(
            message,
            []string{routingKey},
            rabbitmq.WithPublishOptionsPersistentDelivery,     // Survive restarts
            rabbitmq.WithPublishOptionsMandatory,              // Fail if no queue
            rabbitmq.WithPublishOptionsContentType("application/json"),
            rabbitmq.WithPublishOptionsExpiration("3600000"),  // 1 hour TTL
        )

        if err == nil {
            cp.logger.Printf("Message published successfully on attempt %d", attempt)
            return nil
        }

        cp.logger.Printf("Publish attempt %d failed: %v", attempt, err)

        if attempt < maxRetries {
            time.Sleep(retryDelay)
            retryDelay *= 2 // Exponential backoff
        }
    }

    return fmt.Errorf("failed to publish after %d attempts", maxRetries)
}

func (cp *CriticalPublisher) Close() error {
    return cp.publisher.Close()
}
```

## Best Practices Summary

1. **Always use durable queues and exchanges** in production
2. **Enable persistent delivery** for critical messages
3. **Disable auto-ack** and handle acknowledgments manually
4. **Use publisher confirms** for guaranteed delivery
5. **Implement proper error handling** with retry logic
6. **Configure appropriate prefetch** values based on workload
7. **Use quorum queues** for high availability requirements
8. **Implement dead letter queues** for failed message handling
9. **Add comprehensive logging** for monitoring and debugging
10. **Handle graceful shutdown** in your applications

This guide provides the foundation for building robust, production-ready RabbitMQ applications with proper durability, performance, and monitoring capabilities.