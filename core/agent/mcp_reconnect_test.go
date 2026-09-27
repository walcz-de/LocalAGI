package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/LocalAGI/core/types"
)

// swappableMCP serves an MCP server behind a fixed URL; swap() stands in for a
// server restart: the new handler knows none of the old session IDs and offers
// a different tool.
type swappableMCP struct {
	mu sync.Mutex
	h  http.Handler
}

func (s *swappableMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	h := s.h
	s.mu.Unlock()
	h.ServeHTTP(w, r)
}

func (s *swappableMCP) swap(toolName string) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: toolName, Description: "test tool"},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
		})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	s.mu.Lock()
	s.h = h
	s.mu.Unlock()
}

func mcpToolNames(a *Agent) []string {
	var names []string
	for _, act := range a.mcpActionDefinitions {
		names = append(names, string(act.Definition().Name))
	}
	return names
}

func newMCPTestAgent(t *testing.T, url string) *Agent {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a := &Agent{context: &types.ActionContext{Context: ctx}, options: &options{mcpServers: []MCPServer{{URL: url}}}}
	if err := a.initMCPActions(); err != nil {
		t.Fatalf("initMCPActions: %v", err)
	}
	t.Cleanup(a.closeMCPServers)
	return a
}

// TestEnsureMCPSessionsReconnectsAfterServerRestart: after the MCP server
// restarts, the next run must reconnect and see the server's current tools.
func TestEnsureMCPSessionsReconnectsAfterServerRestart(t *testing.T) {
	sw := &swappableMCP{}
	sw.swap("tool_before")
	ts := httptest.NewServer(sw)
	t.Cleanup(ts.Close) // before the agent's cleanup is registered: sessions close first, then the server

	a := newMCPTestAgent(t, ts.URL)
	if got := mcpToolNames(a); len(got) != 1 || got[0] != "tool_before" {
		t.Fatalf("before restart: want [tool_before], got %v", got)
	}

	sw.swap("tool_after") // "restart": old session unknown, new tool list
	start := time.Now()
	a.ensureMCPSessions(context.Background())
	if d := time.Since(start); d > 15*time.Second {
		t.Fatalf("reconnect took %s — a run would stall that long after every server restart", d)
	}

	if got := mcpToolNames(a); len(got) != 1 || got[0] != "tool_after" {
		t.Fatalf("after restart: want [tool_after], got %v (the agent kept its dead session)", got)
	}
	if len(a.mcpSessions) != 1 {
		t.Fatalf("want exactly 1 session after reconnect, got %d", len(a.mcpSessions))
	}
	if err := a.mcpSessions[0].Ping(context.Background(), nil); err != nil {
		t.Fatalf("reconnected session does not answer: %v", err)
	}
}

// TestEnsureMCPSessionsKeepsLiveSessions: a live session must be kept as is -
// no reconnect on every run.
func TestEnsureMCPSessionsKeepsLiveSessions(t *testing.T) {
	sw := &swappableMCP{}
	sw.swap("tool_live")
	ts := httptest.NewServer(sw)
	t.Cleanup(ts.Close) // before the agent's cleanup is registered: sessions close first, then the server

	a := newMCPTestAgent(t, ts.URL)
	before := a.mcpSessions[0]
	a.ensureMCPSessions(context.Background())
	if len(a.mcpSessions) != 1 || a.mcpSessions[0] != before {
		t.Fatalf("a live session was replaced")
	}
}

// TestInitMCPActionsDoesNotDuplicateExtraSessions: re-initialising must not add
// the pre-connected extra sessions a second time.
func TestInitMCPActionsDoesNotDuplicateExtraSessions(t *testing.T) {
	sw := &swappableMCP{}
	sw.swap("tool_extra")
	ts := httptest.NewServer(sw)
	t.Cleanup(ts.Close) // before the agent's cleanup is registered: sessions close first, then the server

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	extra, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatalf("connect extra: %v", err)
	}
	t.Cleanup(func() { extra.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &Agent{context: &types.ActionContext{Context: ctx}, options: &options{extraMCPSessions: []*mcp.ClientSession{extra}}}
	for i := 0; i < 3; i++ {
		if err := a.initMCPActions(); err != nil {
			t.Fatalf("initMCPActions #%d: %v", i, err)
		}
	}
	if len(a.mcpSessions) != 1 {
		t.Fatalf("want the extra session once, got %d sessions", len(a.mcpSessions))
	}
}
