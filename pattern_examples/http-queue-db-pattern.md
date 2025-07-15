# HTTP → Queue → DB Pattern with go-rabbitmq

## Overview

The HTTP → Queue → DB pattern is a common architecture that decouples HTTP request handling from database operations using a message queue. This pattern provides several benefits:

- **Scalability**: HTTP handlers can quickly accept requests and return responses without waiting for database operations
- **Reliability**: Failed database operations can be retried automatically
- **Resilience**: System continues to accept requests even if the database is temporarily unavailable
- **Load Balancing**: Multiple consumers can process database operations in parallel

## Architecture

```
HTTP Request → Web Server → RabbitMQ Queue → Consumer → Database
     ↓              ↓             ↓           ↓          ↓
   Webhook      Publisher     Exchange    Consumer   PostgreSQL
```

## Use Cases

- **Webhook Processing**: Accept webhooks from external services and process them asynchronously
- **Event-Driven Systems**: User actions trigger events that are processed in the background
- **Data Ingestion**: High-volume data ingestion where immediate processing isn't required
- **Audit Logging**: Log user actions without blocking the main request flow
- **Email/Notification Systems**: Send notifications asynchronously

## Complete Implementation

### 1. Project Structure

```
webhook-processor/
├── cmd/
│   ├── publisher/
│   │   └── main.go
│   └── consumer/
│       └── main.go
├── internal/
│   ├── models/
│   │   └── webhook.go
│   ├── handlers/
│   │   └── webhook.go
│   └── database/
│       └── db.go
├── docker-compose.yml
├── go.mod
└── go.sum
```

### 2. Data Models

```go
// internal/models/webhook.go
package models

import (
    "time"
    "encoding/json"
)

// WebhookPayload represents the incoming webhook data
type WebhookPayload struct {
    ID          string                 `json:"id"`
    Source      string                 `json:"source"`
    EventType   string                 `json:"event_type"`
    Timestamp   time.Time              `json:"timestamp"`
    Data        map[string]interface{} `json:"data"`
    Signature   string                 `json:"signature,omitempty"`
}

// WebhookRecord represents the database record
type WebhookRecord struct {
    ID          int64     `db:"id"`
    WebhookID   string    `db:"webhook_id"`
    Source      string    `db:"source"`
    EventType   string    `db:"event_type"`
    Payload     string    `db:"payload"`
    ProcessedAt time.Time `db:"processed_at"`
    CreatedAt   time.Time `db:"created_at"`
}

func (w *WebhookPayload) ToJSON() ([]byte, error) {
    return json.Marshal(w)
}

func WebhookFromJSON(data []byte) (*WebhookPayload, error) {
    var webhook WebhookPayload
    err := json.Unmarshal(data, &webhook)
    return &webhook, err
}
```

### 3. Publisher (HTTP Server)

