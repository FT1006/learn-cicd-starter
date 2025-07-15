# RabbitMQ Routing Strategies Guide

This guide compares the three main exchange types in RabbitMQ: Direct, Topic, and Fanout, providing practical examples and use cases for each.

## Table of Contents
- [Exchange Types Overview](#exchange-types-overview)
- [Direct Exchange](#direct-exchange)
- [Topic Exchange](#topic-exchange)
- [Fanout Exchange](#fanout-exchange)
- [Routing Key Strategies](#routing-key-strategies)
- [Performance Considerations](#performance-considerations)
- [Visual Diagrams](#visual-diagrams)

## Exchange Types Overview

### Direct Exchange
- **Routing**: Messages are routed to queues based on exact routing key match
- **Use Cases**: Point-to-point messaging, task distribution, specific message routing
- **Pattern**: One-to-one or one-to-many with specific keys

### Topic Exchange
- **Routing**: Messages are routed based on wildcard pattern matching
- **Use Cases**: Publish/subscribe with filtering, logging systems, event categorization
- **Pattern**: Hierarchical routing with `*` (one word) and `#` (zero or more words)

### Fanout Exchange
- **Routing**: Messages are broadcast to all bound queues (routing key ignored)
- **Use Cases**: Broadcasting, notifications, cache invalidation, real-time updates
- **Pattern**: One-to-many broadcast

## Direct Exchange

### When to Use Direct Exchange

- **Order Processing**: Route orders to specific handlers
- **Task Distribution**: Send tasks to specific workers
- **Service-to-Service**: Direct communication between services
- **Load Balancing**: Distribute work among multiple workers

### Complete Example: Order Processing System

```go
package main

import (
    "context"
    "encoding/json"
    "log"
    "time"
    
    rabbitmq "github.com/wagslane/go-rabbitmq"
)

type Order struct {
    ID       string `json:"id"`
    Type     string `json:"type"`
    Amount   float64 `json:"amount"`
    Priority string `json:"priority"`
}

// Publisher: Order Service
func setupOrderPublisher(conn *rabbitmq.Conn) (*rabbitmq.Publisher, error) {
    return rabbitmq.NewPublisher(
        conn,
        rabbitmq.WithPublisherOptionsLogging,
        rabbitmq.WithPublisherOptionsExchangeName("orders"),
        rabbitmq.WithPublisherOptionsExchangeKind("direct"),
        rabbitmq.WithPublisherOptionsExchangeDeclare,
    )
}

// Consumer: Payment Service
func setupPaymentConsumer(conn *rabbitmq.Conn) (*rabbitmq.Consumer, error) {
    return rabbitmq.NewConsumer(
        conn,
        "payment_queue",
        rabbitmq.WithConsumerOptionsRoutingKey("payment.process"),
        rabbitmq.WithConsumerOptionsExchangeName("orders"),
        rabbitmq.WithConsumerOptionsExchangeKind("direct"),
        rabbitmq.WithConsumerOptionsExchangeDeclare,
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsConcurrency(3),
    )
}

// Consumer: Inventory Service
func setupInventoryConsumer(conn *rabbitmq.Conn) (*rabbitmq.Consumer, error) {
    return rabbitmq.NewConsumer(
        conn,
        "inventory_queue",
        rabbitmq.WithConsumerOptionsRoutingKey("inventory.update"),
        rabbitmq.WithConsumerOptionsExchangeName("orders"),
        rabbitmq.WithConsumerOptionsExchangeKind("direct"),
        rabbitmq.WithConsumerOptionsExchangeDeclare,
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsConcurrency(2),
    )
}

// Consumer: High Priority Orders
func setupPriorityConsumer(conn *rabbitmq.Conn) (*rabbitmq.Consumer, error) {
    return rabbitmq.NewConsumer(
        conn,
        "priority_queue",
        rabbitmq.WithConsumerOptionsRoutingKey("order.priority.high"),
        rabbitmq.WithConsumerOptionsExchangeName("orders"),
        rabbitmq.WithConsumerOptionsExchangeKind("direct"),
        rabbitmq.WithConsumerOptionsExchangeDeclare,
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsConcurrency(5),
    )
}

func publishOrder(publisher *rabbitmq.Publisher, order Order) error {
    orderJSON, err := json.Marshal(order)
    if err != nil {
        return err
    }
    
    // Route based on order type and priority
    var routingKey string
    if order.Priority == "high" {
        routingKey = "order.priority.high"
    } else {
        switch order.Type {
        case "payment":
            routingKey = "payment.process"
        case "inventory":
            routingKey = "inventory.update"
        default:
            routingKey = "order.general"
        }
    }
    
    return publisher.PublishWithContext(
        context.Background(),
        orderJSON,
        []string{routingKey},
        rabbitmq.WithPublishOptionsContentType("application/json"),
        rabbitmq.WithPublishOptionsPersistentDelivery,
    )
}

func paymentHandler(d rabbitmq.Delivery) rabbitmq.Action {
    var order Order
    if err := json.Unmarshal(d.Body, &order); err != nil {
        log.Printf("Payment service: Failed to parse order: %v", err)
        return rabbitmq.NackRequeue
    }
    
    log.Printf("Payment service: Processing payment for order %s: $%.2f", 
        order.ID, order.Amount)
    
    // Simulate payment processing
    time.Sleep(100 * time.Millisecond)
    
    return rabbitmq.Ack
}

func inventoryHandler(d rabbitmq.Delivery) rabbitmq.Action {
    var order Order
    if err := json.Unmarshal(d.Body, &order); err != nil {
        log.Printf("Inventory service: Failed to parse order: %v", err)
        return rabbitmq.NackRequeue
    }
    
    log.Printf("Inventory service: Updating inventory for order %s", order.ID)
    
    // Simulate inventory update
    time.Sleep(50 * time.Millisecond)
    
    return rabbitmq.Ack
}

func priorityHandler(d rabbitmq.Delivery) rabbitmq.Action {
    var order Order
    if err := json.Unmarshal(d.Body, &order); err != nil {
        log.Printf("Priority service: Failed to parse order: %v", err)
        return rabbitmq.NackRequeue
    }
    
    log.Printf("Priority service: Fast-tracking high priority order %s", order.ID)
    
    // Simulate priority processing
    time.Sleep(25 * time.Millisecond)
    
    return rabbitmq.Ack
}

func main() {
    conn, err := rabbitmq.NewConn("amqp://guest:guest@localhost")
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()
    
    // Setup publisher
    publisher, err := setupOrderPublisher(conn)
    if err != nil {
        log.Fatal(err)
    }
    defer publisher.Close()
    
    // Setup consumers
    paymentConsumer, err := setupPaymentConsumer(conn)
    if err != nil {
        log.Fatal(err)
    }
    defer paymentConsumer.Close()
    
    inventoryConsumer, err := setupInventoryConsumer(conn)
    if err != nil {
        log.Fatal(err)
    }
    defer inventoryConsumer.Close()
    
    priorityConsumer, err := setupPriorityConsumer(conn)
    if err != nil {
        log.Fatal(err)
    }
    defer priorityConsumer.Close()
    
    // Start consumers
    go paymentConsumer.Run(paymentHandler)
    go inventoryConsumer.Run(inventoryHandler)
    go priorityConsumer.Run(priorityHandler)
    
    // Publish sample orders
    orders := []Order{
        {ID: "ord-001", Type: "payment", Amount: 99.99, Priority: "normal"},
        {ID: "ord-002", Type: "inventory", Amount: 49.99, Priority: "normal"},
        {ID: "ord-003", Type: "payment", Amount: 199.99, Priority: "high"},
        {ID: "ord-004", Type: "inventory", Amount: 29.99, Priority: "high"},
    }
    
    for _, order := range orders {
        if err := publishOrder(publisher, order); err != nil {
            log.Printf("Failed to publish order %s: %v", order.ID, err)
        }
        time.Sleep(100 * time.Millisecond)
    }
    
    // Keep running to process messages
    time.Sleep(5 * time.Second)
}
```

## Topic Exchange

### When to Use Topic Exchange

- **Log Aggregation**: Route logs based on severity and source
- **Event Sourcing**: Route events by domain and type
- **Microservices**: Route messages based on service patterns
- **Monitoring**: Route metrics and alerts based on patterns

### Complete Example: Distributed Logging System

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "time"
    
    rabbitmq "github.com/wagslane/go-rabbitmq"
)

type LogMessage struct {
    Level     string    `json:"level"`
    Service   string    `json:"service"`
    Message   string    `json:"message"`
    Timestamp time.Time `json:"timestamp"`
    RequestID string    `json:"request_id"`
}

// Publisher: Application Services
func setupLogPublisher(conn *rabbitmq.Conn) (*rabbitmq.Publisher, error) {
    return rabbitmq.NewPublisher(
        conn,
        rabbitmq.WithPublisherOptionsLogging,
        rabbitmq.WithPublisherOptionsExchangeName("logs"),
        rabbitmq.WithPublisherOptionsExchangeKind("topic"),
        rabbitmq.WithPublisherOptionsExchangeDeclare,
    )
}

// Consumer: Error Alerts (only ERROR level)
func setupErrorConsumer(conn *rabbitmq.Conn) (*rabbitmq.Consumer, error) {
    return rabbitmq.NewConsumer(
        conn,
        "error_alerts",
        rabbitmq.WithConsumerOptionsRoutingKey("*.error.*"),
        rabbitmq.WithConsumerOptionsExchangeName("logs"),
        rabbitmq.WithConsumerOptionsExchangeKind("topic"),
        rabbitmq.WithConsumerOptionsExchangeDeclare,
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsConcurrency(2),
    )
}

// Consumer: Payment Service Logs (all levels)
func setupPaymentLogsConsumer(conn *rabbitmq.Conn) (*rabbitmq.Consumer, error) {
    return rabbitmq.NewConsumer(
        conn,
        "payment_logs",
        rabbitmq.WithConsumerOptionsRoutingKey("payment.*.*"),
        rabbitmq.WithConsumerOptionsExchangeName("logs"),
        rabbitmq.WithConsumerOptionsExchangeKind("topic"),
        rabbitmq.WithConsumerOptionsExchangeDeclare,
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsConcurrency(1),
    )
}

// Consumer: All Application Logs
func setupAllLogsConsumer(conn *rabbitmq.Conn) (*rabbitmq.Consumer, error) {
    return rabbitmq.NewConsumer(
        conn,
        "all_logs",
        rabbitmq.WithConsumerOptionsRoutingKey("#"),
        rabbitmq.WithConsumerOptionsExchangeName("logs"),
        rabbitmq.WithConsumerOptionsExchangeKind("topic"),
        rabbitmq.WithConsumerOptionsExchangeDeclare,
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsConcurrency(1),
    )
}

// Consumer: Critical Issues (ERROR and WARN from specific services)
func setupCriticalConsumer(conn *rabbitmq.Conn) (*rabbitmq.Consumer, error) {
    consumer, err := rabbitmq.NewConsumer(
        conn,
        "critical_issues",
        rabbitmq.WithConsumerOptionsRoutingKey("payment.error.*"),
        rabbitmq.WithConsumerOptionsRoutingKey("payment.warn.*"),
        rabbitmq.WithConsumerOptionsRoutingKey("auth.error.*"),
        rabbitmq.WithConsumerOptionsRoutingKey("auth.warn.*"),
        rabbitmq.WithConsumerOptionsExchangeName("logs"),
        rabbitmq.WithConsumerOptionsExchangeKind("topic"),
        rabbitmq.WithConsumerOptionsExchangeDeclare,
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsConcurrency(3),
    )
    return consumer, err
}

func publishLog(publisher *rabbitmq.Publisher, logMsg LogMessage) error {
    logJSON, err := json.Marshal(logMsg)
    if err != nil {
        return err
    }
    
    // Topic pattern: {service}.{level}.{category}
    routingKey := fmt.Sprintf("%s.%s.app", logMsg.Service, logMsg.Level)
    
    return publisher.PublishWithContext(
        context.Background(),
        logJSON,
        []string{routingKey},
        rabbitmq.WithPublishOptionsContentType("application/json"),
        rabbitmq.WithPublishOptionsPersistentDelivery,
    )
}

func errorHandler(d rabbitmq.Delivery) rabbitmq.Action {
    var logMsg LogMessage
    if err := json.Unmarshal(d.Body, &logMsg); err != nil {
        log.Printf("Error Alert: Failed to parse log: %v", err)
        return rabbitmq.NackRequeue
    }
    
    log.Printf("🚨 ERROR ALERT: [%s] %s - %s", 
        logMsg.Service, logMsg.Level, logMsg.Message)
    
    // Here you would send to alerting system (PagerDuty, Slack, etc.)
    
    return rabbitmq.Ack
}

func paymentLogsHandler(d rabbitmq.Delivery) rabbitmq.Action {
    var logMsg LogMessage
    if err := json.Unmarshal(d.Body, &logMsg); err != nil {
        log.Printf("Payment Logs: Failed to parse log: %v", err)
        return rabbitmq.NackRequeue
    }
    
    log.Printf("💳 Payment Service: [%s] %s", logMsg.Level, logMsg.Message)
    
    return rabbitmq.Ack
}

func allLogsHandler(d rabbitmq.Delivery) rabbitmq.Action {
    var logMsg LogMessage
    if err := json.Unmarshal(d.Body, &logMsg); err != nil {
        log.Printf("All Logs: Failed to parse log: %v", err)
        return rabbitmq.NackRequeue
    }
    
    log.Printf("📝 All Logs: [%s/%s] %s", 
        logMsg.Service, logMsg.Level, logMsg.Message)
    
    return rabbitmq.Ack
}

func criticalHandler(d rabbitmq.Delivery) rabbitmq.Action {
    var logMsg LogMessage
    if err := json.Unmarshal(d.Body, &logMsg); err != nil {
        log.Printf("Critical: Failed to parse log: %v", err)
        return rabbitmq.NackRequeue
    }
    
    log.Printf("⚠️ CRITICAL: [%s] %s - %s", 
        logMsg.Service, logMsg.Level, logMsg.Message)
    
    return rabbitmq.Ack
}

func main() {
    conn, err := rabbitmq.NewConn("amqp://guest:guest@localhost")
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()
    
    // Setup publisher
    publisher, err := setupLogPublisher(conn)
    if err != nil {
        log.Fatal(err)
    }
    defer publisher.Close()
    
    // Setup consumers
    errorConsumer, err := setupErrorConsumer(conn)
    if err != nil {
        log.Fatal(err)
    }
    defer errorConsumer.Close()
    
    paymentLogsConsumer, err := setupPaymentLogsConsumer(conn)
    if err != nil {
        log.Fatal(err)
    }
    defer paymentLogsConsumer.Close()
    
    allLogsConsumer, err := setupAllLogsConsumer(conn)
    if err != nil {
        log.Fatal(err)
    }
    defer allLogsConsumer.Close()
    
    criticalConsumer, err := setupCriticalConsumer(conn)
    if err != nil {
        log.Fatal(err)
    }
    defer criticalConsumer.Close()
    
    // Start consumers
    go errorConsumer.Run(errorHandler)
    go paymentLogsConsumer.Run(paymentLogsHandler)
    go allLogsConsumer.Run(allLogsHandler)
    go criticalConsumer.Run(criticalHandler)
    
    // Publish sample logs
    logs := []LogMessage{
        {Level: "info", Service: "payment", Message: "Payment processed successfully", Timestamp: time.Now(), RequestID: "req-001"},
        {Level: "error", Service: "payment", Message: "Payment gateway timeout", Timestamp: time.Now(), RequestID: "req-002"},
        {Level: "warn", Service: "auth", Message: "Multiple login attempts detected", Timestamp: time.Now(), RequestID: "req-003"},
        {Level: "info", Service: "inventory", Message: "Stock updated", Timestamp: time.Now(), RequestID: "req-004"},
        {Level: "error", Service: "inventory", Message: "Database connection failed", Timestamp: time.Now(), RequestID: "req-005"},
        {Level: "debug", Service: "payment", Message: "Debug info", Timestamp: time.Now(), RequestID: "req-006"},
    }
    
    for _, logMsg := range logs {
        if err := publishLog(publisher, logMsg); err != nil {
            log.Printf("Failed to publish log: %v", err)
        }
        time.Sleep(500 * time.Millisecond)
    }
    
    // Keep running to process messages
    time.Sleep(10 * time.Second)
}
```

## Fanout Exchange

### When to Use Fanout Exchange

- **Broadcasting**: Send notifications to all interested parties
- **Cache Invalidation**: Notify all cache instances
- **Real-time Updates**: Push updates to all connected clients
- **Event Streaming**: Broadcast events to multiple processors

### Complete Example: Real-time Notification System

```go
package main

import (
    "context"
    "encoding/json"
    "log"
    "time"
    
    rabbitmq "github.com/wagslane/go-rabbitmq"
)

type Notification struct {
    ID        string    `json:"id"`
    Type      string    `json:"type"`
    Title     string    `json:"title"`
    Message   string    `json:"message"`
    UserID    string    `json:"user_id"`
    Timestamp time.Time `json:"timestamp"`
}

// Publisher: Notification Service
func setupNotificationPublisher(conn *rabbitmq.Conn) (*rabbitmq.Publisher, error) {
    return rabbitmq.NewPublisher(
        conn,
        rabbitmq.WithPublisherOptionsLogging,
        rabbitmq.WithPublisherOptionsExchangeName("notifications"),
        rabbitmq.WithPublisherOptionsExchangeKind("fanout"),
        rabbitmq.WithPublisherOptionsExchangeDeclare,
    )
}

// Consumer: Email Service
func setupEmailConsumer(conn *rabbitmq.Conn) (*rabbitmq.Consumer, error) {
    return rabbitmq.NewConsumer(
        conn,
        "email_notifications",
        // Routing key is ignored in fanout, but we can still specify it
        rabbitmq.WithConsumerOptionsRoutingKey(""),
        rabbitmq.WithConsumerOptionsExchangeName("notifications"),
        rabbitmq.WithConsumerOptionsExchangeKind("fanout"),
        rabbitmq.WithConsumerOptionsExchangeDeclare,
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsConcurrency(2),
    )
}

// Consumer: SMS Service
func setupSMSConsumer(conn *rabbitmq.Conn) (*rabbitmq.Consumer, error) {
    return rabbitmq.NewConsumer(
        conn,
        "sms_notifications",
        rabbitmq.WithConsumerOptionsRoutingKey(""),
        rabbitmq.WithConsumerOptionsExchangeName("notifications"),
        rabbitmq.WithConsumerOptionsExchangeKind("fanout"),
        rabbitmq.WithConsumerOptionsExchangeDeclare,
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsConcurrency(1),
    )
}

// Consumer: Push Notification Service
func setupPushConsumer(conn *rabbitmq.Conn) (*rabbitmq.Consumer, error) {
    return rabbitmq.NewConsumer(
        conn,
        "push_notifications",
        rabbitmq.WithConsumerOptionsRoutingKey(""),
        rabbitmq.WithConsumerOptionsExchangeName("notifications"),
        rabbitmq.WithConsumerOptionsExchangeKind("fanout"),
        rabbitmq.WithConsumerOptionsExchangeDeclare,
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsConcurrency(3),
    )
}

// Consumer: Analytics Service
func setupAnalyticsConsumer(conn *rabbitmq.Conn) (*rabbitmq.Consumer, error) {
    return rabbitmq.NewConsumer(
        conn,
        "analytics_notifications",
        rabbitmq.WithConsumerOptionsRoutingKey(""),
        rabbitmq.WithConsumerOptionsExchangeName("notifications"),
        rabbitmq.WithConsumerOptionsExchangeKind("fanout"),
        rabbitmq.WithConsumerOptionsExchangeDeclare,
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsConcurrency(1),
    )
}

// Consumer: Audit Log Service
func setupAuditConsumer(conn *rabbitmq.Conn) (*rabbitmq.Consumer, error) {
    return rabbitmq.NewConsumer(
        conn,
        "audit_notifications",
        rabbitmq.WithConsumerOptionsRoutingKey(""),
        rabbitmq.WithConsumerOptionsExchangeName("notifications"),
        rabbitmq.WithConsumerOptionsExchangeKind("fanout"),
        rabbitmq.WithConsumerOptionsExchangeDeclare,
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsConcurrency(1),
    )
}

func publishNotification(publisher *rabbitmq.Publisher, notification Notification) error {
    notificationJSON, err := json.Marshal(notification)
    if err != nil {
        return err
    }
    
    // Routing key is ignored in fanout exchange
    return publisher.PublishWithContext(
        context.Background(),
        notificationJSON,
        []string{""},
        rabbitmq.WithPublishOptionsContentType("application/json"),
        rabbitmq.WithPublishOptionsPersistentDelivery,
    )
}

func emailHandler(d rabbitmq.Delivery) rabbitmq.Action {
    var notification Notification
    if err := json.Unmarshal(d.Body, &notification); err != nil {
        log.Printf("Email Service: Failed to parse notification: %v", err)
        return rabbitmq.NackRequeue
    }
    
    log.Printf("📧 Email Service: Sending email to user %s - %s", 
        notification.UserID, notification.Title)
    
    // Simulate email sending
    time.Sleep(200 * time.Millisecond)
    
    return rabbitmq.Ack
}

func smsHandler(d rabbitmq.Delivery) rabbitmq.Action {
    var notification Notification
    if err := json.Unmarshal(d.Body, &notification); err != nil {
        log.Printf("SMS Service: Failed to parse notification: %v", err)
        return rabbitmq.NackRequeue
    }
    
    log.Printf("📱 SMS Service: Sending SMS to user %s - %s", 
        notification.UserID, notification.Title)
    
    // Simulate SMS sending
    time.Sleep(100 * time.Millisecond)
    
    return rabbitmq.Ack
}

func pushHandler(d rabbitmq.Delivery) rabbitmq.Action {
    var notification Notification
    if err := json.Unmarshal(d.Body, &notification); err != nil {
        log.Printf("Push Service: Failed to parse notification: %v", err)
        return rabbitmq.NackRequeue
    }
    
    log.Printf("🔔 Push Service: Sending push notification to user %s - %s", 
        notification.UserID, notification.Title)
    
    // Simulate push notification
    time.Sleep(50 * time.Millisecond)
    
    return rabbitmq.Ack
}

func analyticsHandler(d rabbitmq.Delivery) rabbitmq.Action {
    var notification Notification
    if err := json.Unmarshal(d.Body, &notification); err != nil {
        log.Printf("Analytics Service: Failed to parse notification: %v", err)
        return rabbitmq.NackRequeue
    }
    
    log.Printf("📊 Analytics Service: Recording notification event for user %s, type: %s", 
        notification.UserID, notification.Type)
    
    // Simulate analytics recording
    time.Sleep(25 * time.Millisecond)
    
    return rabbitmq.Ack
}

func auditHandler(d rabbitmq.Delivery) rabbitmq.Action {
    var notification Notification
    if err := json.Unmarshal(d.Body, &notification); err != nil {
        log.Printf("Audit Service: Failed to parse notification: %v", err)
        return rabbitmq.NackRequeue
    }
    
    log.Printf("📋 Audit Service: Logging notification %s for user %s", 
        notification.ID, notification.UserID)
    
    // Simulate audit logging
    time.Sleep(30 * time.Millisecond)
    
    return rabbitmq.Ack
}

func main() {
    conn, err := rabbitmq.NewConn("amqp://guest:guest@localhost")
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()
    
    // Setup publisher
    publisher, err := setupNotificationPublisher(conn)
    if err != nil {
        log.Fatal(err)
    }
    defer publisher.Close()
    
    // Setup consumers
    emailConsumer, err := setupEmailConsumer(conn)
    if err != nil {
        log.Fatal(err)
    }
    defer emailConsumer.Close()
    
    smsConsumer, err := setupSMSConsumer(conn)
    if err != nil {
        log.Fatal(err)
    }
    defer smsConsumer.Close()
    
    pushConsumer, err := setupPushConsumer(conn)
    if err != nil {
        log.Fatal(err)
    }
    defer pushConsumer.Close()
    
    analyticsConsumer, err := setupAnalyticsConsumer(conn)
    if err != nil {
        log.Fatal(err)
    }
    defer analyticsConsumer.Close()
    
    auditConsumer, err := setupAuditConsumer(conn)
    if err != nil {
        log.Fatal(err)
    }
    defer auditConsumer.Close()
    
    // Start consumers
    go emailConsumer.Run(emailHandler)
    go smsConsumer.Run(smsHandler)
    go pushConsumer.Run(pushHandler)
    go analyticsConsumer.Run(analyticsHandler)
    go auditConsumer.Run(auditHandler)
    
    // Publish sample notifications
    notifications := []Notification{
        {
            ID: "notif-001",
            Type: "order_confirmation",
            Title: "Order Confirmed",
            Message: "Your order #12345 has been confirmed",
            UserID: "user-123",
            Timestamp: time.Now(),
        },
        {
            ID: "notif-002",
            Type: "payment_success",
            Title: "Payment Successful",
            Message: "Your payment of $99.99 has been processed",
            UserID: "user-456",
            Timestamp: time.Now(),
        },
        {
            ID: "notif-003",
            Type: "shipping_update",
            Title: "Package Shipped",
            Message: "Your package is on its way",
            UserID: "user-789",
            Timestamp: time.Now(),
        },
    }
    
    for _, notification := range notifications {
        if err := publishNotification(publisher, notification); err != nil {
            log.Printf("Failed to publish notification %s: %v", notification.ID, err)
        }
        time.Sleep(1 * time.Second)
    }
    
    // Keep running to process messages
    time.Sleep(10 * time.Second)
}
```

## Routing Key Strategies

### Direct Exchange Routing Keys

```go
// Service-based routing
"payment.process"
"inventory.update"
"order.confirm"

// Priority-based routing
"order.priority.high"
"order.priority.normal"
"order.priority.low"

// Action-based routing
"user.create"
"user.update"
"user.delete"
```

### Topic Exchange Routing Keys

```go
// Hierarchical patterns
"service.level.category"
"payment.error.gateway"
"inventory.warn.stock"
"auth.info.login"

// Wildcard patterns for consumers
"*.error.*"        // All error messages
"payment.*.*"      // All payment messages
"#"                // All messages
"*.*.critical"     // All critical messages
"auth.warn.*"      // Auth warnings only
```

### Fanout Exchange Routing Keys

```go
// Routing keys are ignored in fanout
""                 // Empty string is sufficient
"ignored"          // Any value works
```

## Performance Considerations

### Direct Exchange
- **Fastest**: Simple hash-based routing
- **Memory**: Minimal overhead
- **Scalability**: Excellent for high-throughput scenarios
- **Use Case**: When you need maximum performance with exact matching

### Topic Exchange
- **Moderate Speed**: Pattern matching adds overhead
- **Memory**: More memory for pattern storage
- **Scalability**: Good, but slower than direct
- **Use Case**: When you need flexible routing with patterns

### Fanout Exchange
- **Fast**: No routing logic, just broadcast
- **Memory**: Minimal overhead
- **Scalability**: Excellent for broadcasting
- **Use Case**: When all consumers need every message

### Benchmarking Example

```go
package main

import (
    "context"
    "log"
    "time"
    
    rabbitmq "github.com/wagslane/go-rabbitmq"
)

func benchmarkExchange(exchangeType string, routingKey string, messageCount int) time.Duration {
    conn, err := rabbitmq.NewConn("amqp://guest:guest@localhost")
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()
    
    publisher, err := rabbitmq.NewPublisher(
        conn,
        rabbitmq.WithPublisherOptionsExchangeName("benchmark"),
        rabbitmq.WithPublisherOptionsExchangeKind(exchangeType),
        rabbitmq.WithPublisherOptionsExchangeDeclare,
    )
    if err != nil {
        log.Fatal(err)
    }
    defer publisher.Close()
    
    message := []byte("benchmark message")
    
    start := time.Now()
    for i := 0; i < messageCount; i++ {
        err := publisher.PublishWithContext(
            context.Background(),
            message,
            []string{routingKey},
        )
        if err != nil {
            log.Printf("Failed to publish message %d: %v", i, err)
        }
    }
    
    return time.Since(start)
}

func runBenchmarks() {
    messageCount := 10000
    
    // Benchmark direct exchange
    directTime := benchmarkExchange("direct", "test.key", messageCount)
    log.Printf("Direct exchange: %d messages in %v (%.2f msg/sec)", 
        messageCount, directTime, float64(messageCount)/directTime.Seconds())
    
    // Benchmark topic exchange
    topicTime := benchmarkExchange("topic", "test.key.example", messageCount)
    log.Printf("Topic exchange: %d messages in %v (%.2f msg/sec)", 
        messageCount, topicTime, float64(messageCount)/topicTime.Seconds())
    
    // Benchmark fanout exchange
    fanoutTime := benchmarkExchange("fanout", "", messageCount)
    log.Printf("Fanout exchange: %d messages in %v (%.2f msg/sec)", 
        messageCount, fanoutTime, float64(messageCount)/fanoutTime.Seconds())
}
```

## Visual Diagrams

### Direct Exchange Flow
```
Producer                Exchange (direct)              Consumers
                        
[Order Service] -----> [orders exchange] -----> [Payment Queue]
                               |                        |
                               |                  payment.process
                               |
                               +--------------> [Inventory Queue]
                               |                        |
                               |                 inventory.update
                               |
                               +--------------> [Priority Queue]
                                                       |
                                                order.priority.high

Routing: Exact key match
Key: "payment.process" -> Payment Queue only
```

### Topic Exchange Flow
```
Producer                Exchange (topic)               Consumers
                        
[Log Service] -------> [logs exchange] ---------> [Error Alerts]
                               |                        |
                               |                   *.error.*
                               |
                               +----------------> [Payment Logs]
                               |                        |
                               |                 payment.*.*
                               |
                               +----------------> [All Logs]
                                                       |
                                                      #

Routing: Pattern matching
Key: "payment.error.gateway" matches:
- *.error.* (Error Alerts)
- payment.*.* (Payment Logs)
- # (All Logs)
```

### Fanout Exchange Flow
```
Producer                Exchange (fanout)              Consumers
                        
[Notification] ------> [notifications] ---------> [Email Service]
Service                    exchange                     |
                               |                  (all messages)
                               |
                               +----------------> [SMS Service]
                               |                        |
                               |                  (all messages)
                               |
                               +----------------> [Push Service]
                               |                        |
                               |                  (all messages)
                               |
                               +----------------> [Analytics]
                                                       |
                                                (all messages)

Routing: Broadcast to all
All queues receive every message
```

### Queue Binding Patterns
```
Direct Exchange Bindings:
Queue: payment_queue
Binding: payment.process

Queue: inventory_queue  
Binding: inventory.update

Topic Exchange Bindings:
Queue: error_alerts
Binding: *.error.*

Queue: payment_service
Binding: payment.*.*

Queue: all_logs
Binding: #

Fanout Exchange Bindings:
Queue: email_notifications
Binding: (none - receives all)

Queue: sms_notifications
Binding: (none - receives all)
```

## Summary

Choose your exchange type based on your routing requirements:

- **Direct**: Use when you need exact routing key matching for specific message delivery
- **Topic**: Use when you need pattern-based routing with wildcards for flexible subscriptions
- **Fanout**: Use when you need to broadcast messages to all interested consumers

Each exchange type has its place in a well-architected messaging system, and you can often use multiple exchange types within the same application to handle different messaging patterns.