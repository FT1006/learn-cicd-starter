# Message Types Handling Guide

This guide demonstrates how to handle different message types (JSON, binary, protobuf) using go-rabbitmq with proper serialization, deserialization, and content type negotiation.

## Table of Contents

1. [Overview](#overview)
2. [JSON Message Handling](#json-message-handling)
3. [Binary Data Handling](#binary-data-handling)
4. [Protocol Buffers Example](#protocol-buffers-example)
5. [Message Headers and Metadata](#message-headers-and-metadata)
6. [Content Type Negotiation](#content-type-negotiation)

## Overview

Different message formats serve different purposes in distributed systems:

- **JSON**: Human-readable, schema-flexible, ideal for REST APIs and web services
- **Binary**: Efficient for large data like images, files, or raw bytes
- **Protocol Buffers**: Strongly-typed, efficient serialization for microservices
- **XML**: Legacy systems integration (not covered in detail)
- **Custom formats**: Domain-specific encodings

Each format requires proper content type headers and serialization/deserialization handling.

## JSON Message Handling

### Structured Events Example

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

// Define structured event types
type OrderEvent struct {
    OrderID     string    `json:"order_id"`
    CustomerID  string    `json:"customer_id"`
    Amount      float64   `json:"amount"`
    Currency    string    `json:"currency"`
    Items       []Item    `json:"items"`
    CreatedAt   time.Time `json:"created_at"`
}

type Item struct {
    ProductID string  `json:"product_id"`
    Quantity  int     `json:"quantity"`
    Price     float64 `json:"price"`
}

// Publisher with JSON serialization
func publishJSONMessage(publisher *rabbitmq.Publisher) error {
    order := OrderEvent{
        OrderID:    "ORD-12345",
        CustomerID: "CUST-67890",
        Amount:     129.99,
        Currency:   "USD",
        Items: []Item{
            {ProductID: "PROD-001", Quantity: 2, Price: 49.99},
            {ProductID: "PROD-002", Quantity: 1, Price: 30.01},
        },
        CreatedAt: time.Now(),
    }
    
    // Serialize to JSON
    data, err := json.Marshal(order)
    if err != nil {
        return fmt.Errorf("failed to marshal order: %w", err)
    }
    
    // Publish with proper content type and metadata
    err = publisher.PublishWithContext(
        context.Background(),
        data,
        []string{"orders.created"},
        rabbitmq.WithPublishOptionsContentType("application/json"),
        rabbitmq.WithPublishOptionsContentEncoding("utf-8"),
        rabbitmq.WithPublishOptionsMessageID(order.OrderID),
        rabbitmq.WithPublishOptionsTimestamp(time.Now()),
        rabbitmq.WithPublishOptionsType("order.created"),
        rabbitmq.WithPublishOptionsHeaders(rabbitmq.Table{
            "version":     "1.0",
            "customer_id": order.CustomerID,
            "amount":      order.Amount,
        }),
        rabbitmq.WithPublishOptionsPersistentDelivery,
    )
    
    if err != nil {
        return fmt.Errorf("failed to publish order: %w", err)
    }
    
    log.Printf("Published order %s", order.OrderID)
    return nil
}

// Consumer with JSON deserialization and validation
func consumeJSONMessages(conn *rabbitmq.Conn) error {
    consumer, err := rabbitmq.NewConsumer(
        conn,
        "orders_processor",
        rabbitmq.WithConsumerOptionsRoutingKey("orders.created"),
        rabbitmq.WithConsumerOptionsExchangeName("events"),
        rabbitmq.WithConsumerOptionsExchangeKind("topic"),
        rabbitmq.WithConsumerOptionsExchangeDeclare,
        rabbitmq.WithConsumerOptionsQueueDurable,
    )
    if err != nil {
        return err
    }
    defer consumer.Close()
    
    err = consumer.Run(func(d rabbitmq.Delivery) rabbitmq.Action {
        // Check content type
        if d.ContentType != "application/json" {
            log.Printf("unexpected content type: %s", d.ContentType)
            return rabbitmq.NackRequeue
        }
        
        // Deserialize JSON
        var order OrderEvent
        if err := json.Unmarshal(d.Body, &order); err != nil {
            log.Printf("failed to unmarshal order: %v", err)
            // Don't requeue malformed messages
            return rabbitmq.NackDiscard
        }
        
        // Validate the order
        if err := validateOrder(&order); err != nil {
            log.Printf("order validation failed: %v", err)
            return rabbitmq.NackDiscard
        }
        
        // Process the order
        log.Printf("Processing order %s for customer %s, amount: %.2f %s",
            order.OrderID, order.CustomerID, order.Amount, order.Currency)
        
        // Simulate processing
        time.Sleep(100 * time.Millisecond)
        
        return rabbitmq.Ack
    })
    
    return err
}

// Schema validation
func validateOrder(order *OrderEvent) error {
    if order.OrderID == "" {
        return fmt.Errorf("order_id is required")
    }
    if order.CustomerID == "" {
        return fmt.Errorf("customer_id is required")
    }
    if order.Amount <= 0 {
        return fmt.Errorf("amount must be positive")
    }
    if len(order.Items) == 0 {
        return fmt.Errorf("order must contain at least one item")
    }
    
    // Validate items
    for i, item := range order.Items {
        if item.ProductID == "" {
            return fmt.Errorf("item %d: product_id is required", i)
        }
        if item.Quantity <= 0 {
            return fmt.Errorf("item %d: quantity must be positive", i)
        }
        if item.Price < 0 {
            return fmt.Errorf("item %d: price cannot be negative", i)
        }
    }
    
    return nil
}
```

### Error Handling for JSON

```go
// Advanced error handling with dead letter queues
func consumeWithErrorHandling(conn *rabbitmq.Conn) error {
    consumer, err := rabbitmq.NewConsumer(
        conn,
        "orders_processor_with_dlq",
        rabbitmq.WithConsumerOptionsRoutingKey("orders.#"),
        rabbitmq.WithConsumerOptionsExchangeName("events"),
        rabbitmq.WithConsumerOptionsQueueDurable,
        // Configure dead letter exchange
        rabbitmq.WithConsumerOptionsQueueArgs(rabbitmq.Table{
            "x-dead-letter-exchange":    "dlx",
            "x-dead-letter-routing-key": "failed.orders",
        }),
    )
    if err != nil {
        return err
    }
    defer consumer.Close()
    
    err = consumer.Run(func(d rabbitmq.Delivery) rabbitmq.Action {
        // Track retry count from headers
        retryCount := 0
        if count, ok := d.Headers["x-retry-count"].(int32); ok {
            retryCount = int(count)
        }
        
        var order OrderEvent
        if err := json.Unmarshal(d.Body, &order); err != nil {
            log.Printf("unmarshal error (retry %d): %v", retryCount, err)
            
            // Send to DLQ after 3 retries
            if retryCount >= 3 {
                return rabbitmq.NackDiscard
            }
            
            // Requeue with incremented retry count
            // In production, you'd republish with updated headers
            return rabbitmq.NackRequeue
        }
        
        // Process order...
        return rabbitmq.Ack
    })
    
    return err
}
```

## Binary Data Handling

### Image Processing Example

```go
package main

import (
    "bytes"
    "context"
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "image"
    "image/jpeg"
    "image/png"
    "io"
    "log"
    
    rabbitmq "github.com/wagslane/go-rabbitmq"
)

// ImageMessage metadata
type ImageMetadata struct {
    Filename    string `json:"filename"`
    Format      string `json:"format"`
    Width       int    `json:"width"`
    Height      int    `json:"height"`
    SizeBytes   int    `json:"size_bytes"`
    Checksum    string `json:"checksum"`
    UploadedBy  string `json:"uploaded_by"`
    ProcessingID string `json:"processing_id"`
}

// Publish binary image data
func publishImage(publisher *rabbitmq.Publisher, imageData []byte, metadata ImageMetadata) error {
    // Calculate checksum
    hash := sha256.Sum256(imageData)
    metadata.Checksum = hex.EncodeToString(hash[:])
    metadata.SizeBytes = len(imageData)
    
    // Determine content type based on format
    contentType := "application/octet-stream"
    switch metadata.Format {
    case "jpeg", "jpg":
        contentType = "image/jpeg"
    case "png":
        contentType = "image/png"
    case "gif":
        contentType = "image/gif"
    case "webp":
        contentType = "image/webp"
    }
    
    // Publish with binary data and metadata in headers
    err := publisher.PublishWithContext(
        context.Background(),
        imageData,
        []string{"images.upload"},
        rabbitmq.WithPublishOptionsContentType(contentType),
        rabbitmq.WithPublishOptionsMessageID(metadata.ProcessingID),
        rabbitmq.WithPublishOptionsTimestamp(time.Now()),
        rabbitmq.WithPublishOptionsHeaders(rabbitmq.Table{
            "filename":     metadata.Filename,
            "format":       metadata.Format,
            "width":        metadata.Width,
            "height":       metadata.Height,
            "size_bytes":   metadata.SizeBytes,
            "checksum":     metadata.Checksum,
            "uploaded_by":  metadata.UploadedBy,
        }),
        rabbitmq.WithPublishOptionsPersistentDelivery,
    )
    
    if err != nil {
        return fmt.Errorf("failed to publish image: %w", err)
    }
    
    log.Printf("Published image %s (%s, %d bytes)", 
        metadata.Filename, metadata.Format, metadata.SizeBytes)
    return nil
}

// Consumer for image processing
func consumeImages(conn *rabbitmq.Conn) error {
    consumer, err := rabbitmq.NewConsumer(
        conn,
        "image_processor",
        rabbitmq.WithConsumerOptionsRoutingKey("images.upload"),
        rabbitmq.WithConsumerOptionsExchangeName("media"),
        rabbitmq.WithConsumerOptionsExchangeKind("topic"),
        rabbitmq.WithConsumerOptionsQueueDurable,
        // Limit concurrent processing for memory-intensive operations
        rabbitmq.WithConsumerOptionsConcurrency(2),
    )
    if err != nil {
        return err
    }
    defer consumer.Close()
    
    err = consumer.Run(func(d rabbitmq.Delivery) rabbitmq.Action {
        // Extract metadata from headers
        metadata := extractImageMetadata(d.Headers)
        
        // Verify checksum
        hash := sha256.Sum256(d.Body)
        checksum := hex.EncodeToString(hash[:])
        if checksum != metadata.Checksum {
            log.Printf("checksum mismatch for %s", metadata.Filename)
            return rabbitmq.NackDiscard
        }
        
        // Process based on content type
        switch d.ContentType {
        case "image/jpeg":
            return processJPEG(d.Body, metadata)
        case "image/png":
            return processPNG(d.Body, metadata)
        default:
            log.Printf("unsupported content type: %s", d.ContentType)
            return rabbitmq.NackDiscard
        }
    })
    
    return err
}

func processJPEG(data []byte, metadata ImageMetadata) rabbitmq.Action {
    // Decode JPEG
    img, err := jpeg.Decode(bytes.NewReader(data))
    if err != nil {
        log.Printf("failed to decode JPEG: %v", err)
        return rabbitmq.NackDiscard
    }
    
    // Example: Create thumbnail
    thumbnail := createThumbnail(img, 200, 200)
    
    // Save or forward processed image...
    log.Printf("Processed JPEG image %s", metadata.Filename)
    
    return rabbitmq.Ack
}

func processPNG(data []byte, metadata ImageMetadata) rabbitmq.Action {
    // Decode PNG
    img, err := png.Decode(bytes.NewReader(data))
    if err != nil {
        log.Printf("failed to decode PNG: %v", err)
        return rabbitmq.NackDiscard
    }
    
    // Process image...
    log.Printf("Processed PNG image %s", metadata.Filename)
    
    return rabbitmq.Ack
}

// Helper to extract metadata from headers
func extractImageMetadata(headers rabbitmq.Table) ImageMetadata {
    metadata := ImageMetadata{}
    
    if v, ok := headers["filename"].(string); ok {
        metadata.Filename = v
    }
    if v, ok := headers["format"].(string); ok {
        metadata.Format = v
    }
    if v, ok := headers["width"].(int32); ok {
        metadata.Width = int(v)
    }
    if v, ok := headers["height"].(int32); ok {
        metadata.Height = int(v)
    }
    if v, ok := headers["size_bytes"].(int32); ok {
        metadata.SizeBytes = int(v)
    }
    if v, ok := headers["checksum"].(string); ok {
        metadata.Checksum = v
    }
    if v, ok := headers["uploaded_by"].(string); ok {
        metadata.UploadedBy = v
    }
    
    return metadata
}
```

### File Upload Handling

```go
// Chunked file upload for large files
type FileChunk struct {
    FileID      string
    ChunkNumber int
    TotalChunks int
    Data        []byte
    Checksum    string
}

func publishLargeFile(publisher *rabbitmq.Publisher, filePath string) error {
    file, err := os.Open(filePath)
    if err != nil {
        return err
    }
    defer file.Close()
    
    fileInfo, _ := file.Stat()
    fileID := generateFileID()
    chunkSize := 1024 * 1024 // 1MB chunks
    totalChunks := int(math.Ceil(float64(fileInfo.Size()) / float64(chunkSize)))
    
    buffer := make([]byte, chunkSize)
    chunkNumber := 0
    
    for {
        n, err := file.Read(buffer)
        if err == io.EOF {
            break
        }
        if err != nil {
            return err
        }
        
        chunk := buffer[:n]
        hash := sha256.Sum256(chunk)
        
        // Publish chunk
        err = publisher.PublishWithContext(
            context.Background(),
            chunk,
            []string{"files.chunk"},
            rabbitmq.WithPublishOptionsContentType("application/octet-stream"),
            rabbitmq.WithPublishOptionsHeaders(rabbitmq.Table{
                "file_id":      fileID,
                "chunk_number": chunkNumber,
                "total_chunks": totalChunks,
                "chunk_size":   n,
                "checksum":     hex.EncodeToString(hash[:]),
                "filename":     filepath.Base(filePath),
            }),
            rabbitmq.WithPublishOptionsMessageID(fmt.Sprintf("%s-%d", fileID, chunkNumber)),
            rabbitmq.WithPublishOptionsPersistentDelivery,
        )
        
        if err != nil {
            return err
        }
        
        chunkNumber++
        log.Printf("Published chunk %d/%d for file %s", chunkNumber, totalChunks, fileID)
    }
    
    return nil
}
```

## Protocol Buffers Example

First, define your protobuf schema:

```protobuf
// events.proto
syntax = "proto3";

package events;
option go_package = "github.com/example/events";

import "google/protobuf/timestamp.proto";

message UserEvent {
    string event_id = 1;
    string user_id = 2;
    EventType type = 3;
    google.protobuf.Timestamp timestamp = 4;
    
    oneof payload {
        UserCreated user_created = 10;
        UserUpdated user_updated = 11;
        UserDeleted user_deleted = 12;
    }
}

enum EventType {
    EVENT_TYPE_UNSPECIFIED = 0;
    EVENT_TYPE_USER_CREATED = 1;
    EVENT_TYPE_USER_UPDATED = 2;
    EVENT_TYPE_USER_DELETED = 3;
}

message UserCreated {
    string email = 1;
    string full_name = 2;
    repeated string roles = 3;
}

message UserUpdated {
    map<string, string> changed_fields = 1;
}

message UserDeleted {
    string reason = 1;
    string deleted_by = 2;
}
```

Go implementation:

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"
    
    "github.com/golang/protobuf/proto"
    "github.com/golang/protobuf/ptypes"
    rabbitmq "github.com/wagslane/go-rabbitmq"
    
    pb "github.com/example/events"
)

// Publish protobuf message
func publishProtobufEvent(publisher *rabbitmq.Publisher) error {
    // Create a user created event
    event := &pb.UserEvent{
        EventId:   generateEventID(),
        UserId:    "user-12345",
        Type:      pb.EventType_EVENT_TYPE_USER_CREATED,
        Timestamp: ptypes.TimestampNow(),
        Payload: &pb.UserEvent_UserCreated{
            UserCreated: &pb.UserCreated{
                Email:    "user@example.com",
                FullName: "John Doe",
                Roles:    []string{"user", "beta_tester"},
            },
        },
    }
    
    // Serialize to protobuf
    data, err := proto.Marshal(event)
    if err != nil {
        return fmt.Errorf("failed to marshal event: %w", err)
    }
    
    // Publish with protobuf content type
    err = publisher.PublishWithContext(
        context.Background(),
        data,
        []string{"users.created"},
        rabbitmq.WithPublishOptionsContentType("application/x-protobuf"),
        rabbitmq.WithPublishOptionsMessageID(event.EventId),
        rabbitmq.WithPublishOptionsTimestamp(time.Now()),
        rabbitmq.WithPublishOptionsType("UserEvent"),
        rabbitmq.WithPublishOptionsHeaders(rabbitmq.Table{
            "proto_type": "events.UserEvent",
            "version":    "1.0",
            "user_id":    event.UserId,
            "event_type": event.Type.String(),
        }),
        rabbitmq.WithPublishOptionsPersistentDelivery,
    )
    
    if err != nil {
        return fmt.Errorf("failed to publish event: %w", err)
    }
    
    log.Printf("Published user event %s", event.EventId)
    return nil
}

// Consume protobuf messages
func consumeProtobufEvents(conn *rabbitmq.Conn) error {
    consumer, err := rabbitmq.NewConsumer(
        conn,
        "user_event_processor",
        rabbitmq.WithConsumerOptionsRoutingKey("users.*"),
        rabbitmq.WithConsumerOptionsExchangeName("events"),
        rabbitmq.WithConsumerOptionsExchangeKind("topic"),
        rabbitmq.WithConsumerOptionsQueueDurable,
    )
    if err != nil {
        return err
    }
    defer consumer.Close()
    
    err = consumer.Run(func(d rabbitmq.Delivery) rabbitmq.Action {
        // Check content type
        if d.ContentType != "application/x-protobuf" {
            log.Printf("unexpected content type: %s", d.ContentType)
            return rabbitmq.NackDiscard
        }
        
        // Check proto type from headers
        protoType, ok := d.Headers["proto_type"].(string)
        if !ok || protoType != "events.UserEvent" {
            log.Printf("unknown proto type: %v", protoType)
            return rabbitmq.NackDiscard
        }
        
        // Deserialize protobuf
        var event pb.UserEvent
        if err := proto.Unmarshal(d.Body, &event); err != nil {
            log.Printf("failed to unmarshal event: %v", err)
            return rabbitmq.NackDiscard
        }
        
        // Process based on event type
        switch event.Type {
        case pb.EventType_EVENT_TYPE_USER_CREATED:
            return handleUserCreated(&event)
        case pb.EventType_EVENT_TYPE_USER_UPDATED:
            return handleUserUpdated(&event)
        case pb.EventType_EVENT_TYPE_USER_DELETED:
            return handleUserDeleted(&event)
        default:
            log.Printf("unknown event type: %v", event.Type)
            return rabbitmq.NackDiscard
        }
    })
    
    return err
}

func handleUserCreated(event *pb.UserEvent) rabbitmq.Action {
    created := event.GetUserCreated()
    if created == nil {
        log.Printf("user created payload is nil")
        return rabbitmq.NackDiscard
    }
    
    log.Printf("User created: %s (%s) with roles: %v", 
        created.FullName, created.Email, created.Roles)
    
    // Process user creation...
    
    return rabbitmq.Ack
}
```

## Message Headers and Metadata

### Using Headers for Message Routing and Metadata

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "time"
    
    rabbitmq "github.com/wagslane/go-rabbitmq"
    "github.com/google/uuid"
)

// Multi-tenant message with headers
type TenantMessage struct {
    TenantID string          `json:"tenant_id"`
    Data     json.RawMessage `json:"data"`
}

func publishWithHeaders(publisher *rabbitmq.Publisher) error {
    tenantID := "tenant-123"
    userID := "user-456"
    requestID := uuid.New().String()
    
    message := TenantMessage{
        TenantID: tenantID,
        Data:     json.RawMessage(`{"action": "update_profile", "name": "Jane Doe"}`),
    }
    
    data, _ := json.Marshal(message)
    
    // Rich headers for routing and debugging
    headers := rabbitmq.Table{
        // Routing headers
        "tenant_id":    tenantID,
        "user_id":      userID,
        "environment":  "production",
        "region":       "us-east-1",
        
        // Tracking headers
        "request_id":   requestID,
        "trace_id":     generateTraceID(),
        "span_id":      generateSpanID(),
        
        // Business metadata
        "priority":     "high",
        "sla_deadline": time.Now().Add(5 * time.Minute).Unix(),
        "retry_count":  int32(0),
        "max_retries":  int32(3),
        
        // Versioning
        "schema_version": "2.0",
        "api_version":    "v2",
        
        // Feature flags
        "features": map[string]interface{}{
            "new_validation": true,
            "async_processing": false,
        },
    }
    
    err := publisher.PublishWithContext(
        context.Background(),
        data,
        []string{fmt.Sprintf("tenant.%s.profile.update", tenantID)},
        rabbitmq.WithPublishOptionsContentType("application/json"),
        rabbitmq.WithPublishOptionsHeaders(headers),
        rabbitmq.WithPublishOptionsMessageID(requestID),
        rabbitmq.WithPublishOptionsTimestamp(time.Now()),
        rabbitmq.WithPublishOptionsCorrelationID(requestID),
        rabbitmq.WithPublishOptionsExpiration("300000"), // 5 minutes TTL
        rabbitmq.WithPublishOptionsPriority(5), // 0-9 scale
        rabbitmq.WithPublishOptionsPersistentDelivery,
    )
    
    if err != nil {
        return err
    }
    
    log.Printf("Published message with request_id: %s", requestID)
    return nil
}

// Consumer that uses headers for routing logic
func consumeWithHeaderRouting(conn *rabbitmq.Conn) error {
    consumer, err := rabbitmq.NewConsumer(
        conn,
        "header_based_processor",
        rabbitmq.WithConsumerOptionsRoutingKey("tenant.#"),
        rabbitmq.WithConsumerOptionsExchangeName("events"),
        rabbitmq.WithConsumerOptionsQueueDurable,
    )
    if err != nil {
        return err
    }
    defer consumer.Close()
    
    err = consumer.Run(func(d rabbitmq.Delivery) rabbitmq.Action {
        // Extract routing information from headers
        tenantID := getHeaderString(d.Headers, "tenant_id")
        userID := getHeaderString(d.Headers, "user_id")
        requestID := getHeaderString(d.Headers, "request_id")
        priority := getHeaderString(d.Headers, "priority")
        
        // Check SLA deadline
        if deadline, ok := d.Headers["sla_deadline"].(int64); ok {
            if time.Now().Unix() > deadline {
                log.Printf("Message %s exceeded SLA deadline", requestID)
                // Send to dead letter queue
                return rabbitmq.NackDiscard
            }
        }
        
        // Multi-tenant isolation
        if !isAuthorizedTenant(tenantID) {
            log.Printf("Unauthorized tenant: %s", tenantID)
            return rabbitmq.NackDiscard
        }
        
        // Priority-based processing
        if priority == "high" {
            return processHighPriority(d, tenantID, userID, requestID)
        }
        
        // Feature flag checking
        if features, ok := d.Headers["features"].(map[string]interface{}); ok {
            if newValidation, ok := features["new_validation"].(bool); ok && newValidation {
                // Use new validation logic
                log.Printf("Using new validation for request %s", requestID)
            }
        }
        
        // Process message
        var msg TenantMessage
        if err := json.Unmarshal(d.Body, &msg); err != nil {
            return handleRetry(d, err)
        }
        
        log.Printf("Processing message for tenant %s, user %s, request %s",
            tenantID, userID, requestID)
        
        return rabbitmq.Ack
    })
    
    return err
}

// Retry handling with exponential backoff
func handleRetry(d rabbitmq.Delivery, err error) rabbitmq.Action {
    retryCount := int32(0)
    maxRetries := int32(3)
    
    if count, ok := d.Headers["retry_count"].(int32); ok {
        retryCount = count
    }
    if max, ok := d.Headers["max_retries"].(int32); ok {
        maxRetries = max
    }
    
    if retryCount >= maxRetries {
        log.Printf("Max retries reached for message %s: %v", 
            d.MessageId, err)
        return rabbitmq.NackDiscard
    }
    
    // In production, republish with updated retry count and backoff
    log.Printf("Retry %d/%d for message %s: %v", 
        retryCount+1, maxRetries, d.MessageId, err)
    
    return rabbitmq.NackRequeue
}

// Helper functions
func getHeaderString(headers rabbitmq.Table, key string) string {
    if val, ok := headers[key].(string); ok {
        return val
    }
    return ""
}
```

## Content Type Negotiation

### Dynamic Content Type Handling

```go
package main

import (
    "bytes"
    "compress/gzip"
    "context"
    "encoding/json"
    "encoding/xml"
    "fmt"
    "io"
    "log"
    
    rabbitmq "github.com/wagslane/go-rabbitmq"
    "github.com/golang/protobuf/proto"
)

// Flexible message that can be serialized in multiple formats
type FlexibleMessage struct {
    ID      string                 `json:"id" xml:"id"`
    Type    string                 `json:"type" xml:"type"`
    Payload map[string]interface{} `json:"payload" xml:"payload"`
}

// Content negotiation publisher
func publishWithContentNegotiation(publisher *rabbitmq.Publisher, msg FlexibleMessage, format string) error {
    var data []byte
    var contentType string
    var err error
    
    // Serialize based on requested format
    switch format {
    case "json":
        data, err = json.Marshal(msg)
        contentType = "application/json"
        
    case "xml":
        data, err = xml.Marshal(msg)
        contentType = "application/xml"
        
    case "protobuf":
        // Convert to protobuf (assuming you have a proto definition)
        // data, err = proto.Marshal(convertToProto(msg))
        // contentType = "application/x-protobuf"
        return fmt.Errorf("protobuf serialization not implemented in this example")
        
    case "msgpack":
        // Using msgpack for efficient binary serialization
        // data, err = msgpack.Marshal(msg)
        // contentType = "application/x-msgpack"
        return fmt.Errorf("msgpack serialization not implemented in this example")
        
    default:
        return fmt.Errorf("unsupported format: %s", format)
    }
    
    if err != nil {
        return fmt.Errorf("serialization error: %w", err)
    }
    
    // Check if compression is beneficial
    var finalData []byte
    var contentEncoding string
    
    if len(data) > 1024 { // Compress if larger than 1KB
        compressedData, err := compressData(data)
        if err == nil && len(compressedData) < len(data) {
            finalData = compressedData
            contentEncoding = "gzip"
            log.Printf("Compressed %d bytes to %d bytes (%.1f%% reduction)",
                len(data), len(compressedData), 
                float64(len(data)-len(compressedData))/float64(len(data))*100)
        } else {
            finalData = data
        }
    } else {
        finalData = data
    }
    
    // Build publish options
    publishOpts := []func(*rabbitmq.PublishOptions){
        rabbitmq.WithPublishOptionsContentType(contentType),
        rabbitmq.WithPublishOptionsMessageID(msg.ID),
        rabbitmq.WithPublishOptionsType(msg.Type),
        rabbitmq.WithPublishOptionsTimestamp(time.Now()),
        rabbitmq.WithPublishOptionsPersistentDelivery,
        rabbitmq.WithPublishOptionsHeaders(rabbitmq.Table{
            "format":          format,
            "original_size":   len(data),
            "compressed_size": len(finalData),
        }),
    }
    
    if contentEncoding != "" {
        publishOpts = append(publishOpts, 
            rabbitmq.WithPublishOptionsContentEncoding(contentEncoding))
    }
    
    err = publisher.PublishWithContext(
        context.Background(),
        finalData,
        []string{fmt.Sprintf("messages.%s", msg.Type)},
        publishOpts...,
    )
    
    if err != nil {
        return fmt.Errorf("publish error: %w", err)
    }
    
    log.Printf("Published message %s as %s (encoding: %s)", 
        msg.ID, contentType, contentEncoding)
    return nil
}

// Universal consumer that handles multiple content types
func consumeWithContentNegotiation(conn *rabbitmq.Conn) error {
    consumer, err := rabbitmq.NewConsumer(
        conn,
        "universal_processor",
        rabbitmq.WithConsumerOptionsRoutingKey("messages.#"),
        rabbitmq.WithConsumerOptionsExchangeName("events"),
        rabbitmq.WithConsumerOptionsQueueDurable,
    )
    if err != nil {
        return err
    }
    defer consumer.Close()
    
    err = consumer.Run(func(d rabbitmq.Delivery) rabbitmq.Action {
        // Decompress if needed
        data := d.Body
        if d.ContentEncoding == "gzip" {
            decompressed, err := decompressData(data)
            if err != nil {
                log.Printf("decompression error: %v", err)
                return rabbitmq.NackDiscard
            }
            data = decompressed
            log.Printf("Decompressed message from %d to %d bytes", 
                len(d.Body), len(data))
        }
        
        // Parse based on content type
        var msg FlexibleMessage
        switch d.ContentType {
        case "application/json":
            if err := json.Unmarshal(data, &msg); err != nil {
                log.Printf("JSON unmarshal error: %v", err)
                return rabbitmq.NackDiscard
            }
            
        case "application/xml":
            if err := xml.Unmarshal(data, &msg); err != nil {
                log.Printf("XML unmarshal error: %v", err)
                return rabbitmq.NackDiscard
            }
            
        case "application/x-protobuf":
            // Handle protobuf deserialization
            log.Printf("Protobuf handling not implemented")
            return rabbitmq.NackDiscard
            
        case "application/x-msgpack":
            // Handle msgpack deserialization
            log.Printf("Msgpack handling not implemented")
            return rabbitmq.NackDiscard
            
        default:
            // Try to detect format
            if detected, err := detectAndParse(data, &msg); err != nil || !detected {
                log.Printf("Unknown content type %s and auto-detection failed", 
                    d.ContentType)
                return rabbitmq.NackDiscard
            }
        }
        
        // Process the message
        log.Printf("Processed message %s of type %s", msg.ID, msg.Type)
        
        return rabbitmq.Ack
    })
    
    return err
}

// Compression helpers
func compressData(data []byte) ([]byte, error) {
    var buf bytes.Buffer
    gz := gzip.NewWriter(&buf)
    
    if _, err := gz.Write(data); err != nil {
        return nil, err
    }
    
    if err := gz.Close(); err != nil {
        return nil, err
    }
    
    return buf.Bytes(), nil
}

func decompressData(data []byte) ([]byte, error) {
    gz, err := gzip.NewReader(bytes.NewReader(data))
    if err != nil {
        return nil, err
    }
    defer gz.Close()
    
    return io.ReadAll(gz)
}

// Auto-detection for unknown content types
func detectAndParse(data []byte, msg *FlexibleMessage) (bool, error) {
    // Try JSON first (most common)
    if err := json.Unmarshal(data, msg); err == nil {
        log.Printf("Auto-detected JSON format")
        return true, nil
    }
    
    // Try XML
    if err := xml.Unmarshal(data, msg); err == nil {
        log.Printf("Auto-detected XML format")
        return true, nil
    }
    
    // Could add more format detection here
    
    return false, fmt.Errorf("unable to detect message format")
}
```

## Best Practices Summary

1. **Always set content type**: Use `WithPublishOptionsContentType` to indicate message format
2. **Use appropriate encoding**: Set `WithPublishOptionsContentEncoding` for character encoding or compression
3. **Include metadata in headers**: Use `WithPublishOptionsHeaders` for routing and debugging information
4. **Set message IDs**: Use `WithPublishOptionsMessageID` for tracking and deduplication
5. **Add timestamps**: Use `WithPublishOptionsTimestamp` for message age tracking
6. **Handle errors gracefully**: Implement retry logic with backoff and dead letter queues
7. **Validate messages**: Check content type and validate schema before processing
8. **Consider compression**: Compress large messages to reduce network overhead
9. **Use correlation IDs**: Track request/response patterns with `WithPublishOptionsCorrelationID`
10. **Set TTL when appropriate**: Use `WithPublishOptionsExpiration` for time-sensitive messages

## Complete Working Example

Here's a complete example that ties everything together:

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "os"
    "os/signal"
    "syscall"
    "time"
    
    rabbitmq "github.com/wagslane/go-rabbitmq"
)

func main() {
    // Connect to RabbitMQ
    conn, err := rabbitmq.NewConn(
        "amqp://guest:guest@localhost",
        rabbitmq.WithConnectionOptionsLogging,
    )
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()
    
    // Create publisher
    publisher, err := rabbitmq.NewPublisher(
        conn,
        rabbitmq.WithPublisherOptionsLogging,
        rabbitmq.WithPublisherOptionsExchangeName("messages"),
        rabbitmq.WithPublisherOptionsExchangeDeclare,
        rabbitmq.WithPublisherOptionsExchangeKind("topic"),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer publisher.Close()
    
    // Start consumer in background
    go startConsumer(conn)
    
    // Publish different message types
    ctx := context.Background()
    
    // JSON message
    jsonMsg := map[string]interface{}{
        "user_id": "123",
        "action":  "login",
        "timestamp": time.Now(),
    }
    jsonData, _ := json.Marshal(jsonMsg)
    
    err = publisher.PublishWithContext(
        ctx,
        jsonData,
        []string{"events.user.login"},
        rabbitmq.WithPublishOptionsContentType("application/json"),
        rabbitmq.WithPublishOptionsMessageID("msg-001"),
        rabbitmq.WithPublishOptionsTimestamp(time.Now()),
        rabbitmq.WithPublishOptionsHeaders(rabbitmq.Table{
            "source": "web",
            "version": "1.0",
        }),
    )
    if err != nil {
        log.Printf("Failed to publish JSON message: %v", err)
    }
    
    // Binary message (simulated image)
    binaryData := []byte{0xFF, 0xD8, 0xFF, 0xE0} // JPEG header
    
    err = publisher.PublishWithContext(
        ctx,
        binaryData,
        []string{"media.image.upload"},
        rabbitmq.WithPublishOptionsContentType("image/jpeg"),
        rabbitmq.WithPublishOptionsMessageID("img-001"),
        rabbitmq.WithPublishOptionsHeaders(rabbitmq.Table{
            "filename": "test.jpg",
            "size": len(binaryData),
        }),
    )
    if err != nil {
        log.Printf("Failed to publish binary message: %v", err)
    }
    
    // Wait for interrupt
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    <-sigChan
    
    log.Println("Shutting down...")
}

func startConsumer(conn *rabbitmq.Conn) {
    consumer, err := rabbitmq.NewConsumer(
        conn,
        "multi_format_consumer",
        rabbitmq.WithConsumerOptionsRoutingKey("#"),
        rabbitmq.WithConsumerOptionsExchangeName("messages"),
        rabbitmq.WithConsumerOptionsExchangeKind("topic"),
        rabbitmq.WithConsumerOptionsQueueDurable,
    )
    if err != nil {
        log.Fatal(err)
    }
    defer consumer.Close()
    
    err = consumer.Run(func(d rabbitmq.Delivery) rabbitmq.Action {
        log.Printf("Received message: ID=%s, Type=%s, Size=%d bytes",
            d.MessageId, d.ContentType, len(d.Body))
        
        // Process based on content type
        switch d.ContentType {
        case "application/json":
            var data map[string]interface{}
            if err := json.Unmarshal(d.Body, &data); err != nil {
                log.Printf("JSON parse error: %v", err)
                return rabbitmq.NackDiscard
            }
            log.Printf("JSON message: %v", data)
            
        case "image/jpeg", "image/png":
            filename := "unknown"
            if name, ok := d.Headers["filename"].(string); ok {
                filename = name
            }
            log.Printf("Binary image: %s (%d bytes)", filename, len(d.Body))
            
        default:
            log.Printf("Unknown content type: %s", d.ContentType)
        }
        
        return rabbitmq.Ack
    })
    
    if err != nil {
        log.Printf("Consumer error: %v", err)
    }
}
```

This guide provides comprehensive examples of handling different message types with go-rabbitmq, demonstrating proper use of content types, encoding, headers, and metadata for robust message processing.