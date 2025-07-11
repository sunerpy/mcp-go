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

type callStats struct {
	startTime     time.Time
	progressCount int32
}

func setupClient(ctx context.Context, onNotify func(mcp.JSONRPCNotification)) *client.Client {
	serverURL := "http://localhost:6655/mcp"
	trans, err := transport.NewStreamableHTTP(
		serverURL,
		transport.WithContinuousListening(),
	)
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
				Name:    "logging-client-example",
				Version: "1.0.0",
			},
		},
	}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		log.Fatalf("initialize error: %v", err)
	}

	setReq := mcp.SetLevelRequest{
		Params: mcp.SetLevelParams{Level: mcp.LoggingLevelDebug},
	}
	if err := c.SetLevel(ctx, setReq); err != nil {
		log.Fatalf("set level error: %v", err)
	}
	return c
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	var globalProgress int32
	var globalMessages int32
	var wg sync.WaitGroup
	callTrack := sync.Map{}
	var counter int32
	generateID := func() string {
		return fmt.Sprintf("call-%d", atomic.AddInt32(&counter, 1))
	}

	onNotify := func(n mcp.JSONRPCNotification) {
		switch n.Method {
		case "notifications/progress":
			atomic.AddInt32(&globalProgress, 1)
			if cid, ok := n.Params.AdditionalFields["call_id"].(string); ok {
				if v, exists := callTrack.Load(cid); exists {
					stats := v.(*callStats)
					atomic.AddInt32(&stats.progressCount, 1)
				}
			}
		case "notifications/message":
			atomic.AddInt32(&globalMessages, 1)
			if data, ok := n.Params.AdditionalFields["data"].(map[string]any); ok {
				if cid, ok := data["call_id"].(string); ok {
					if v, exists := callTrack.LoadAndDelete(cid); exists {
						stats := v.(*callStats)
						log.Printf("Call %s completed (%d progress updates) in %v", cid, stats.progressCount, time.Since(stats.startTime))
					}
				}
			}
		default:
			log.Printf("Notification %s -> %v", n.Method, n.Params)
		}
	}

	c := setupClient(ctx, onNotify)
	defer c.Close()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cid := generateID()
			callTrack.Store(cid, &callStats{startTime: time.Now()})
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				req := mcp.CallToolRequest{
					Params: mcp.CallToolParams{
						Name: "add_numbers",
						Arguments: map[string]any{
							"a":       rand.Intn(100),
							"b":       rand.Intn(100),
							"call_id": id,
						},
					},
				}
				if _, err := c.CallTool(ctx, req); err != nil && !errors.Is(err, context.Canceled) {
					log.Printf("tool call error: %v", err)
					callTrack.Delete(id)
				}
			}(cid)
		case <-ctx.Done():
			wg.Wait()
			log.Printf("Total progress notifications: %d", atomic.LoadInt32(&globalProgress))
			log.Printf("Total final messages: %d", atomic.LoadInt32(&globalMessages))
			return
		}
	}
}