```go
// cmd/publisher/main.go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/gorilla/mux"
    rabbitmq "github.com/wagslane/go-rabbitmq"
    "your-project/internal/models"
)

type WebhookServer struct {
    publisher *rabbitmq.Publisher
    logger    *log.Logger
}

func NewWebhookServer(publisher *rabbitmq.Publisher) *WebhookServer {
    return &WebhookServer{
        publisher: publisher,
        logger:    log.New(os.Stdout, "[WEBHOOK] ", log.LstdFlags),
    }
}

func (s *WebhookServer) handleWebhook(w http.ResponseWriter, r *http.Request) {
    // Parse webhook payload
    var payload models.WebhookPayload
    if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
        s.logger.Printf("Failed to decode webhook payload: %v", err)
        http.Error(w, "Invalid payload", http.StatusBadRequest)
        return
    }

    // Add timestamp if not present
    if payload.Timestamp.IsZero() {
        payload.Timestamp = time.Now()
    }

    // Convert to JSON for queue
    jsonData, err := payload.ToJSON()
    if err != nil {
        s.logger.Printf("Failed to marshal payload: %v", err)
        http.Error(w, "Processing error", http.StatusInternalServerError)
        return
    }

    // Publish to queue with production-ready options
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    err = s.publisher.PublishWithContext(
        ctx,
        jsonData,
        []string{"webhook.process"}, // routing key
        rabbitmq.WithPublishOptionsContentType("application/json"),
        rabbitmq.WithPublishOptionsPersistentDelivery,
        rabbitmq.WithPublishOptionsMandatory,
        rabbitmq.WithPublishOptionsExchange("webhooks"),
        rabbitmq.WithPublishOptionsMessageID(payload.ID),
        rabbitmq.WithPublishOptionsTimestamp(time.Now()),
    )

    if err != nil {
        s.logger.Printf("Failed to publish webhook: %v", err)
        http.Error(w, "Processing error", http.StatusInternalServerError)
        return
    }

    s.logger.Printf("Webhook published successfully: %s", payload.ID)
    
    // Return success response immediately
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusAccepted)
    json.NewEncoder(w).Encode(map[string]string{
        "status":  "accepted",
        "message": "Webhook queued for processing",
        "id":      payload.ID,
    })
}

func (s *WebhookServer) healthCheck(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    json.NewEncoder(w).Encode(map[string]string{
        "status": "healthy",
        "service": "webhook-publisher",
    })
}

func main() {
    // Connect to RabbitMQ
    conn, err := rabbitmq.NewConn(
        "amqp://guest:guest@localhost:5672",
        rabbitmq.WithConnectionOptionsLogging,
        rabbitmq.WithConnectionOptionsReconnectInterval(time.Second*5),
    )
    if err != nil {
        log.Fatal("Failed to connect to RabbitMQ:", err)
    }
    defer conn.Close()

    // Create publisher with production settings
    publisher, err := rabbitmq.NewPublisher(
        conn,
        rabbitmq.WithPublisherOptionsLogging,
        rabbitmq.WithPublisherOptionsExchangeName("webhooks"),
        rabbitmq.WithPublisherOptionsExchangeKind("direct"),
        rabbitmq.WithPublisherOptionsExchangeDurable,
        rabbitmq.WithPublisherOptionsExchangeDeclare,
        rabbitmq.WithPublisherOptionsConfirm, // Enable publisher confirms
    )
    if err != nil {
        log.Fatal("Failed to create publisher:", err)
    }
    defer publisher.Close()

    // Set up publisher confirmation and return handlers
    publisher.NotifyReturn(func(r rabbitmq.Return) {
        log.Printf("Message returned: %s (reason: %s)", string(r.Body), r.ReplyText)
    })

    publisher.NotifyPublish(func(c rabbitmq.Confirmation) {
        if c.Ack {
            log.Printf("Message confirmed: tag=%v", c.DeliveryTag)
        } else {
            log.Printf("Message rejected: tag=%v", c.DeliveryTag)
        }
    })

    // Create webhook server
    server := NewWebhookServer(publisher)

    // Set up HTTP routes
    router := mux.NewRouter()
    router.HandleFunc("/webhook", server.handleWebhook).Methods("POST")
    router.HandleFunc("/health", server.healthCheck).Methods("GET")

    // HTTP server configuration
    httpServer := &http.Server{
        Addr:         ":8080",
        Handler:      router,
        ReadTimeout:  10 * time.Second,
        WriteTimeout: 10 * time.Second,
        IdleTimeout:  60 * time.Second,
    }

    // Start HTTP server in goroutine
    go func() {
        log.Printf("Starting webhook server on :8080")
        if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatal("HTTP server error:", err)
        }
    }()

    // Graceful shutdown
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

    <-sigChan
    log.Println("Shutting down webhook server...")

    // Shutdown HTTP server
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    if err := httpServer.Shutdown(ctx); err != nil {
        log.Printf("HTTP server shutdown error: %v", err)
    }

    log.Println("Webhook server stopped")
}
```

### 4. Consumer (Database Writer)

