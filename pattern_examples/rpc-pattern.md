# RPC Pattern Over RabbitMQ

## Overview

The RPC (Remote Procedure Call) pattern allows you to invoke functions on remote services synchronously. Unlike traditional asynchronous messaging, RPC provides a request-response mechanism where the client waits for a reply from the server.

### Key Components

1. **RPC Client**: Sends requests and waits for responses
2. **RPC Server**: Processes requests and sends replies
3. **Correlation ID**: Links requests with their corresponding responses
4. **Reply Queue**: Temporary exclusive queue for receiving responses
5. **Request Queue**: Persistent queue where servers listen for requests

### Architecture

```
Client                     Server
  |                          |
  |---[request + replyTo]--->|
  |                          |
  |<---[response]------------|
  |                          |
```

## Implementation

### 1. RPC Client

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "sync"
    "time"

    "github.com/google/uuid"
    rabbitmq "github.com/wagslane/go-rabbitmq"
)

type RPCClient struct {
    conn        *rabbitmq.Conn
    publisher   *rabbitmq.Publisher
    consumer    *rabbitmq.Consumer
    replyQueue  string
    pendingRPCs map[string]chan RPCResponse
    mu          sync.RWMutex
}

type RPCRequest struct {
    Method string                 `json:"method"`
    Params map[string]interface{} `json:"params"`
}

type RPCResponse struct {
    Result interface{} `json:"result,omitempty"`
    Error  string      `json:"error,omitempty"`
}

func NewRPCClient(conn *rabbitmq.Conn) (*RPCClient, error) {
    // Create publisher
    publisher, err := rabbitmq.NewPublisher(
        conn,
        rabbitmq.WithPublisherOptionsLogging,
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create publisher: %w", err)
    }

    // Generate unique reply queue name
    replyQueue := fmt.Sprintf("rpc_reply_%s", uuid.New().String())

    // Create consumer for reply queue
    consumer, err := rabbitmq.NewConsumer(
        conn,
        replyQueue,
        rabbitmq.WithConsumerOptionsQueueExclusive,
        rabbitmq.WithConsumerOptionsQueueAutoDelete,
        rabbitmq.WithConsumerOptionsLogging,
    )
    if err != nil {
        publisher.Close()
        return nil, fmt.Errorf("failed to create consumer: %w", err)
    }

    client := &RPCClient{
        conn:        conn,
        publisher:   publisher,
        consumer:    consumer,
        replyQueue:  replyQueue,
        pendingRPCs: make(map[string]chan RPCResponse),
    }

    // Start consuming replies
    go client.consumeReplies()

    return client, nil
}

func (c *RPCClient) consumeReplies() {
    err := c.consumer.Run(func(d rabbitmq.Delivery) rabbitmq.Action {
        correlationID := d.CorrelationId

        c.mu.RLock()
        responseChan, exists := c.pendingRPCs[correlationID]
        c.mu.RUnlock()

        if !exists {
            log.Printf("Received response for unknown correlation ID: %s", correlationID)
            return rabbitmq.Ack
        }

        var response RPCResponse
        if err := json.Unmarshal(d.Body, &response); err != nil {
            log.Printf("Failed to unmarshal response: %v", err)
            response.Error = "Failed to unmarshal response"
        }

        // Send response to waiting goroutine
        select {
        case responseChan <- response:
        case <-time.After(1 * time.Second):
            log.Printf("Timeout sending response for correlation ID: %s", correlationID)
        }

        return rabbitmq.Ack
    })

    if err != nil {
        log.Printf("Error consuming replies: %v", err)
    }
}

func (c *RPCClient) Call(ctx context.Context, requestQueue string, method string, params map[string]interface{}, timeout time.Duration) (interface{}, error) {
    // Generate correlation ID
    correlationID := uuid.New().String()

    // Create request
    request := RPCRequest{
        Method: method,
        Params: params,
    }

    requestBody, err := json.Marshal(request)
    if err != nil {
        return nil, fmt.Errorf("failed to marshal request: %w", err)
    }

    // Create response channel
    responseChan := make(chan RPCResponse, 1)

    // Register pending RPC
    c.mu.Lock()
    c.pendingRPCs[correlationID] = responseChan
    c.mu.Unlock()

    // Cleanup on exit
    defer func() {
        c.mu.Lock()
        delete(c.pendingRPCs, correlationID)
        c.mu.Unlock()
        close(responseChan)
    }()

    // Publish request
    err = c.publisher.PublishWithContext(
        ctx,
        requestBody,
        []string{requestQueue},
        rabbitmq.WithPublishOptionsContentType("application/json"),
        rabbitmq.WithPublishOptionsCorrelationID(correlationID),
        rabbitmq.WithPublishOptionsReplyTo(c.replyQueue),
        rabbitmq.WithPublishOptionsExpiration(fmt.Sprintf("%d", timeout.Milliseconds())),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to publish request: %w", err)
    }

    // Wait for response with timeout
    select {
    case response := <-responseChan:
        if response.Error != "" {
            return nil, fmt.Errorf("RPC error: %s", response.Error)
        }
        return response.Result, nil
    case <-time.After(timeout):
        return nil, fmt.Errorf("RPC timeout after %v", timeout)
    case <-ctx.Done():
        return nil, fmt.Errorf("RPC cancelled: %w", ctx.Err())
    }
}

