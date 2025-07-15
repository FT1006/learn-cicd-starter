# Error Handling Patterns in go-rabbitmq

This guide covers comprehensive error handling strategies for RabbitMQ consumers, including dead letter queues, retry mechanisms, and poison message handling.

## Overview of Error Handling Strategies

### 1. Message Acknowledgment Actions
The go-rabbitmq library provides three main actions for handling messages:

- **`rabbitmq.Ack`** - Message processed successfully
- **`rabbitmq.NackRequeue`** - Temporary failure, requeue for retry
- **`rabbitmq.NackDiscard`** - Permanent failure, discard or send to DLQ

### 2. Error Handling Hierarchy
```
Message Processing
├── Success → Ack
├── Temporary Error → NackRequeue (with retry limits)
├── Permanent Error → NackDiscard (to DLQ)
└── Poison Message → NackDiscard (to DLQ + alert)
```

## Dead Letter Queue Setup

### Basic DLQ Configuration
```go
package main

import (
    "log"
    "time"
    
    rabbitmq "github.com/wagslane/go-rabbitmq"
)

func setupMainQueueWithDLQ(conn *rabbitmq.Conn) (*rabbitmq.Consumer, error) {
    // Main queue with DLQ configuration
    consumer, err := rabbitmq.NewConsumer(
        conn,
        "orders-queue",
        rabbitmq.WithConsumerOptionsExchangeName("orders"),
        rabbitmq.WithConsumerOptionsRoutingKey("order.*"),
        rabbitmq.WithConsumerOptionsExchangeDeclare,
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsQueueArgs(rabbitmq.Table{
            "x-dead-letter-exchange":    "orders-dlq",
            "x-dead-letter-routing-key": "failed",
            "x-message-ttl":             300000, // 5 minutes
        }),
        rabbitmq.WithConsumerOptionsQOSPrefetch(10),
        rabbitmq.WithConsumerOptionsConcurrency(3),
    )
    
    return consumer, err
}

func setupDLQConsumer(conn *rabbitmq.Conn) (*rabbitmq.Consumer, error) {
    // Dead letter queue consumer
    dlqConsumer, err := rabbitmq.NewConsumer(
        conn,
        "orders-dlq-queue",
        rabbitmq.WithConsumerOptionsExchangeName("orders-dlq"),
        rabbitmq.WithConsumerOptionsRoutingKey("failed"),
        rabbitmq.WithConsumerOptionsExchangeDeclare,
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsQOSPrefetch(5),
        rabbitmq.WithConsumerOptionsConcurrency(1), // Process DLQ slowly
    )
    
    return dlqConsumer, err
}
```

### Advanced DLQ with Retry Queue
```go
func setupRetryPattern(conn *rabbitmq.Conn) (*rabbitmq.Consumer, *rabbitmq.Consumer, error) {
    // Main processing queue
    mainConsumer, err := rabbitmq.NewConsumer(
        conn,
        "orders-main",
        rabbitmq.WithConsumerOptionsExchangeName("orders"),
        rabbitmq.WithConsumerOptionsRoutingKey("order.process"),
        rabbitmq.WithConsumerOptionsExchangeDeclare,
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsQueueArgs(rabbitmq.Table{
            "x-dead-letter-exchange":    "orders-retry",
            "x-dead-letter-routing-key": "retry",
        }),
    )
    if err != nil {
        return nil, nil, err
    }
    
    // Retry queue with TTL that feeds back to main queue
    retryConsumer, err := rabbitmq.NewConsumer(
        conn,
        "orders-retry-queue",
        rabbitmq.WithConsumerOptionsExchangeName("orders-retry"),
        rabbitmq.WithConsumerOptionsRoutingKey("retry"),
        rabbitmq.WithConsumerOptionsExchangeDeclare,
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsQueueArgs(rabbitmq.Table{
            "x-message-ttl":             30000, // 30 seconds delay
            "x-dead-letter-exchange":    "orders",
            "x-dead-letter-routing-key": "order.process",
        }),
        rabbitmq.WithConsumerOptionsQOSPrefetch(1),
    )
    
    return mainConsumer, retryConsumer, err
}
```

## Retry Mechanisms with Exponential Backoff