```go
// cmd/consumer/main.go
package main

import (
    "context"
    "database/sql"
    "fmt"
    "log"
    "os"
    "os/signal"
    "syscall"
    "time"

    _ "github.com/lib/pq"
    rabbitmq "github.com/wagslane/go-rabbitmq"
    "your-project/internal/models"
)

type WebhookProcessor struct {
    db     *sql.DB
    logger *log.Logger
}

func NewWebhookProcessor(db *sql.DB) *WebhookProcessor {
    return &WebhookProcessor{
        db:     db,
        logger: log.New(os.Stdout, "[PROCESSOR] ", log.LstdFlags),
    }
}

func (p *WebhookProcessor) processWebhook(d rabbitmq.Delivery) rabbitmq.Action {
    p.logger.Printf("Processing webhook: %s", d.MessageId)

    // Parse webhook payload
    webhook, err := models.WebhookFromJSON(d.Body)
    if err != nil {
        p.logger.Printf("Failed to parse webhook JSON: %v", err)
        return rabbitmq.NackDiscard // Discard malformed messages
    }

    // Validate webhook
    if webhook.ID == "" || webhook.Source == "" || webhook.EventType == "" {
        p.logger.Printf("Invalid webhook data: missing required fields")
        return rabbitmq.NackDiscard
    }

    // Save to database with retry logic
    maxRetries := 3
    for attempt := 1; attempt <= maxRetries; attempt++ {
        err = p.saveWebhookToDB(webhook)
        if err == nil {
            p.logger.Printf("Webhook processed successfully: %s", webhook.ID)
            return rabbitmq.Ack
        }

        p.logger.Printf("Database error (attempt %d/%d): %v", attempt, maxRetries, err)
        
        if attempt < maxRetries {
            // Wait before retrying
            time.Sleep(time.Duration(attempt) * time.Second)
        }
    }

    // After all retries failed, decide whether to requeue or discard
    if isRetryableError(err) {
        p.logger.Printf("Requeuing webhook after %d failed attempts: %s", maxRetries, webhook.ID)
        return rabbitmq.NackRequeue
    } else {
        p.logger.Printf("Discarding webhook after %d failed attempts: %s", maxRetries, webhook.ID)
        return rabbitmq.NackDiscard
    }
}

func (p *WebhookProcessor) saveWebhookToDB(webhook *models.WebhookPayload) error {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    // Convert payload to JSON string for storage
    payloadJSON, err := webhook.ToJSON()
    if err != nil {
        return fmt.Errorf("failed to marshal payload: %w", err)
    }

    query := `
        INSERT INTO webhooks (webhook_id, source, event_type, payload, processed_at, created_at)
        VALUES ($1, $2, $3, $4, $5, $6)
        ON CONFLICT (webhook_id) DO UPDATE SET
            processed_at = EXCLUDED.processed_at,
            payload = EXCLUDED.payload
    `

    _, err = p.db.ExecContext(ctx, query,
        webhook.ID,
        webhook.Source,
        webhook.EventType,
        string(payloadJSON),
        time.Now(),
        webhook.Timestamp,
    )

    return err
}

func isRetryableError(err error) bool {
    // Check for specific database errors that are retryable
    if err == nil {
        return false
    }
    
    errStr := err.Error()
    
    // Connection errors are retryable
    if contains(errStr, "connection refused") ||
       contains(errStr, "timeout") ||
       contains(errStr, "connection reset") {
        return true
    }
    
    // Constraint violations are not retryable
    if contains(errStr, "constraint") ||
       contains(errStr, "duplicate") {
        return false
    }
    
    // Default to not retryable for unknown errors
    return false
}

func contains(str, substr string) bool {
    return len(str) >= len(substr) && 
           (str == substr || 
            (len(str) > len(substr) && 
             (str[:len(substr)] == substr || 
              str[len(str)-len(substr):] == substr || 
              findInString(str, substr))))
}

func findInString(str, substr string) bool {
    for i := 0; i <= len(str)-len(substr); i++ {
        if str[i:i+len(substr)] == substr {
            return true
        }
    }
    return false
}

func initDB() (*sql.DB, error) {
    dbURL := os.Getenv("DATABASE_URL")
    if dbURL == "" {
        dbURL = "postgres://user:password@localhost/webhooks?sslmode=disable"
    }

    db, err := sql.Open("postgres", dbURL)
    if err != nil {
        return nil, fmt.Errorf("failed to open database: %w", err)
    }

    // Configure connection pool
    db.SetMaxOpenConns(25)
    db.SetMaxIdleConns(5)
    db.SetConnMaxLifetime(30 * time.Minute)

    // Test connection
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    if err := db.PingContext(ctx); err != nil {
        return nil, fmt.Errorf("failed to ping database: %w", err)
    }

    return db, nil
}

func createTables(db *sql.DB) error {
    query := `
        CREATE TABLE IF NOT EXISTS webhooks (
            id SERIAL PRIMARY KEY,
            webhook_id VARCHAR(255) UNIQUE NOT NULL,
            source VARCHAR(255) NOT NULL,
            event_type VARCHAR(255) NOT NULL,
            payload JSONB NOT NULL,
            processed_at TIMESTAMP NOT NULL DEFAULT NOW(),
            created_at TIMESTAMP NOT NULL DEFAULT NOW()
        );
        
        CREATE INDEX IF NOT EXISTS idx_webhooks_source ON webhooks(source);
        CREATE INDEX IF NOT EXISTS idx_webhooks_event_type ON webhooks(event_type);
        CREATE INDEX IF NOT EXISTS idx_webhooks_created_at ON webhooks(created_at);
    `

    _, err := db.Exec(query)
    return err
}

func main() {
    // Initialize database
    db, err := initDB()
    if err != nil {
        log.Fatal("Failed to initialize database:", err)
    }
    defer db.Close()

    // Create tables
    if err := createTables(db); err != nil {
        log.Fatal("Failed to create tables:", err)
    }

    // Connect to RabbitMQ
    conn, err := rabbitmq.NewConn(
        "amqp://guest:guest@localhost:5672",
        rabbitmq.WithConnectionOptionsLogging,
        rabbitmq.WithConnectionOptionsReconnectInterval(time.Second*5),
    )
    if err != nil {
        log.Fatal("Failed to connect to RabbitMQ:", err)
    }
    defer conn.Close()

    // Create consumer with production settings
    consumer, err := rabbitmq.NewConsumer(
        conn,
        "webhook-processing-queue",
        rabbitmq.WithConsumerOptionsLogging,
        rabbitmq.WithConsumerOptionsExchangeName("webhooks"),
        rabbitmq.WithConsumerOptionsExchangeKind("direct"),
        rabbitmq.WithConsumerOptionsExchangeDurable,
        rabbitmq.WithConsumerOptionsExchangeDeclare,
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsRoutingKey("webhook.process"),
        rabbitmq.WithConsumerOptionsQOSPrefetch(5), // Process 5 messages at a time
        rabbitmq.WithConsumerOptionsConcurrency(3), // 3 concurrent processors
    )
    if err != nil {
        log.Fatal("Failed to create consumer:", err)
    }

    // Create processor
    processor := NewWebhookProcessor(db)

    // Set up graceful shutdown
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

    go func() {
        sig := <-sigChan
        log.Printf("Received signal %s, shutting down gracefully...", sig)
        consumer.Close()
    }()

    log.Println("Starting webhook consumer...")
    log.Println("Waiting for webhooks to process...")

    // Start consuming messages
    err = consumer.Run(processor.processWebhook)
    if err != nil {
        log.Fatal("Consumer error:", err)
    }

    log.Println("Webhook consumer stopped")
}
```