func (c *RPCClient) Close() error {
    if c.consumer != nil {
        c.consumer.Close()
    }
    if c.publisher != nil {
        c.publisher.Close()
    }
    return nil
}
```

### 2. RPC Server

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

type RPCServer struct {
    conn     *rabbitmq.Conn
    consumer *rabbitmq.Consumer
    publisher *rabbitmq.Publisher
    handlers map[string]RPCHandler
}

type RPCHandler func(params map[string]interface{}) (interface{}, error)

func NewRPCServer(conn *rabbitmq.Conn, requestQueue string) (*RPCServer, error) {
    // Create consumer for request queue
    consumer, err := rabbitmq.NewConsumer(
        conn,
        requestQueue,
        rabbitmq.WithConsumerOptionsQueueDurable,
        rabbitmq.WithConsumerOptionsLogging,
        rabbitmq.WithConsumerOptionsQOSPrefetch(1), // Process one request at a time
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create consumer: %w", err)
    }

    // Create publisher for replies
    publisher, err := rabbitmq.NewPublisher(
        conn,
        rabbitmq.WithPublisherOptionsLogging,
    )
    if err != nil {
        consumer.Close()
        return nil, fmt.Errorf("failed to create publisher: %w", err)
    }

    server := &RPCServer{
        conn:     conn,
        consumer: consumer,
        publisher: publisher,
        handlers: make(map[string]RPCHandler),
    }

    return server, nil
}

func (s *RPCServer) RegisterHandler(method string, handler RPCHandler) {
    s.handlers[method] = handler
}

func (s *RPCServer) Start() error {
    return s.consumer.Run(func(d rabbitmq.Delivery) rabbitmq.Action {
        // Parse request
        var request RPCRequest
        if err := json.Unmarshal(d.Body, &request); err != nil {
            log.Printf("Failed to unmarshal request: %v", err)
            s.sendErrorResponse(d, "Invalid request format")
            return rabbitmq.Ack
        }

        // Check if handler exists
        handler, exists := s.handlers[request.Method]
        if !exists {
            s.sendErrorResponse(d, fmt.Sprintf("Method '%s' not found", request.Method))
            return rabbitmq.Ack
        }

        // Execute handler
        result, err := handler(request.Params)
        
        // Send response
        if err != nil {
            s.sendErrorResponse(d, err.Error())
        } else {
            s.sendSuccessResponse(d, result)
        }

        return rabbitmq.Ack
    })
}

func (s *RPCServer) sendSuccessResponse(d rabbitmq.Delivery, result interface{}) {
    response := RPCResponse{
        Result: result,
    }
    s.sendResponse(d, response)
}

func (s *RPCServer) sendErrorResponse(d rabbitmq.Delivery, errorMsg string) {
    response := RPCResponse{
        Error: errorMsg,
    }
    s.sendResponse(d, response)
}

func (s *RPCServer) sendResponse(d rabbitmq.Delivery, response RPCResponse) {
    if d.ReplyTo == "" {
        log.Printf("No reply queue specified for message")
        return
    }

    responseBody, err := json.Marshal(response)
    if err != nil {
        log.Printf("Failed to marshal response: %v", err)
        return
    }

    err = s.publisher.PublishWithContext(
        context.Background(),
        responseBody,
        []string{d.ReplyTo},
        rabbitmq.WithPublishOptionsContentType("application/json"),
        rabbitmq.WithPublishOptionsCorrelationID(d.CorrelationId),
    )
    if err != nil {
        log.Printf("Failed to send response: %v", err)
    }
}

func (s *RPCServer) Close() error {
    if s.consumer != nil {
        s.consumer.Close()
    }
    if s.publisher != nil {
        s.publisher.Close()
    }
    return nil
}
```

### 3. Complete Working Example

#### Server Implementation