### Message Processing with Retry Logic
```go
package main

import (
    "encoding/json"
    "errors"
    "fmt"
    "log"
    "time"
    
    rabbitmq "github.com/wagslane/go-rabbitmq"
)

// RetryableError indicates a temporary failure that should be retried
type RetryableError struct {
    Err error
    Retryable bool
}

func (e *RetryableError) Error() string {
    return e.Err.Error()
}

// MessageMetadata tracks retry attempts and error information
type MessageMetadata struct {
    RetryCount    int       `json:"retry_count"`
    FirstAttempt  time.Time `json:"first_attempt"`
    LastAttempt   time.Time `json:"last_attempt"`
    ErrorHistory  []string  `json:"error_history"`
    MaxRetries    int       `json:"max_retries"`
}

// OrderMessage represents a business message with retry metadata
type OrderMessage struct {
    ID       string          `json:"id"`
    Data     json.RawMessage `json:"data"`
    Metadata MessageMetadata `json:"metadata"`
}

func processOrderWithRetry(d rabbitmq.Delivery) rabbitmq.Action {
    var message OrderMessage
    
    // Parse message with metadata
    if err := json.Unmarshal(d.Body, &message); err != nil {
        log.Printf("Failed to unmarshal message: %v", err)
        return rabbitmq.NackDiscard // Malformed message, send to DLQ
    }
    
    // Initialize metadata if first attempt
    if message.Metadata.RetryCount == 0 {
        message.Metadata.FirstAttempt = time.Now()
        message.Metadata.MaxRetries = 3
    }
    
    message.Metadata.RetryCount++
    message.Metadata.LastAttempt = time.Now()
    
    // Process the order
    err := processOrder(message.Data)
    
    if err == nil {
        log.Printf("Order %s processed successfully after %d attempts", 
            message.ID, message.Metadata.RetryCount)
        return rabbitmq.Ack
    }
    
    // Handle different error types
    var retryableErr *RetryableError
    if errors.As(err, &retryableErr) {
        if retryableErr.Retryable && message.Metadata.RetryCount < message.Metadata.MaxRetries {
            message.Metadata.ErrorHistory = append(message.Metadata.ErrorHistory, err.Error())
            
            // Republish with updated metadata for retry
            if republishErr := republishForRetry(message); republishErr != nil {
                log.Printf("Failed to republish for retry: %v", republishErr)
                return rabbitmq.NackDiscard
            }
            
            log.Printf("Order %s failed (attempt %d/%d): %v - requeuing", 
                message.ID, message.Metadata.RetryCount, message.Metadata.MaxRetries, err)
            return rabbitmq.Ack // We handled the retry, ack the original
        }
    }
    
    // Permanent failure or retry limit exceeded
    log.Printf("Order %s permanently failed after %d attempts: %v", 
        message.ID, message.Metadata.RetryCount, err)
    
    // Log failure details before sending to DLQ
    logFailureDetails(message, err)
    
    return rabbitmq.NackDiscard
}

func processOrder(data json.RawMessage) error {
    // Simulate different types of failures
    // This would be your actual business logic
    
    // Example: temporary network error (retryable)
    if shouldSimulateNetworkError() {
        return &RetryableError{
            Err: errors.New("network timeout"),
            Retryable: true,
        }
    }
    
    // Example: permanent validation error (not retryable)
    if shouldSimulateValidationError() {
        return &RetryableError{
            Err: errors.New("invalid order format"),
            Retryable: false,
        }
    }
    
    return nil // Success
}

func republishForRetry(message OrderMessage) error {
    // This would use a publisher to send the message back to the retry queue
    // with exponential backoff delay
    
    delay := calculateExponentialBackoff(message.Metadata.RetryCount)
    log.Printf("Scheduling retry for order %s after %v", message.ID, delay)
    
    // In practice, you'd publish to a retry queue with TTL
    // The message would be dead-lettered back to the main queue after the delay
    
    return nil
}

func calculateExponentialBackoff(retryCount int) time.Duration {
    baseDelay := 1 * time.Second
    maxDelay := 5 * time.Minute
    
    delay := time.Duration(1<<uint(retryCount)) * baseDelay
    if delay > maxDelay {
        delay = maxDelay
    }
    
    return delay
}

func shouldSimulateNetworkError() bool {
    // Simulate occasional network errors
    return time.Now().UnixNano()%10 == 0
}

func shouldSimulateValidationError() bool {
    // Simulate occasional validation errors
    return time.Now().UnixNano()%50 == 0
}

func logFailureDetails(message OrderMessage, err error) {
    log.Printf("FAILURE DETAILS for order %s:", message.ID)
    log.Printf("  - Total attempts: %d", message.Metadata.RetryCount)
    log.Printf("  - First attempt: %v", message.Metadata.FirstAttempt)
    log.Printf("  - Last attempt: %v", message.Metadata.LastAttempt)
    log.Printf("  - Final error: %v", err)
    log.Printf("  - Error history: %v", message.Metadata.ErrorHistory)
}
```