### 5. Docker Compose Setup

```yaml
# docker-compose.yml
version: '3.8'

services:
  rabbitmq:
    image: rabbitmq:3.12-management-alpine
    container_name: webhook-rabbitmq
    ports:
      - "5672:5672"
      - "15672:15672"
    environment:
      RABBITMQ_DEFAULT_USER: guest
      RABBITMQ_DEFAULT_PASS: guest
    volumes:
      - rabbitmq_data:/var/lib/rabbitmq
    healthcheck:
      test: rabbitmq-diagnostics -q ping
      interval: 30s
      timeout: 30s
      retries: 3

  postgres:
    image: postgres:15-alpine
    container_name: webhook-postgres
    environment:
      POSTGRES_DB: webhooks
      POSTGRES_USER: user
      POSTGRES_PASSWORD: password
    ports:
      - "5432:5432"
    volumes:
      - postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U user -d webhooks"]
      interval: 10s
      timeout: 5s
      retries: 5

  webhook-publisher:
    build: 
      context: .
      dockerfile: Dockerfile.publisher
    container_name: webhook-publisher
    ports:
      - "8080:8080"
    depends_on:
      rabbitmq:
        condition: service_healthy
    environment:
      RABBITMQ_URL: amqp://guest:guest@rabbitmq:5672
    restart: unless-stopped

  webhook-consumer:
    build: 
      context: .
      dockerfile: Dockerfile.consumer
    container_name: webhook-consumer
    depends_on:
      rabbitmq:
        condition: service_healthy
      postgres:
        condition: service_healthy
    environment:
      RABBITMQ_URL: amqp://guest:guest@rabbitmq:5672
      DATABASE_URL: postgres://user:password@postgres:5432/webhooks?sslmode=disable
    restart: unless-stopped
    scale: 2  # Run 2 consumer instances

volumes:
  rabbitmq_data:
  postgres_data:
```

