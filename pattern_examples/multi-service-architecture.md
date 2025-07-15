# Multi-Service Architecture with go-rabbitmq

This guide demonstrates how to build scalable microservices architectures using go-rabbitmq, with publishers in APIs and consumers in background workers.

## Table of Contents

1. [Architecture Overview](#architecture-overview)
2. [Communication Patterns](#communication-patterns)
3. [API Gateway Publishing Pattern](#api-gateway-publishing-pattern)
4. [Worker Service Consumption Patterns](#worker-service-consumption-patterns)
5. [Service Discovery and Routing](#service-discovery-and-routing)
6. [Complete Examples](#complete-examples)
7. [Scaling Considerations](#scaling-considerations)
8. [Docker Compose Setup](#docker-compose-setup)

## Architecture Overview

In a microservices architecture, RabbitMQ serves as the messaging backbone, enabling:

- **Asynchronous Communication**: APIs publish events without waiting for processing
- **Service Decoupling**: Services communicate through messages, not direct calls
- **Scalability**: Workers can be scaled independently based on queue depth
- **Resilience**: Messages persist during service outages

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   API       │     │   API       │     │   API       │
│  Gateway    │     │  Service    │     │  Service    │
│             │     │   (Auth)    │     │  (Orders)   │
└──────┬──────┘     └──────┬──────┘     └──────┬──────┘
       │                   │                   │
       └───────────────────┴───────────────────┘
                          │
                    ┌─────▼─────┐
                    │ RabbitMQ  │
                    │  Broker   │
                    └─────┬─────┘
                          │
       ┌──────────────────┼──────────────────┐
       │                  │                  │
┌──────▼──────┐    ┌──────▼──────┐   ┌──────▼──────┐
│   Email     │    │  Audit      │   │  Analytics  │
│   Worker    │    │  Worker     │   │   Worker    │
└─────────────┘    └─────────────┘   └─────────────┘
```

## Communication Patterns

### 1. Event-Driven Pattern

Services publish events when state changes occur:

```go
// Order service publishes order events
type OrderEvent struct {
    EventType string    `json:"event_type"` // "order.created", "order.shipped"
    OrderID   string    `json:"order_id"`
    UserID    string    `json:"user_id"`
    Timestamp time.Time `json:"timestamp"`
    Data      interface{} `json:"data"`
}
```

### 2. Command Pattern

APIs send commands to workers for processing:

```go
// API sends command to worker
type ProcessImageCommand struct {
    CommandID string `json:"command_id"`
    ImageURL  string `json:"image_url"`
    Operations []string `json:"operations"`
}
```

### 3. Request-Reply Pattern

Synchronous communication using correlation IDs:

```go
// Request with correlation ID
type CalculationRequest struct {
    CorrelationID string `json:"correlation_id"`
    ReplyTo       string `json:"reply_to"`
    Expression    string `json:"expression"`
}
```

## API Gateway Publishing Pattern

### Publisher Service Base

Create a reusable publisher service for all APIs:

```go
// internal/messaging/publisher.go
package messaging

import (
    "context"
    "encoding/json"
    "fmt"
    "time"
    
    rabbitmq "github.com/wagslane/go-rabbitmq"
)

type PublisherService struct {
    conn      *rabbitmq.Conn
    publishers map[string]*rabbitmq.Publisher
}

func NewPublisherService(amqpURL string) (*PublisherService, error) {
    conn, err := rabbitmq.NewConn(
        amqpURL,
        rabbitmq.WithConnectionOptionsLogging,
        rabbitmq.WithConnectionOptionsReconnectInterval(5*time.Second),
        rabbitmq.WithConnectionOptionsConnectionName("api-publisher"),
    )
    if err != nil {
        return nil, err
    }
    
    return &PublisherService{
        conn:       conn,
        publishers: make(map[string]*rabbitmq.Publisher),
    }, nil
}

func (ps *PublisherService) GetPublisher(exchange string) (*rabbitmq.Publisher, error) {
    if pub, exists := ps.publishers[exchange]; exists {
        return pub, nil
    }
    
    pub, err := rabbitmq.NewPublisher(
        ps.conn,
        rabbitmq.WithPublisherOptionsLogging,
        rabbitmq.WithPublisherOptionsExchangeName(exchange),
        rabbitmq.WithPublisherOptionsExchangeDeclare,
        rabbitmq.WithPublisherOptionsExchangeKind("topic"),
        rabbitmq.WithPublisherOptionsExchangeDurable,
    )
    if err != nil {
        return nil, err
    }
    
    // Set up confirmations
    pub.NotifyPublish(func(c rabbitmq.Confirmation) {
        if !c.Ack {
            // Log failed publication
            fmt.Printf("Message not confirmed: tag=%d\n", c.DeliveryTag)
        }
    })
    
    ps.publishers[exchange] = pub
    return pub, nil
}

func (ps *PublisherService) PublishEvent(ctx context.Context, exchange, routingKey string, event interface{}) error {
    publisher, err := ps.GetPublisher(exchange)
    if err != nil {
        return err
    }
    
    data, err := json.Marshal(event)
    if err != nil {
        return err
    }
    
    return publisher.PublishWithContext(
        ctx,
        data,
        []string{routingKey},
        rabbitmq.WithPublishOptionsContentType("application/json"),
        rabbitmq.WithPublishOptionsPersistentDelivery,
        rabbitmq.WithPublishOptionsExchange(exchange),
    )
}

func (ps *PublisherService) Close() {
    for _, pub := range ps.publishers {
        pub.Close()
    }
    ps.conn.Close()
}
```

### REST API Integration

Integrate the publisher into your REST APIs:

```go
// cmd/api/main.go
package main

import (
    "context"
    "encoding/json"
    "net/http"
    "time"
    
    "github.com/gorilla/mux"
    "yourproject/internal/messaging"
)

type OrderAPI struct {
    publisher *messaging.PublisherService
}

type OrderCreatedEvent struct {
    EventID   string    `json:"event_id"`
    OrderID   string    `json:"order_id"`
    UserID    string    `json:"user_id"`
    Amount    float64   `json:"amount"`
    Timestamp time.Time `json:"timestamp"`
}

func (api *OrderAPI) CreateOrder(w http.ResponseWriter, r *http.Request) {
    var order struct {
        UserID string  `json:"user_id"`
        Items  []string `json:"items"`
        Amount float64 `json:"amount"`
    }
    
    if err := json.NewDecoder(r.Body).Decode(&order); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    
    // Create order in database
    orderID := generateOrderID()
    // ... save to database ...
    
    // Publish event
    event := OrderCreatedEvent{
        EventID:   generateEventID(),
        OrderID:   orderID,
        UserID:    order.UserID,
        Amount:    order.Amount,
        Timestamp: time.Now(),
    }
    
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    if err := api.publisher.PublishEvent(ctx, "orders", "order.created", event); err != nil {
        // Log error but don't fail the request
        // Consider using outbox pattern for guaranteed delivery
        log.Printf("Failed to publish event: %v", err)
    }
    
    // Return success response
    w.WriteHeader(http.StatusCreated)
    json.NewEncoder(w).Encode(map[string]string{
        "order_id": orderID,
        "status": "created",
    })
}

func main() {
    // Initialize publisher
    publisher, err := messaging.NewPublisherService("amqp://guest:guest@rabbitmq:5672")
    if err != nil {
        log.Fatal("Failed to create publisher:", err)
    }
    defer publisher.Close()
    
    api := &OrderAPI{publisher: publisher}
    
    // Setup routes
    r := mux.NewRouter()
    r.HandleFunc("/orders", api.CreateOrder).Methods("POST")
    
    log.Println("Order API listening on :8080")
    http.ListenAndServe(":8080", r)
}
```

## Worker Service Consumption Patterns

### Consumer Service Base

Create a reusable consumer framework:

```go
// internal/worker/base.go
package worker

import (
    "context"
    "encoding/json"
    "fmt"
    "time"
    
    rabbitmq "github.com/wagslane/go-rabbitmq"
)

type Handler func(ctx context.Context, delivery rabbitmq.Delivery) error

type WorkerConfig struct {
    Exchange      string
    Queue         string
    RoutingKeys   []string
    Concurrency   int
    PrefetchCount int
    ConsumerName  string
}

type Worker struct {
    conn     *rabbitmq.Conn
    consumer *rabbitmq.Consumer
    handler  Handler
    config   WorkerConfig
}

func NewWorker(amqpURL string, config WorkerConfig, handler Handler) (*Worker, error) {
    conn, err := rabbitmq.NewConn(
        amqpURL,
        rabbitmq.WithConnectionOptionsLogging,
        rabbitmq.WithConnectionOptionsReconnectInterval(5*time.Second),
        rabbitmq.WithConnectionOptionsConnectionName(config.ConsumerName),
    )
    if err != nil {
        return nil, err
    }
    
    // Build consumer options
    opts := []func(*rabbitmq.ConsumerOptions){
        rabbitmq.WithConsumerOptionsConcurrency(config.Concurrency),
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsQuorumQueue,
        rabbitmq.WithConsumerOptionsConsumerName(config.ConsumerName),
        rabbitmq.WithConsumerOptionsExchangeName(config.Exchange),
        rabbitmq.WithConsumerOptionsExchangeKind("topic"),
        rabbitmq.WithConsumerOptionsExchangeDurable,
        rabbitmq.WithConsumerOptionsExchangeDeclare,
    }
    
    // Add routing keys
    for _, key := range config.RoutingKeys {
        opts = append(opts, rabbitmq.WithConsumerOptionsRoutingKey(key))
    }
    
    if config.PrefetchCount > 0 {
        opts = append(opts, rabbitmq.WithConsumerOptionsQOSPrefetch(config.PrefetchCount))
    }
    
    consumer, err := rabbitmq.NewConsumer(conn, config.Queue, opts...)
    if err != nil {
        conn.Close()
        return nil, err
    }
    
    return &Worker{
        conn:     conn,
        consumer: consumer,
        handler:  handler,
        config:   config,
    }, nil
}

func (w *Worker) Start(ctx context.Context) error {
    return w.consumer.Run(func(d rabbitmq.Delivery) rabbitmq.Action {
        // Create a context with timeout for each message
        msgCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
        defer cancel()
        
        // Process message
        if err := w.handler(msgCtx, d); err != nil {
            // Log error
            fmt.Printf("Error processing message: %v\n", err)
            
            // Decide whether to requeue based on error type
            if isRetryableError(err) {
                return rabbitmq.NackRequeue
            }
            return rabbitmq.NackDiscard
        }
        
        return rabbitmq.Ack
    })
}

func (w *Worker) Stop() {
    w.consumer.Close()
    w.conn.Close()
}

func isRetryableError(err error) bool {
    // Implement logic to determine if error is retryable
    // e.g., network errors, temporary database issues
    return false
}
```

### Email Worker Example

```go
// cmd/email-worker/main.go
package main

import (
    "context"
    "encoding/json"
    "log"
    "os"
    "os/signal"
    "syscall"
    
    rabbitmq "github.com/wagslane/go-rabbitmq"
    "yourproject/internal/worker"
)

type EmailService struct {
    // Email client configuration
}

func (s *EmailService) ProcessOrderCreated(ctx context.Context, d rabbitmq.Delivery) error {
    var event struct {
        OrderID string `json:"order_id"`
        UserID  string `json:"user_id"`
        Amount  float64 `json:"amount"`
    }
    
    if err := json.Unmarshal(d.Body, &event); err != nil {
        return err
    }
    
    log.Printf("Sending order confirmation email for order %s to user %s", 
        event.OrderID, event.UserID)
    
    // Send email logic here
    // ...
    
    return nil
}

func main() {
    emailService := &EmailService{}
    
    // Create worker for order events
    orderWorker, err := worker.NewWorker(
        "amqp://guest:guest@rabbitmq:5672",
        worker.WorkerConfig{
            Exchange:      "orders",
            Queue:         "email-order-notifications",
            RoutingKeys:   []string{"order.created", "order.shipped"},
            Concurrency:   5,
            PrefetchCount: 10,
            ConsumerName:  "email-worker",
        },
        emailService.ProcessOrderCreated,
    )
    if err != nil {
        log.Fatal("Failed to create worker:", err)
    }
    
    // Handle shutdown
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    
    // Start worker
    go func() {
        if err := orderWorker.Start(ctx); err != nil {
            log.Printf("Worker error: %v", err)
        }
    }()
    
    log.Println("Email worker started. Press Ctrl+C to stop.")
    <-sigChan
    
    log.Println("Shutting down...")
    orderWorker.Stop()
}
```

## Service Discovery and Routing

### Exchange Topology

Design your exchange topology for service isolation:

```go
// internal/messaging/topology.go
package messaging

const (
    // Service-specific exchanges
    OrdersExchange    = "orders"
    UsersExchange     = "users"
    PaymentsExchange  = "payments"
    
    // Cross-service exchange
    SystemExchange    = "system"
)

// Routing key patterns
const (
    // Order events
    OrderCreated   = "order.created"
    OrderShipped   = "order.shipped"
    OrderCancelled = "order.cancelled"
    
    // User events
    UserRegistered = "user.registered"
    UserUpdated    = "user.updated"
    
    // System events
    HealthCheck    = "system.health"
    ConfigUpdated  = "system.config.updated"
)
```

### Service Registry Pattern

Implement service discovery for dynamic routing:

```go
// internal/registry/service.go
package registry

type ServiceInfo struct {
    Name        string
    Exchange    string
    RoutingKeys []string
    Queues      []QueueInfo
}

type QueueInfo struct {
    Name        string
    RoutingKeys []string
    Durable     bool
    Exclusive   bool
}

var ServiceRegistry = map[string]ServiceInfo{
    "email-service": {
        Name:     "email-service",
        Exchange: "notifications",
        Queues: []QueueInfo{
            {
                Name:        "email-orders",
                RoutingKeys: []string{"order.*"},
                Durable:     true,
            },
            {
                Name:        "email-users",
                RoutingKeys: []string{"user.*"},
                Durable:     true,
            },
        },
    },
    "analytics-service": {
        Name:     "analytics-service",
        Exchange: "analytics",
        Queues: []QueueInfo{
            {
                Name:        "analytics-events",
                RoutingKeys: []string{"#"}, // Receive all events
                Durable:     true,
            },
        },
    },
}
```

## Complete Examples

### 1. REST API Publishing Events

Full API service with multiple event types:

```go
// cmd/user-api/main.go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "time"
    
    "github.com/google/uuid"
    "github.com/gorilla/mux"
    "yourproject/internal/messaging"
)

type UserAPI struct {
    publisher *messaging.PublisherService
}

// Event types
type UserEvent struct {
    EventID   string                 `json:"event_id"`
    EventType string                 `json:"event_type"`
    UserID    string                 `json:"user_id"`
    Timestamp time.Time              `json:"timestamp"`
    Data      map[string]interface{} `json:"data"`
}

func (api *UserAPI) RegisterUser(w http.ResponseWriter, r *http.Request) {
    var req struct {
        Email    string `json:"email"`
        Name     string `json:"name"`
        Password string `json:"password"`
    }
    
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    
    // Create user
    userID := uuid.New().String()
    // ... save to database ...
    
    // Publish registration event
    event := UserEvent{
        EventID:   uuid.New().String(),
        EventType: "user.registered",
        UserID:    userID,
        Timestamp: time.Now(),
        Data: map[string]interface{}{
            "email": req.Email,
            "name":  req.Name,
        },
    }
    
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    if err := api.publisher.PublishEvent(ctx, "users", "user.registered", event); err != nil {
        log.Printf("Failed to publish user.registered event: %v", err)
    }
    
    // Also publish to analytics exchange
    if err := api.publisher.PublishEvent(ctx, "analytics", "user.registered", event); err != nil {
        log.Printf("Failed to publish to analytics: %v", err)
    }
    
    w.WriteHeader(http.StatusCreated)
    json.NewEncoder(w).Encode(map[string]string{
        "user_id": userID,
        "status":  "created",
    })
}

func (api *UserAPI) UpdateUser(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    userID := vars["id"]
    
    var updates map[string]interface{}
    if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    
    // Update user in database
    // ...
    
    // Publish update event
    event := UserEvent{
        EventID:   uuid.New().String(),
        EventType: "user.updated",
        UserID:    userID,
        Timestamp: time.Now(),
        Data:      updates,
    }
    
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    if err := api.publisher.PublishEvent(ctx, "users", "user.updated", event); err != nil {
        log.Printf("Failed to publish user.updated event: %v", err)
    }
    
    w.WriteHeader(http.StatusOK)
    json.NewEncoder(w).Encode(map[string]string{
        "status": "updated",
    })
}

func main() {
    // Initialize publisher with retry
    var publisher *messaging.PublisherService
    var err error
    
    for i := 0; i < 5; i++ {
        publisher, err = messaging.NewPublisherService("amqp://guest:guest@rabbitmq:5672")
        if err == nil {
            break
        }
        log.Printf("Failed to connect to RabbitMQ, retrying in 5s... (%d/5)", i+1)
        time.Sleep(5 * time.Second)
    }
    
    if err != nil {
        log.Fatal("Failed to create publisher after retries:", err)
    }
    defer publisher.Close()
    
    api := &UserAPI{publisher: publisher}
    
    // Setup routes
    r := mux.NewRouter()
    r.HandleFunc("/users", api.RegisterUser).Methods("POST")
    r.HandleFunc("/users/{id}", api.UpdateUser).Methods("PUT")
    
    // Health check
    r.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
    }).Methods("GET")
    
    log.Println("User API listening on :8081")
    if err := http.ListenAndServe(":8081", r); err != nil {
        log.Fatal("Server error:", err)
    }
}
```

### 2. Multi-Consumer Worker Service

Worker that processes events from multiple services:

```go
// cmd/audit-worker/main.go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "os"
    "os/signal"
    "sync"
    "syscall"
    "time"
    
    rabbitmq "github.com/wagslane/go-rabbitmq"
    "yourproject/internal/worker"
)

type AuditService struct {
    // Database connection for audit logs
}

type AuditEntry struct {
    ID        string    `json:"id"`
    EventType string    `json:"event_type"`
    UserID    string    `json:"user_id"`
    Timestamp time.Time `json:"timestamp"`
    Data      json.RawMessage `json:"data"`
}

func (s *AuditService) ProcessEvent(ctx context.Context, d rabbitmq.Delivery) error {
    // Parse event header
    eventType := d.Headers["event_type"].(string)
    
    entry := AuditEntry{
        ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
        EventType: eventType,
        Timestamp: time.Now(),
        Data:      d.Body,
    }
    
    // Extract user ID if present
    var event map[string]interface{}
    if err := json.Unmarshal(d.Body, &event); err == nil {
        if userID, ok := event["user_id"].(string); ok {
            entry.UserID = userID
        }
    }
    
    log.Printf("Auditing event: %s for user: %s", eventType, entry.UserID)
    
    // Save to audit database
    // ...
    
    return nil
}

func main() {
    auditService := &AuditService{}
    
    // Workers for different exchanges
    workers := []struct {
        name     string
        exchange string
        queue    string
        keys     []string
    }{
        {
            name:     "audit-orders",
            exchange: "orders",
            queue:    "audit-order-events",
            keys:     []string{"order.*"},
        },
        {
            name:     "audit-users",
            exchange: "users",
            queue:    "audit-user-events",
            keys:     []string{"user.*"},
        },
        {
            name:     "audit-payments",
            exchange: "payments",
            queue:    "audit-payment-events",
            keys:     []string{"payment.*"},
        },
    }
    
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    
    var wg sync.WaitGroup
    
    // Start workers
    for _, w := range workers {
        wg.Add(1)
        go func(workerConfig struct {
            name     string
            exchange string
            queue    string
            keys     []string
        }) {
            defer wg.Done()
            
            worker, err := worker.NewWorker(
                "amqp://guest:guest@rabbitmq:5672",
                worker.WorkerConfig{
                    Exchange:      workerConfig.exchange,
                    Queue:         workerConfig.queue,
                    RoutingKeys:   workerConfig.keys,
                    Concurrency:   2,
                    PrefetchCount: 5,
                    ConsumerName:  workerConfig.name,
                },
                auditService.ProcessEvent,
            )
            if err != nil {
                log.Printf("Failed to create worker %s: %v", workerConfig.name, err)
                return
            }
            defer worker.Stop()
            
            log.Printf("Starting worker: %s", workerConfig.name)
            if err := worker.Start(ctx); err != nil {
                log.Printf("Worker %s error: %v", workerConfig.name, err)
            }
        }(w)
    }
    
    // Handle shutdown
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    
    log.Println("Audit worker started. Press Ctrl+C to stop.")
    <-sigChan
    
    log.Println("Shutting down...")
    cancel()
    wg.Wait()
}
```

### 3. Service-to-Service Communication

Direct service communication using RPC pattern:

```go
// internal/rpc/client.go
package rpc

import (
    "context"
    "encoding/json"
    "fmt"
    "sync"
    "time"
    
    "github.com/google/uuid"
    rabbitmq "github.com/wagslane/go-rabbitmq"
)

type RPCClient struct {
    conn         *rabbitmq.Conn
    publisher    *rabbitmq.Publisher
    consumer     *rabbitmq.Consumer
    replyQueue   string
    pendingCalls map[string]chan *RPCResponse
    mu           sync.Mutex
}

type RPCRequest struct {
    ID      string          `json:"id"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params"`
    ReplyTo string          `json:"reply_to"`
}

type RPCResponse struct {
    ID     string          `json:"id"`
    Result json.RawMessage `json:"result,omitempty"`
    Error  string          `json:"error,omitempty"`
}

func NewRPCClient(amqpURL string) (*RPCClient, error) {
    conn, err := rabbitmq.NewConn(amqpURL)
    if err != nil {
        return nil, err
    }
    
    // Create exclusive reply queue
    replyQueue := fmt.Sprintf("rpc.reply.%s", uuid.New().String())
    
    publisher, err := rabbitmq.NewPublisher(
        conn,
        rabbitmq.WithPublisherOptionsExchangeName("rpc"),
        rabbitmq.WithPublisherOptionsExchangeDeclare,
        rabbitmq.WithPublisherOptionsExchangeKind("direct"),
    )
    if err != nil {
        conn.Close()
        return nil, err
    }
    
    consumer, err := rabbitmq.NewConsumer(
        conn,
        replyQueue,
        rabbitmq.WithConsumerOptionsQueueExclusive,
        rabbitmq.WithConsumerOptionsQueueAutoDelete,
    )
    if err != nil {
        publisher.Close()
        conn.Close()
        return nil, err
    }
    
    client := &RPCClient{
        conn:         conn,
        publisher:    publisher,
        consumer:     consumer,
        replyQueue:   replyQueue,
        pendingCalls: make(map[string]chan *RPCResponse),
    }
    
    // Start consuming replies
    go client.consumeReplies()
    
    return client, nil
}

func (c *RPCClient) Call(ctx context.Context, service, method string, params interface{}) (*RPCResponse, error) {
    // Create request
    reqID := uuid.New().String()
    paramsJSON, err := json.Marshal(params)
    if err != nil {
        return nil, err
    }
    
    req := RPCRequest{
        ID:      reqID,
        Method:  method,
        Params:  paramsJSON,
        ReplyTo: c.replyQueue,
    }
    
    // Create response channel
    respChan := make(chan *RPCResponse, 1)
    c.mu.Lock()
    c.pendingCalls[reqID] = respChan
    c.mu.Unlock()
    
    // Send request
    reqData, err := json.Marshal(req)
    if err != nil {
        return nil, err
    }
    
    if err := c.publisher.PublishWithContext(
        ctx,
        reqData,
        []string{service},
        rabbitmq.WithPublishOptionsContentType("application/json"),
        rabbitmq.WithPublishOptionsExchange("rpc"),
    ); err != nil {
        return nil, err
    }
    
    // Wait for response
    select {
    case resp := <-respChan:
        return resp, nil
    case <-ctx.Done():
        c.mu.Lock()
        delete(c.pendingCalls, reqID)
        c.mu.Unlock()
        return nil, ctx.Err()
    }
}

func (c *RPCClient) consumeReplies() {
    c.consumer.Run(func(d rabbitmq.Delivery) rabbitmq.Action {
        var resp RPCResponse
        if err := json.Unmarshal(d.Body, &resp); err != nil {
            return rabbitmq.NackDiscard
        }
        
        c.mu.Lock()
        if respChan, ok := c.pendingCalls[resp.ID]; ok {
            delete(c.pendingCalls, resp.ID)
            c.mu.Unlock()
            respChan <- &resp
        } else {
            c.mu.Unlock()
        }
        
        return rabbitmq.Ack
    })
}
```

## Scaling Considerations

### 1. Connection Management

Use connection pooling for high-throughput services:

```go
// internal/messaging/pool.go
package messaging

import (
    "sync"
    "time"
    
    rabbitmq "github.com/wagslane/go-rabbitmq"
)

type ConnectionPool struct {
    url         string
    connections []*rabbitmq.Conn
    mu          sync.Mutex
    maxSize     int
    current     int
}

func NewConnectionPool(url string, size int) *ConnectionPool {
    return &ConnectionPool{
        url:         url,
        connections: make([]*rabbitmq.Conn, 0, size),
        maxSize:     size,
    }
}

func (p *ConnectionPool) Get() (*rabbitmq.Conn, error) {
    p.mu.Lock()
    defer p.mu.Unlock()
    
    // Return existing connection if available
    if len(p.connections) > 0 {
        conn := p.connections[len(p.connections)-1]
        p.connections = p.connections[:len(p.connections)-1]
        return conn, nil
    }
    
    // Create new connection if under limit
    if p.current < p.maxSize {
        conn, err := rabbitmq.NewConn(
            p.url,
            rabbitmq.WithConnectionOptionsLogging,
            rabbitmq.WithConnectionOptionsReconnectInterval(5*time.Second),
        )
        if err != nil {
            return nil, err
        }
        p.current++
        return conn, nil
    }
    
    return nil, fmt.Errorf("connection pool exhausted")
}

func (p *ConnectionPool) Put(conn *rabbitmq.Conn) {
    p.mu.Lock()
    defer p.mu.Unlock()
    
    if len(p.connections) < p.maxSize {
        p.connections = append(p.connections, conn)
    } else {
        conn.Close()
        p.current--
    }
}
```

### 2. Worker Scaling Strategy

Implement auto-scaling based on queue depth:

```go
// internal/autoscale/monitor.go
package autoscale

import (
    "context"
    "fmt"
    "log"
    "time"
)

type QueueMonitor struct {
    queueName   string
    minWorkers  int
    maxWorkers  int
    targetDepth int
}

func (m *QueueMonitor) GetDesiredWorkerCount(ctx context.Context) (int, error) {
    // Get queue depth from RabbitMQ management API
    depth, err := m.getQueueDepth(ctx)
    if err != nil {
        return m.minWorkers, err
    }
    
    // Calculate desired workers
    desired := depth / m.targetDepth
    if desired < m.minWorkers {
        desired = m.minWorkers
    } else if desired > m.maxWorkers {
        desired = m.maxWorkers
    }
    
    return desired, nil
}

func (m *QueueMonitor) getQueueDepth(ctx context.Context) (int, error) {
    // Implementation to query RabbitMQ management API
    // GET http://rabbitmq:15672/api/queues/{vhost}/{queue}
    return 0, nil
}
```

### 3. Circuit Breaker Pattern

Protect downstream services:

```go
// internal/resilience/circuitbreaker.go
package resilience

import (
    "errors"
    "sync"
    "time"
)

type CircuitBreaker struct {
    maxFailures  int
    resetTimeout time.Duration
    
    mu           sync.Mutex
    failures     int
    lastFailTime time.Time
    state        string // "closed", "open", "half-open"
}

func NewCircuitBreaker(maxFailures int, resetTimeout time.Duration) *CircuitBreaker {
    return &CircuitBreaker{
        maxFailures:  maxFailures,
        resetTimeout: resetTimeout,
        state:        "closed",
    }
}

func (cb *CircuitBreaker) Call(fn func() error) error {
    cb.mu.Lock()
    defer cb.mu.Unlock()
    
    // Check if circuit should be reset
    if cb.state == "open" && time.Since(cb.lastFailTime) > cb.resetTimeout {
        cb.state = "half-open"
        cb.failures = 0
    }
    
    if cb.state == "open" {
        return errors.New("circuit breaker is open")
    }
    
    err := fn()
    if err != nil {
        cb.failures++
        cb.lastFailTime = time.Now()
        
        if cb.failures >= cb.maxFailures {
            cb.state = "open"
        }
        return err
    }
    
    // Success - reset failures
    if cb.state == "half-open" {
        cb.state = "closed"
    }
    cb.failures = 0
    return nil
}
```

## Docker Compose Setup

Complete multi-service setup with Docker Compose:

```yaml
# docker-compose.yml
version: '3.8'

services:
  rabbitmq:
    image: rabbitmq:3.12-management-alpine
    hostname: rabbitmq
    ports:
      - "5672:5672"
      - "15672:15672"
    environment:
      RABBITMQ_DEFAULT_USER: admin
      RABBITMQ_DEFAULT_PASS: admin123
    volumes:
      - rabbitmq_data:/var/lib/rabbitmq
      - ./rabbitmq.conf:/etc/rabbitmq/rabbitmq.conf:ro
    healthcheck:
      test: ["CMD", "rabbitmq-diagnostics", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5

  # API Services
  order-api:
    build:
      context: .
      dockerfile: ./cmd/order-api/Dockerfile
    ports:
      - "8080:8080"
    environment:
      RABBITMQ_URL: amqp://admin:admin123@rabbitmq:5672
      DB_HOST: postgres
      DB_NAME: orders
    depends_on:
      rabbitmq:
        condition: service_healthy
      postgres:
        condition: service_healthy
    restart: unless-stopped

  user-api:
    build:
      context: .
      dockerfile: ./cmd/user-api/Dockerfile
    ports:
      - "8081:8081"
    environment:
      RABBITMQ_URL: amqp://admin:admin123@rabbitmq:5672
      DB_HOST: postgres
      DB_NAME: users
    depends_on:
      rabbitmq:
        condition: service_healthy
      postgres:
        condition: service_healthy
    restart: unless-stopped

  # Worker Services
  email-worker:
    build:
      context: .
      dockerfile: ./cmd/email-worker/Dockerfile
    environment:
      RABBITMQ_URL: amqp://admin:admin123@rabbitmq:5672
      SMTP_HOST: mailhog
      SMTP_PORT: 1025
    depends_on:
      rabbitmq:
        condition: service_healthy
    deploy:
      replicas: 2
    restart: unless-stopped

  audit-worker:
    build:
      context: .
      dockerfile: ./cmd/audit-worker/Dockerfile
    environment:
      RABBITMQ_URL: amqp://admin:admin123@rabbitmq:5672
      DB_HOST: postgres
      DB_NAME: audit
    depends_on:
      rabbitmq:
        condition: service_healthy
      postgres:
        condition: service_healthy
    restart: unless-stopped

  analytics-worker:
    build:
      context: .
      dockerfile: ./cmd/analytics-worker/Dockerfile
    environment:
      RABBITMQ_URL: amqp://admin:admin123@rabbitmq:5672
      CLICKHOUSE_HOST: clickhouse
    depends_on:
      rabbitmq:
        condition: service_healthy
      clickhouse:
        condition: service_healthy
    deploy:
      replicas: 3
    restart: unless-stopped

  # Supporting Services
  postgres:
    image: postgres:15-alpine
    environment:
      POSTGRES_USER: admin
      POSTGRES_PASSWORD: admin123
      POSTGRES_MULTIPLE_DATABASES: orders,users,audit
    volumes:
      - postgres_data:/var/lib/postgresql/data
      - ./scripts/init-db.sh:/docker-entrypoint-initdb.d/init-db.sh:ro
    ports:
      - "5432:5432"
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U admin"]
      interval: 10s
      timeout: 5s
      retries: 5

  clickhouse:
    image: clickhouse/clickhouse-server:23-alpine
    ports:
      - "8123:8123"
      - "9000:9000"
    volumes:
      - clickhouse_data:/var/lib/clickhouse
    healthcheck:
      test: ["CMD", "clickhouse-client", "--query", "SELECT 1"]
      interval: 10s
      timeout: 5s
      retries: 5

  mailhog:
    image: mailhog/mailhog
    ports:
      - "1025:1025"
      - "8025:8025"

  # Monitoring
  prometheus:
    image: prom/prometheus
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml:ro
      - prometheus_data:/prometheus
    ports:
      - "9090:9090"
    command:
      - '--config.file=/etc/prometheus/prometheus.yml'
      - '--storage.tsdb.path=/prometheus'

  grafana:
    image: grafana/grafana
    ports:
      - "3000:3000"
    volumes:
      - grafana_data:/var/lib/grafana
      - ./grafana/dashboards:/etc/grafana/provisioning/dashboards:ro
      - ./grafana/datasources:/etc/grafana/provisioning/datasources:ro
    environment:
      GF_SECURITY_ADMIN_PASSWORD: admin123

volumes:
  rabbitmq_data:
  postgres_data:
  clickhouse_data:
  prometheus_data:
  grafana_data:
```

### RabbitMQ Configuration

```conf
# rabbitmq.conf
# Performance tuning
vm_memory_high_watermark.relative = 0.6
disk_free_limit.absolute = 2GB

# Connection limits
channel_max = 256
connection_max = 4096

# Queue defaults
queue_master_locator = min-masters

# Management plugin
management.load_definitions = /etc/rabbitmq/definitions.json
```

### Service Dockerfile Example

```dockerfile
# cmd/order-api/Dockerfile
FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o order-api ./cmd/order-api

FROM alpine:3.18
RUN apk --no-cache add ca-certificates

WORKDIR /app
COPY --from=builder /app/order-api .

EXPOSE 8080
CMD ["./order-api"]
```

### Monitoring Configuration

```yaml
# prometheus.yml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: 'rabbitmq'
    static_configs:
      - targets: ['rabbitmq:15692']

  - job_name: 'order-api'
    static_configs:
      - targets: ['order-api:8080']
    metrics_path: '/metrics'

  - job_name: 'user-api'
    static_configs:
      - targets: ['user-api:8081']
    metrics_path: '/metrics'
```

## Best Practices Summary

1. **Connection Management**
   - Use connection pooling for high-throughput services
   - Implement proper reconnection logic
   - Monitor connection health

2. **Message Design**
   - Use consistent event schemas
   - Include correlation IDs for tracing
   - Version your message formats

3. **Error Handling**
   - Implement retry logic with exponential backoff
   - Use dead letter queues for failed messages
   - Monitor and alert on error rates

4. **Scaling**
   - Scale workers based on queue depth
   - Use prefetch counts to control load
   - Implement circuit breakers for downstream services

5. **Monitoring**
   - Track message rates and latencies
   - Monitor queue depths and consumer lag
   - Set up alerts for critical thresholds

This architecture provides a solid foundation for building scalable microservices with RabbitMQ, supporting both synchronous and asynchronous communication patterns while maintaining service isolation and resilience.