## Poison Message Handling

### Detecting and Handling Poison Messages
```go
package main

import (
    "fmt"
    "log"
    "strings"
    "time"
    
    rabbitmq "github.com/wagslane/go-rabbitmq"
)

// PoisonMessageDetector tracks message patterns to identify poison messages
type PoisonMessageDetector struct {
    messageHashes    map[string]int    // Hash -> failure count
    lastFailureTime  map[string]time.Time
    maxFailures      int
    timeWindow       time.Duration
}

func NewPoisonMessageDetector() *PoisonMessageDetector {
    return &PoisonMessageDetector{
        messageHashes:   make(map[string]int),
        lastFailureTime: make(map[string]time.Time),
        maxFailures:     5,
        timeWindow:      10 * time.Minute,
    }
}

func (pmd *PoisonMessageDetector) IsPoison(messageBody []byte, err error) bool {
    hash := fmt.Sprintf("%x", messageBody)[:16] // First 16 chars of hash
    
    now := time.Now()
    
    // Clean old entries
    if lastTime, exists := pmd.lastFailureTime[hash]; exists {
        if now.Sub(lastTime) > pmd.timeWindow {
            delete(pmd.messageHashes, hash)
            delete(pmd.lastFailureTime, hash)
        }
    }
    
    // Increment failure count
    pmd.messageHashes[hash]++
    pmd.lastFailureTime[hash] = now
    
    // Check if this is a poison message
    if pmd.messageHashes[hash] >= pmd.maxFailures {
        return true
    }
    
    // Additional heuristics for poison detection
    if isPoisonError(err) {
        return true
    }
    
    return false
}

func isPoisonError(err error) bool {
    if err == nil {
        return false
    }
    
    errorMsg := strings.ToLower(err.Error())
    
    // Common poison message indicators
    poisonIndicators := []string{
        "json: cannot unmarshal",
        "invalid character",
        "unexpected end of json",
        "unknown field",
        "invalid message format",
        "schema validation failed",
    }
    
    for _, indicator := range poisonIndicators {
        if strings.Contains(errorMsg, indicator) {
            return true
        }
    }
    
    return false
}

func handlePoisonMessage(d rabbitmq.Delivery, detector *PoisonMessageDetector) {
    log.Printf("POISON MESSAGE DETECTED:")
    log.Printf("  - Message ID: %s", d.MessageId)
    log.Printf("  - Routing Key: %s", d.RoutingKey)
    log.Printf("  - Exchange: %s", d.Exchange)
    log.Printf("  - Body (first 200 chars): %s", string(d.Body[:min(200, len(d.Body))]))
    log.Printf("  - Headers: %v", d.Headers)
    
    // Alert monitoring system
    alertMonitoring("poison_message", map[string]interface{}{
        "message_id":   d.MessageId,
        "routing_key":  d.RoutingKey,
        "exchange":     d.Exchange,
        "body_preview": string(d.Body[:min(100, len(d.Body))]),
    })
    
    // Store for analysis
    storePoisonMessage(d)
}

func alertMonitoring(eventType string, data map[string]interface{}) {
    // Integration with monitoring systems (Datadog, New Relic, etc.)
    log.Printf("MONITORING ALERT: %s - %v", eventType, data)
}

func storePoisonMessage(d rabbitmq.Delivery) {
    // Store poison message for later analysis
    // This could be a database, file, or another queue
    log.Printf("Storing poison message for analysis: %s", d.MessageId)
}

func min(a, b int) int {
    if a < b {
        return a
    }
    return b
}
```

## Complete Production-Ready Error Handling Example