### 6. Go Module Configuration

```go
// go.mod
module your-project

go 1.21

require (
    github.com/gorilla/mux v1.8.1
    github.com/lib/pq v1.10.9
    github.com/wagslane/go-rabbitmq v0.12.4
)

require (
    github.com/rabbitmq/amqp091-go v1.9.0 // indirect
)
```

## Option Explanations

### Publisher Options

#### Exchange Configuration
- **`WithPublisherOptionsExchangeName("webhooks")`**: Sets the exchange name where messages are published
- **`WithPublisherOptionsExchangeKind("direct")`**: Uses direct exchange for routing by exact key match
- **`WithPublisherOptionsExchangeDurable`**: Exchange survives server restarts
- **`WithPublisherOptionsExchangeDeclare`**: Creates exchange if it doesn't exist

#### Reliability Options
- **`WithPublisherOptionsConfirm`**: Enables publisher confirmations for message acknowledgment
- **`WithPublishOptionsPersistentDelivery`**: Messages survive server restarts
- **`WithPublishOptionsMandatory`**: Returns message if no queue is bound to routing key

### Consumer Options

#### Queue Configuration
- **`WithConsumerOptionsQueueDurable`**: Queue survives server restarts
- **`WithConsumerOptionsQOSPrefetch(5)`**: Prefetch 5 messages for better throughput
- **`WithConsumerOptionsConcurrency(3)`**: Process 3 messages concurrently

#### Exchange Binding
- **`WithConsumerOptionsExchangeName("webhooks")`**: Bind to webhooks exchange
- **`WithConsumerOptionsRoutingKey("webhook.process")`**: Listen for specific routing key

## Error Handling Strategies

### 1. Publisher Error Handling

```go
// Timeout handling
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

// Retry logic for publishing
maxRetries := 3
for attempt := 1; attempt <= maxRetries; attempt++ {
    err = publisher.PublishWithContext(ctx, jsonData, routingKeys, options...)
    if err == nil {
        break
    }
    
    if attempt < maxRetries {
        time.Sleep(time.Duration(attempt) * time.Second)
    }
}
```

### 2. Consumer Error Handling

```go
func (p *WebhookProcessor) processWebhook(d rabbitmq.Delivery) rabbitmq.Action {
    // Parse errors -> NackDiscard (don't retry malformed messages)
    if err := json.Unmarshal(d.Body, &webhook); err != nil {
        return rabbitmq.NackDiscard
    }
    
    // Database errors -> NackRequeue (retry later)
    if err := p.saveWebhookToDB(webhook); err != nil {
        if isRetryableError(err) {
            return rabbitmq.NackRequeue
        }
        return rabbitmq.NackDiscard
    }
    
    // Success -> Ack
    return rabbitmq.Ack
}
```

