package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

func setupClient(ctx context.Context, onNotify func(mcp.JSONRPCNotification)) *client.Client {
	serverURL := "http://localhost:6655/mcp"
	trans, err := transport.NewStreamableHTTP(serverURL, transport.WithContinuousListening())
	if err != nil {
		log.Fatalf("transport error: %v", err)
	}
	c := client.NewClient(trans)
	c.OnNotification(onNotify)
	if err := c.Start(ctx); err != nil {
		log.Fatalf("start error: %v", err)
	}

	initReq := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "add-client",
				Version: "1.0.0",
			},
		},
	}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		log.Fatalf("initialize error: %v", err)
	}

	return c
}

type callStats struct {
	startTime     time.Time
	progressCount int32
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var totalCalls int32
	var wg sync.WaitGroup
	var globalProgress int32
	var globalMessages int32
	// callTrack := sync.Map{}
	onNotify := func(n mcp.JSONRPCNotification) {
		log.Printf("Got %s: %v", n.Method, n.Params)
		return
		switch n.Method {
		case "notifications/progress":
			token := n.Params.AdditionalFields["progressToken"]
			callID := n.Params.AdditionalFields["call_id"]
			atomic.AddInt32(&globalProgress, 1)
			log.Printf("Progress received: token=%v, call_id=%v, detail=%v", token, callID, n.Params)

		case "notifications/message":
			callID := ""
			if data, ok := n.Params.AdditionalFields["data"].(map[string]any); ok {
				callID, _ = data["call_id"].(string)
			}
			log.Printf("Message received for call_id=%s: %v", callID, n.Params)

		default:
			log.Printf("Other notification: %s -> %v", n.Method, n.Params)
		}
	}

	c := setupClient(ctx, onNotify)
	defer c.Close()
	setReq := mcp.SetLevelRequest{
		Params: mcp.SetLevelParams{
			Level: mcp.LoggingLevelDebug,
		},
	}
	c.SetLevel(ctx, setReq)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	log.Println("Starting periodic 'add' tool calls")
	for {
		select {
		case <-ticker.C:
			wg.Add(1)
			go func() {
				defer wg.Done()
				a, b := rand.Intn(100), rand.Intn(100)
				progressToken := fmt.Sprintf("token-%d", time.Now().UnixNano())
				callID := fmt.Sprintf("call-%d", rand.Int())
				req := mcp.CallToolRequest{
					Params: mcp.CallToolParams{
						Name: "add",
						Arguments: map[string]any{
							"a": a,
							"b": b,
						},
						Meta: &mcp.Meta{
							ProgressToken: progressToken,
							AdditionalFields: map[string]any{
								"call_id": callID,
							},
						},
					},
				}
				res, err := c.CallTool(ctx, req)
				if err != nil && !errors.Is(err, context.Canceled) {
					log.Printf("add tool error: %v", err)
					return
				}
				atomic.AddInt32(&totalCalls, 1)
				if len(res.Content) > 0 {
					// log.Printf("[Add Result] a=%d, b=%d → %v", a, b, res.Content[0])
				}
			}()
		case <-ctx.Done():
			wg.Wait()
			log.Printf("Total add tool calls: %d", atomic.LoadInt32(&totalCalls))
			log.Printf("Done.msg: %d; process: %d;", atomic.LoadInt32(&globalMessages), atomic.LoadInt32(&globalProgress))
			return
		}
	}
}