### Main Application with Comprehensive Error Handling
```go
package main

import (
    "context"
    "encoding/json"
    "log"
    "os"
    "os/signal"
    "syscall"
    "time"
    
    rabbitmq "github.com/wagslane/go-rabbitmq"
)

func main() {
    // Connection setup
    conn, err := rabbitmq.NewConn(
        "amqp://guest:guest@localhost:5672/",
        rabbitmq.WithConnectionOptionsLogging,
    )
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()
    
    // Setup consumers
    mainConsumer, err := setupMainConsumer(conn)
    if err != nil {
        log.Fatal(err)
    }
    defer mainConsumer.Close()
    
    dlqConsumer, err := setupDLQConsumer(conn)
    if err != nil {
        log.Fatal(err)
    }
    defer dlqConsumer.Close()
    
    // Setup poison message detector
    detector := NewPoisonMessageDetector()
    
    // Setup graceful shutdown
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    
    sigs := make(chan os.Signal, 1)
    signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
    
    go func() {
        <-sigs
        log.Println("Shutting down gracefully...")
        cancel()
    }()
    
    // Start consumers
    go func() {
        err := mainConsumer.Run(createMainHandler(detector))
        if err != nil {
            log.Printf("Main consumer error: %v", err)
        }
    }()
    
    go func() {
        err := dlqConsumer.Run(createDLQHandler())
        if err != nil {
            log.Printf("DLQ consumer error: %v", err)
        }
    }()
    
    // Wait for shutdown
    <-ctx.Done()
    log.Println("Application stopped")
}

func setupMainConsumer(conn *rabbitmq.Conn) (*rabbitmq.Consumer, error) {
    return rabbitmq.NewConsumer(
        conn,
        "orders-main-queue",
        rabbitmq.WithConsumerOptionsExchangeName("orders"),
        rabbitmq.WithConsumerOptionsRoutingKey("order.process"),
        rabbitmq.WithConsumerOptionsExchangeDeclare,
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsQueueArgs(rabbitmq.Table{
            "x-dead-letter-exchange":    "orders-dlq",
            "x-dead-letter-routing-key": "failed",
        }),
        rabbitmq.WithConsumerOptionsQOSPrefetch(10),
        rabbitmq.WithConsumerOptionsConcurrency(3),
        rabbitmq.WithConsumerOptionsLogging,
    )
}

func setupDLQConsumer(conn *rabbitmq.Conn) (*rabbitmq.Consumer, error) {
    return rabbitmq.NewConsumer(
        conn,
        "orders-dlq-queue",
        rabbitmq.WithConsumerOptionsExchangeName("orders-dlq"),
        rabbitmq.WithConsumerOptionsRoutingKey("failed"),
        rabbitmq.WithConsumerOptionsExchangeDeclare,
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsQOSPrefetch(1),
        rabbitmq.WithConsumerOptionsConcurrency(1),
        rabbitmq.WithConsumerOptionsLogging,
    )
}

func createMainHandler(detector *PoisonMessageDetector) func(rabbitmq.Delivery) rabbitmq.Action {
    return func(d rabbitmq.Delivery) rabbitmq.Action {
        start := time.Now()
        
        log.Printf("Processing message: %s", d.MessageId)
        
        // Simulate processing
        err := processBusinessLogic(d.Body)
        
        processingTime := time.Since(start)
        
        if err != nil {
            log.Printf("Processing failed after %v: %v", processingTime, err)
            
            // Check if this is a poison message
            if detector.IsPoison(d.Body, err) {
                handlePoisonMessage(d, detector)
                return rabbitmq.NackDiscard
            }
            
            // Determine retry strategy based on error type
            var retryableErr *RetryableError
            if errors.As(err, &retryableErr) && retryableErr.Retryable {
                log.Printf("Retryable error, requeueing: %v", err)
                return rabbitmq.NackRequeue
            }
            
            log.Printf("Non-retryable error, sending to DLQ: %v", err)
            return rabbitmq.NackDiscard
        }
        
        log.Printf("Message processed successfully in %v", processingTime)
        return rabbitmq.Ack
    }
}

func createDLQHandler() func(rabbitmq.Delivery) rabbitmq.Action {
    return func(d rabbitmq.Delivery) rabbitmq.Action {
        log.Printf("Processing DLQ message: %s", d.MessageId)
        
        // Log failure details
        logDLQMessage(d)
        
        // Attempt manual processing or alerting
        if canManuallyProcess(d) {
            log.Printf("Manual processing successful for DLQ message: %s", d.MessageId)
            return rabbitmq.Ack
        }
        
        // Store for later analysis
        storeDLQMessage(d)
        
        return rabbitmq.Ack
    }
}

func processBusinessLogic(body []byte) error {
    // Your actual business logic here
    var order map[string]interface{}
    if err := json.Unmarshal(body, &order); err != nil {
        return &RetryableError{
            Err: err,
            Retryable: false, // JSON parsing errors are not retryable
        }
    }
    
    // Simulate different error scenarios
    if _, exists := order["invalid_field"]; exists {
        return &RetryableError{
            Err: errors.New("invalid field in order"),
            Retryable: false,
        }
    }
    
    // Simulate temporary network error
    if time.Now().UnixNano()%10 == 0 {
        return &RetryableError{
            Err: errors.New("network timeout"),
            Retryable: true,
        }
    }
    
    return nil
}

func logDLQMessage(d rabbitmq.Delivery) {
    log.Printf("DLQ MESSAGE DETAILS:")
    log.Printf("  - Message ID: %s", d.MessageId)
    log.Printf("  - Routing Key: %s", d.RoutingKey)
    log.Printf("  - Exchange: %s", d.Exchange)
    log.Printf("  - Timestamp: %v", d.Timestamp)
    log.Printf("  - Headers: %v", d.Headers)
    log.Printf("  - Body preview: %s", string(d.Body[:min(200, len(d.Body))]))
}

func canManuallyProcess(d rabbitmq.Delivery) bool {
    // Implement logic to determine if message can be manually processed
    // This might involve checking headers, message content, etc.
    return false
}

func storeDLQMessage(d rabbitmq.Delivery) {
    // Store DLQ message for later analysis
    // This could be a database, file system, or another queue
    log.Printf("Storing DLQ message for analysis: %s", d.MessageId)
}
```