### 3. Action Types

- **`rabbitmq.Ack`**: Message processed successfully
- **`rabbitmq.NackDiscard`**: Message processing failed, discard it
- **`rabbitmq.NackRequeue`**: Message processing failed, requeue for retry

## Production Considerations

### 1. Message Durability
```go
// Publisher: Make messages persistent
rabbitmq.WithPublishOptionsPersistentDelivery,

// Consumer: Make queue durable
rabbitmq.WithConsumerOptionsQueueDurable,
```

### 2. Error Handling and Dead Letter Queues
```go
// Configure dead letter exchange for failed messages
rabbitmq.WithConsumerOptionsQueueArgs(rabbitmq.Table{
    "x-dead-letter-exchange": "webhooks.dlx",
    "x-dead-letter-routing-key": "failed",
})
```

### 3. Connection Management
```go
// Automatic reconnection
rabbitmq.WithConnectionOptionsReconnectInterval(time.Second*5),

// Connection monitoring
conn.NotifyClose(func(err error) {
    log.Printf("Connection closed: %v", err)
})
```

### 4. Monitoring and Observability
```go
// Publisher confirmations
publisher.NotifyReturn(func(r rabbitmq.Return) {
    log.Printf("Message returned: %s", r.ReplyText)
})

publisher.NotifyPublish(func(c rabbitmq.Confirmation) {
    if !c.Ack {
        log.Printf("Message not confirmed: %v", c.DeliveryTag)
    }
})
```

### 5. Resource Management
```go
// Database connection pool
db.SetMaxOpenConns(25)
db.SetMaxIdleConns(5)
db.SetConnMaxLifetime(30 * time.Minute)

// Consumer concurrency
rabbitmq.WithConsumerOptionsConcurrency(3),
rabbitmq.WithConsumerOptionsQOSPrefetch(5),
```

### 6. Health Checks
```go
func (s *WebhookServer) healthCheck(w http.ResponseWriter, r *http.Request) {
    // Check RabbitMQ connection
    if s.publisher == nil {
        http.Error(w, "Publisher not available", http.StatusServiceUnavailable)
        return
    }
    
    // Check database connection
    if err := s.db.Ping(); err != nil {
        http.Error(w, "Database not available", http.StatusServiceUnavailable)
        return
    }
    
    w.WriteHeader(http.StatusOK)
    json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}
```

## Testing

### 1. Send Test Webhook
```bash
curl -X POST http://localhost:8080/webhook \
  -H "Content-Type: application/json" \
  -d '{
    "id": "test-webhook-001",
    "source": "github",
    "event_type": "push",
    "data": {
      "repository": "my-repo",
      "branch": "main",
      "commit": "abc123"
    }
  }'
```

### 2. Check Database
```sql
SELECT * FROM webhooks ORDER BY created_at DESC LIMIT 10;
```

### 3. Monitor RabbitMQ
Visit http://localhost:15672 (guest/guest) to monitor:
- Queue depth
- Message rates
- Consumer status

## Performance Tuning

### 1. Consumer Scaling
```go
// Increase concurrent processing
rabbitmq.WithConsumerOptionsConcurrency(10),
rabbitmq.WithConsumerOptionsQOSPrefetch(20),
```

### 2. Batch Processing
```go
// Process messages in batches for better database performance
func (p *WebhookProcessor) processBatch(deliveries []rabbitmq.Delivery) {
    tx, err := p.db.Begin()
    if err != nil {
        return
    }
    
    for _, d := range deliveries {
        // Process each delivery in transaction
    }
    
    tx.Commit()
}
```

### 3. Database Optimization
```sql
-- Add indexes for common queries
CREATE INDEX CONCURRENTLY idx_webhooks_source_event 
ON webhooks(source, event_type);

-- Partition large tables by date
CREATE TABLE webhooks_2024_01 PARTITION OF webhooks 
FOR VALUES FROM ('2024-01-01') TO ('2024-02-01');
```

This pattern provides a robust, scalable foundation for processing webhooks asynchronously while maintaining data consistency and reliability.