```go
package main

import (
    "fmt"
    "log"
    "math"
    "os"
    "os/signal"
    "syscall"

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

    // Create RPC server
    server, err := NewRPCServer(conn, "math_rpc")
    if err != nil {
        log.Fatal(err)
    }
    defer server.Close()

    // Register handlers
    server.RegisterHandler("add", func(params map[string]interface{}) (interface{}, error) {
        a, aOk := params["a"].(float64)
        b, bOk := params["b"].(float64)
        if !aOk || !bOk {
            return nil, fmt.Errorf("invalid parameters: expected numbers")
        }
        return a + b, nil
    })

    server.RegisterHandler("multiply", func(params map[string]interface{}) (interface{}, error) {
        a, aOk := params["a"].(float64)
        b, bOk := params["b"].(float64)
        if !aOk || !bOk {
            return nil, fmt.Errorf("invalid parameters: expected numbers")
        }
        return a * b, nil
    })

    server.RegisterHandler("sqrt", func(params map[string]interface{}) (interface{}, error) {
        x, xOk := params["x"].(float64)
        if !xOk {
            return nil, fmt.Errorf("invalid parameter: expected number")
        }
        if x < 0 {
            return nil, fmt.Errorf("cannot compute square root of negative number")
        }
        return math.Sqrt(x), nil
    })

    // Handle graceful shutdown
    sigs := make(chan os.Signal, 1)
    signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

    go func() {
        sig := <-sigs
        fmt.Printf("\nReceived signal: %v\n", sig)
        server.Close()
        os.Exit(0)
    }()

    fmt.Println("Math RPC server started. Waiting for requests...")
    
    // Start server
    if err := server.Start(); err != nil {
        log.Fatal(err)
    }
}
```

#### Client Implementation

```go
package main

import (
    "context"
    "fmt"
    "log"
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

    // Create RPC client
    client, err := NewRPCClient(conn)
    if err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    ctx := context.Background()
    timeout := 5 * time.Second

    // Test addition
    result, err := client.Call(ctx, "math_rpc", "add", map[string]interface{}{
        "a": 10.0,
        "b": 20.0,
    }, timeout)
    if err != nil {
        log.Printf("Add error: %v", err)
    } else {
        fmt.Printf("10 + 20 = %v\n", result)
    }

    // Test multiplication
    result, err = client.Call(ctx, "math_rpc", "multiply", map[string]interface{}{
        "a": 5.0,
        "b": 8.0,
    }, timeout)
    if err != nil {
        log.Printf("Multiply error: %v", err)
    } else {
        fmt.Printf("5 * 8 = %v\n", result)
    }

    // Test square root
    result, err = client.Call(ctx, "math_rpc", "sqrt", map[string]interface{}{
        "x": 25.0,
    }, timeout)
    if err != nil {
        log.Printf("Sqrt error: %v", err)
    } else {
        fmt.Printf("√25 = %v\n", result)
    }

    // Test error case
    result, err = client.Call(ctx, "math_rpc", "sqrt", map[string]interface{}{
        "x": -4.0,
    }, timeout)
    if err != nil {
        fmt.Printf("Expected error: %v\n", err)
    } else {
        fmt.Printf("Unexpected result: %v\n", result)
    }

    // Test timeout
    shortTimeout := 100 * time.Millisecond
    result, err = client.Call(ctx, "math_rpc", "add", map[string]interface{}{
        "a": 1.0,
        "b": 2.0,
    }, shortTimeout)
    if err != nil {
        fmt.Printf("Timeout test: %v\n", err)
    } else {
        fmt.Printf("Result: %v\n", result)
    }
}
```

## Key Features Explained

### 1. Correlation ID Management

The correlation ID is crucial for matching requests with responses:

```go
// Client generates unique correlation ID
correlationID := uuid.New().String()

// Server echoes back the correlation ID
rabbitmq.WithPublishOptionsCorrelationID(d.CorrelationId)
```

### 2. Reply Queue Setup

The client creates an exclusive, auto-delete queue for responses:

```go
consumer, err := rabbitmq.NewConsumer(
    conn,
    replyQueue,
    rabbitmq.WithConsumerOptionsQueueExclusive,   // Only this consumer can access
    rabbitmq.WithConsumerOptionsQueueAutoDelete,  // Deleted when consumer disconnects
    rabbitmq.WithConsumerOptionsLogging,
)
```

### 3. Timeout Handling

Multiple timeout mechanisms ensure robustness:

```go
// Request expiration (server-side timeout)
rabbitmq.WithPublishOptionsExpiration(fmt.Sprintf("%d", timeout.Milliseconds()))

// Client-side timeout
select {
case response := <-responseChan:
    // Handle response
case <-time.After(timeout):
    return nil, fmt.Errorf("RPC timeout after %v", timeout)
}
```

### 4. Error Handling

Comprehensive error handling at multiple levels:

```go
// Server error response
type RPCResponse struct {
    Result interface{} `json:"result,omitempty"`
    Error  string      `json:"error,omitempty"`
}

// Client error checking
if response.Error != "" {
    return nil, fmt.Errorf("RPC error: %s", response.Error)
}
```

## Performance Considerations

### 1. Connection Pooling

For high-throughput scenarios, consider connection pooling:

```go
type RPCClientPool struct {
    clients []*RPCClient
    current int
    mu      sync.Mutex
}

func (p *RPCClientPool) GetClient() *RPCClient {
    p.mu.Lock()
    defer p.mu.Unlock()
    
    client := p.clients[p.current]
    p.current = (p.current + 1) % len(p.clients)
    return client
}
```

### 2. Message Prefetch

Configure appropriate prefetch for servers:

```go
rabbitmq.WithConsumerOptionsQOSPrefetch(1), // Process one request at a time
```

### 3. Connection Management

Implement proper connection lifecycle management:

```go
func (c *RPCClient) healthCheck() error {
    if c.conn.IsClosed() {
        return fmt.Errorf("connection is closed")
    }
    return nil
}
```

### 4. Batch Operations

For multiple related calls, consider batching:

```go
type BatchRequest struct {
    Requests []RPCRequest `json:"requests"`
}

type BatchResponse struct {
    Responses []RPCResponse `json:"responses"`
}
```

## When to Use RPC vs Async Messaging

### Use RPC When:

1. **Synchronous Operations**: You need immediate results
2. **Request-Response Pattern**: Clear request-response relationship
3. **Simple Communication**: Direct method calls feel natural
4. **Error Handling**: Need to know immediately if operation failed
5. **Transactional Operations**: Operations that need immediate confirmation

### Use Async Messaging When:

1. **Fire-and-Forget**: Don't need immediate response
2. **Event-Driven Architecture**: Publishing events for multiple consumers
3. **Decoupling**: Want loose coupling between services
4. **Scalability**: Need to handle varying loads
5. **Reliability**: Can tolerate temporary failures

## Best Practices

### 1. Idempotency

Make RPC operations idempotent when possible:

```go
server.RegisterHandler("createUser", func(params map[string]interface{}) (interface{}, error) {
    userID := params["id"].(string)
    
    // Check if user already exists
    if user, exists := userDB[userID]; exists {
        return user, nil // Return existing user
    }
    
    // Create new user
    user := createUser(params)
    userDB[userID] = user
    return user, nil
})
```

### 2. Circuit Breaker Pattern

Implement circuit breakers for resilience:

```go
type CircuitBreaker struct {
    failures    int
    threshold   int
    timeout     time.Duration
    lastAttempt time.Time
    state       string // "closed", "open", "half-open"
}

func (cb *CircuitBreaker) Call(fn func() error) error {
    if cb.state == "open" {
        if time.Since(cb.lastAttempt) > cb.timeout {
            cb.state = "half-open"
        } else {
            return fmt.Errorf("circuit breaker is open")
        }
    }
    
    err := fn()
    if err != nil {
        cb.failures++
        if cb.failures >= cb.threshold {
            cb.state = "open"
            cb.lastAttempt = time.Now()
        }
        return err
    }
    
    cb.failures = 0
    cb.state = "closed"
    return nil
}
```

### 3. Monitoring and Metrics

Add metrics for monitoring:

```go
type RPCMetrics struct {
    RequestCount    int64
    ResponseTime    time.Duration
    ErrorCount      int64
    TimeoutCount    int64
}

func (m *RPCMetrics) RecordRequest(duration time.Duration, err error) {
    atomic.AddInt64(&m.RequestCount, 1)
    
    if err != nil {
        atomic.AddInt64(&m.ErrorCount, 1)
        if strings.Contains(err.Error(), "timeout") {
            atomic.AddInt64(&m.TimeoutCount, 1)
        }
    }
}
```

### 4. Security Considerations

Implement proper authentication and authorization:

```go
func (s *RPCServer) authenticateRequest(d rabbitmq.Delivery) error {
    token := d.Headers["Authorization"]
    if token == nil {
        return fmt.Errorf("missing authorization header")
    }
    
    // Validate token
    return validateToken(token.(string))
}
```

## Conclusion

The RPC pattern over RabbitMQ provides a powerful way to implement synchronous communication in distributed systems. While it adds complexity compared to simple async messaging, it offers the familiar request-response semantics that many applications require.

Key takeaways:
- Use correlation IDs to match requests with responses
- Implement proper timeout handling at multiple levels
- Consider performance implications and optimize accordingly
- Choose RPC vs async messaging based on your specific requirements
- Follow best practices for reliability and maintainability

The implementation shown here provides a solid foundation that can be extended with additional features like authentication, metrics, circuit breakers, and more sophisticated error handling as needed.