## When to Use Ack vs NackRequeue vs NackDiscard

### Decision Matrix

| Scenario | Action | Reasoning |
|----------|--------|-----------|
| **Successful Processing** | `Ack` | Message processed successfully |
| **Temporary Network Error** | `NackRequeue` | Retry might succeed |
| **Database Timeout** | `NackRequeue` | Temporary resource issue |
| **Rate Limit Hit** | `NackRequeue` | Temporary throttling |
| **Invalid JSON/Schema** | `NackDiscard` | Permanent parsing error |
| **Business Logic Validation Failed** | `NackDiscard` | Data doesn't meet requirements |
| **Poison Message Detected** | `NackDiscard` | Repeated failures indicate poison |
| **Max Retries Exceeded** | `NackDiscard` | Avoid infinite retry loops |

### Implementation Guidelines

```go
func determineAction(err error, retryCount int, maxRetries int) rabbitmq.Action {
    if err == nil {
        return rabbitmq.Ack
    }
    
    // Check retry count first
    if retryCount >= maxRetries {
        return rabbitmq.NackDiscard
    }
    
    // Analyze error type
    errorMsg := strings.ToLower(err.Error())
    
    // Permanent errors (don't retry)
    permanentErrors := []string{
        "json: cannot unmarshal",
        "invalid character",
        "validation failed",
        "bad request",
        "unauthorized",
        "forbidden",
        "not found",
    }
    
    for _, permErr := range permanentErrors {
        if strings.Contains(errorMsg, permErr) {
            return rabbitmq.NackDiscard
        }
    }
    
    // Temporary errors (retry)
    temporaryErrors := []string{
        "timeout",
        "connection refused",
        "service unavailable",
        "rate limit",
        "too many requests",
        "internal server error",
    }
    
    for _, tempErr := range temporaryErrors {
        if strings.Contains(errorMsg, tempErr) {
            return rabbitmq.NackRequeue
        }
    }
    
    // Default to discard if unsure
    return rabbitmq.NackDiscard
}
```

## Best Practices

1. **Always set up DLQ**: Every production consumer should have a dead letter queue
2. **Monitor DLQ size**: Alert when DLQ grows beyond normal levels
3. **Implement exponential backoff**: Avoid overwhelming failing services
4. **Limit retry attempts**: Prevent infinite retry loops
5. **Log failure details**: Include context for debugging
6. **Use poison message detection**: Identify and handle malformed messages
7. **Monitor processing times**: Track performance degradation
8. **Set appropriate TTL**: Prevent messages from staying in queues too long
9. **Use structured logging**: Make logs searchable and analyzable
10. **Test error scenarios**: Ensure error handling works as expected

This comprehensive error handling pattern ensures robust message processing with proper retry mechanisms, dead letter queue handling, and poison message detection.