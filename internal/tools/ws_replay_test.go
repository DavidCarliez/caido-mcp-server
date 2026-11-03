package tools_test

import (
	"encoding/base64"
	"testing"

	caido "github.com/caido-community/sdk-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/c0tton-fluff/caido-mcp-server/v4/internal/testutil"
	"github.com/c0tton-fluff/caido-mcp-server/v4/internal/tools"
)

// wsEntryData builds a GetReplayEntryWs response for a WebSocket replay entry.
func wsEntryData(entryID string, draft map[string]any, messages ...map[string]any) map[string]any {
	entry := map[string]any{
		"__typename": "ReplayEntryWs",
		"id":         entryID,
		"error":      nil,
		"createdAt":  int64(1793734476143),
		"http": map[string]any{
			"raw": base64.StdEncoding.EncodeToString([]byte(
				"GET /amb HTTP/1.1\r\nHost: dev.example.com\r\n" +
					"Upgrade: websocket\r\nConnection: Upgrade\r\n\r\n",
			)),
			"connection": map[string]any{
				"host": "dev.example.com", "port": 443, "isTLS": true,
			},
			"request": map[string]any{
				"response": map[string]any{
					"statusCode":    101,
					"roundtripTime": 12,
					"raw": base64.StdEncoding.EncodeToString([]byte(
						"HTTP/1.1 101 Switching Protocols\r\n\r\n",
					)),
				},
			},
		},
		"draft":  draft,
		"stream": map[string]any{"id": "stream-1"},
	}
	msgs := make([]any, len(messages))
	for i, m := range messages {
		messages[i] = m
		msgs[i] = m
	}
	entry["messages"] = messages
	return map[string]any{"replayEntry": entry}
}

func wsMessage(id, direction, payload string) map[string]any {
	return map[string]any{
		"id": id,
		"head": map[string]any{
			"raw":       base64.StdEncoding.EncodeToString([]byte(payload)),
			"direction": direction,
			"format":    "TEXT",
			"length":    len(payload),
			"createdAt": int64(1793734500000),
		},
	}
}

// TestGetReplayEntryWsTool reads a WebSocket entry: handshake, draft, frames.
func TestGetReplayEntryWsTool(t *testing.T) {
	env := testutil.NewMCPTestEnv(t, func(s *mcp.Server, c *caido.Client) {
		tools.RegisterGetReplayEntryTool(s, c)
	})
	env.Mock.ValidateAgainstSchema()
	env.Mock.On("GetReplayEntryWs", wsEntryData(
		"entry-ws",
		map[string]any{
			"raw":         base64.StdEncoding.EncodeToString([]byte(`{"ping":1}`)),
			"direction":   "SERVER",
			"format":      "TEXT",
			"editorState": "",
		},
		wsMessage("m1", "SERVER", `{"ping":1}`),
		wsMessage("m2", "CLIENT", `{"pong":1}`),
	))

	result := env.CallTool(t, "caido_get_replay_entry", map[string]any{
		"id": "entry-ws", "sessionKind": "WS",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	out := testutil.UnmarshalToolResult[tools.GetReplayEntryOutput](t, result)

	if out.Kind != "ws" {
		t.Errorf("kind = %q, want ws", out.Kind)
	}
	if out.StatusCode != 101 {
		t.Errorf("statusCode = %d, want 101 (handshake response)", out.StatusCode)
	}
	if out.Host != "dev.example.com" || out.StreamID != "stream-1" {
		t.Errorf("host/stream = %q/%q", out.Host, out.StreamID)
	}
	if out.Draft == nil {
		t.Fatal("draft missing")
	}
	if out.Draft.Body != `{"ping":1}` || out.Draft.Direction != "SERVER" ||
		out.Draft.Format != "TEXT" {
		t.Errorf("draft = %+v", *out.Draft)
	}
	if out.MessageCount != 2 || len(out.Messages) != 2 {
		t.Fatalf("messages = %d/%d, want 2", len(out.Messages), out.MessageCount)
	}
	if out.Messages[1].Body != `{"pong":1}` || out.Messages[1].Direction != "CLIENT" {
		t.Errorf("message[1] = %+v", out.Messages[1])
	}
}

// TestGetReplayEntryAutoDetectsWs covers the default path: the HTTP query
// answers null for a WebSocket entry, so the tool retries as WS.
func TestGetReplayEntryAutoDetectsWs(t *testing.T) {
	env := testutil.NewMCPTestEnv(t, func(s *mcp.Server, c *caido.Client) {
		tools.RegisterGetReplayEntryTool(s, c)
	})
	env.Mock.On("GetReplayEntry", map[string]any{"replayEntry": nil})
	env.Mock.On("GetReplayEntryWs", wsEntryData("entry-ws", nil))

	result := env.CallTool(t, "caido_get_replay_entry", map[string]any{
		"id": "entry-ws",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	out := testutil.UnmarshalToolResult[tools.GetReplayEntryOutput](t, result)
	if out.Kind != "ws" || out.ID != "entry-ws" {
		t.Fatalf("kind/id = %q/%q, want ws/entry-ws", out.Kind, out.ID)
	}
	if out.Draft != nil {
		t.Errorf("draft should be absent without one: %+v", *out.Draft)
	}
}

// TestSetWsMessageDraftTool writes a draft and asserts the exact GraphQL
// variables, including the reused editorState and the settings object.
func TestSetWsMessageDraftTool(t *testing.T) {
	env := testutil.NewMCPTestEnv(t, func(s *mcp.Server, c *caido.Client) {
		tools.RegisterSetWsMessageDraftTool(s, c)
	})
	env.Mock.ValidateAgainstSchema()
	env.Mock.On("GetReplayEntryWs", wsEntryData("entry-ws", map[string]any{
		"raw":         base64.StdEncoding.EncodeToString([]byte("old")),
		"direction":   "SERVER",
		"format":      "TEXT",
		"editorState": "ed-state",
	}))
	env.Mock.On("SetReplayEntryWsDraft", map[string]any{
		"updateReplayEntryDraft": map[string]any{
			"entry": map[string]any{
				"__typename": "ReplayEntryWs",
				"draft": map[string]any{
					"raw":       base64.StdEncoding.EncodeToString([]byte(`{"hi":"there"}`)),
					"direction": "SERVER",
					"format":    "TEXT",
				},
			},
		},
	})

	result := env.CallTool(t, "caido_set_ws_message_draft", map[string]any{
		"id": "entry-ws", "payload": `{"hi":"there"}`, "serverTimeoutMs": 7000,
	})
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	out := testutil.UnmarshalToolResult[tools.SetWsMessageDraftOutput](t, result)
	if !out.Applied || out.Draft.Body != `{"hi":"there"}` ||
		out.Draft.Direction != "SERVER" || out.Draft.Format != "TEXT" {
		t.Fatalf("draft = %+v applied=%v", out.Draft, out.Applied)
	}

	vars := env.Mock.LastVariables("SetReplayEntryWsDraft")
	if vars == nil {
		t.Fatal("no variables captured for SetReplayEntryWsDraft")
	}
	input, _ := vars["input"].(map[string]any)
	ws, _ := input["ws"].(map[string]any)
	if got, _ := ws["raw"].(string); got != base64.StdEncoding.EncodeToString([]byte(`{"hi":"there"}`)) {
		t.Errorf("raw = %q", got)
	}
	if ws["direction"] != "SERVER" || ws["format"] != "TEXT" {
		t.Errorf("direction/format = %v/%v", ws["direction"], ws["format"])
	}
	if ws["editorState"] != "ed-state" {
		t.Errorf("editorState = %v, want the existing state", ws["editorState"])
	}
	settings, _ := ws["settings"].(map[string]any)
	if ms, _ := settings["serverTimeoutMs"].(float64); ms != 7000 {
		t.Errorf("serverTimeoutMs = %v, want 7000", settings["serverTimeoutMs"])
	}
}

// TestSetWsMessageDraftRejects covers local validation: bad direction and bad
// base64 payload must fail before any GraphQL call.
func TestSetWsMessageDraftRejects(t *testing.T) {
	cases := []struct {
		name  string
		input map[string]any
	}{
		{"bad direction", map[string]any{
			"id": "e1", "payload": "x", "direction": "UP",
		}},
		{"bad format", map[string]any{
			"id": "e1", "payload": "x", "format": "JSON",
		}},
		{"bad base64", map[string]any{
			"id": "e1", "payload": "not base64!!", "payloadEncoding": "base64",
		}},
		{"missing payload", map[string]any{"id": "e1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewMCPTestEnv(t, func(s *mcp.Server, c *caido.Client) {
				tools.RegisterSetWsMessageDraftTool(s, c)
			})
			result := env.CallTool(t, "caido_set_ws_message_draft", tc.input)
			if !result.IsError {
				t.Fatalf("expected error, got %v", result.Content)
			}
		})
	}
}

// TestClearWsMessageDraftTool covers the clear mutation.
func TestClearWsMessageDraftTool(t *testing.T) {
	env := testutil.NewMCPTestEnv(t, func(s *mcp.Server, c *caido.Client) {
		tools.RegisterClearWsMessageDraftTool(s, c)
	})
	env.Mock.ValidateAgainstSchema()
	env.Mock.On("ClearReplayWsDraft", map[string]any{
		"clearReplayEntryDraft": map[string]any{
			"entry": map[string]any{"__typename": "ReplayEntryWs", "id": "entry-ws"},
		},
	})

	result := env.CallTool(t, "caido_clear_ws_message_draft", map[string]any{
		"id": "entry-ws",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	out := testutil.UnmarshalToolResult[tools.ClearWsMessageDraftOutput](t, result)
	if !out.Cleared || out.EntryID != "entry-ws" {
		t.Fatalf("output = %+v", out)
	}
	vars := env.Mock.LastVariables("ClearReplayWsDraft")
	if vars["kind"] != "WS" {
		t.Errorf("kind variable = %v, want WS", vars["kind"])
	}
}

// TestSendWsMessageTool runs one send cycle: start task, write the frame, read
// the entry back, stop the task.
func TestSendWsMessageTool(t *testing.T) {
	env := testutil.NewMCPTestEnv(t, func(s *mcp.Server, c *caido.Client) {
		tools.RegisterSendWsMessageTool(s, c)
	})
	env.Mock.ValidateAgainstSchema()
	env.Mock.On("StartReplayTask", map[string]any{
		"startReplayTask": map[string]any{
			"task": map[string]any{
				"id":          "task-9",
				"sessionKind": "WS",
				"replayEntry": map[string]any{
					"__typename": "ReplayEntryWs", "id": "entry-run",
				},
			},
			"error": nil,
		},
	})
	env.Mock.On("SendReplayTaskMessage", map[string]any{
		"sendReplayTaskMessage": map[string]any{
			"message": map[string]any{
				"__typename": "StreamWsMessage",
				"id":         "m-sent",
				"head": map[string]any{
					"raw":       base64.StdEncoding.EncodeToString([]byte("hello")),
					"direction": "SERVER",
					"format":    "TEXT",
					"length":    5,
					"createdAt": int64(1793734500000),
				},
			},
			"error": nil,
		},
	})
	env.Mock.On("GetReplayEntryWs", wsEntryData(
		"entry-run", nil,
		wsMessage("m-sent", "SERVER", "hello"),
		wsMessage("m-reply", "CLIENT", "echo:hello"),
	))
	env.Mock.On("StopReplayWsTasks", map[string]any{
		"stopReplayWsTasks": map[string]any{"error": nil},
	})

	result := env.CallTool(t, "caido_send_ws_message", map[string]any{
		"sessionId": "58", "payload": "hello", "waitMillis": 1,
	})
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	out := testutil.UnmarshalToolResult[tools.SendWsMessageOutput](t, result)
	if out.TaskID != "task-9" || out.EntryID != "entry-run" {
		t.Fatalf("task/entry = %q/%q", out.TaskID, out.EntryID)
	}
	if out.Sent == nil || out.Sent.Body != "hello" {
		t.Fatalf("sent = %+v", out.Sent)
	}
	if len(out.Messages) != 2 || out.Messages[1].Body != "echo:hello" {
		t.Fatalf("messages = %+v", out.Messages)
	}
	if !out.Live {
		t.Error("live = false, want true for an entry without error")
	}

	vars := env.Mock.LastVariables("SendReplayTaskMessage")
	input, _ := vars["input"].(map[string]any)
	ws, _ := input["ws"].(map[string]any)
	if ws["direction"] != "SERVER" {
		t.Errorf("send direction = %v, want SERVER default", ws["direction"])
	}
	stopVars := env.Mock.LastVariables("StopReplayWsTasks")
	ids, _ := stopVars["taskIds"].([]any)
	if len(ids) != 1 || ids[0] != "task-9" {
		t.Errorf("stopped tasks = %v, want [task-9]", stopVars["taskIds"])
	}
}

// TestSendWsMessageReportsTaskErrors covers Caido's error unions: a dead task
// must surface as a readable error, not an empty success.
func TestSendWsMessageReportsTaskErrors(t *testing.T) {
	env := testutil.NewMCPTestEnv(t, func(s *mcp.Server, c *caido.Client) {
		tools.RegisterSendWsMessageTool(s, c)
	})
	env.Mock.On("StartReplayTask", map[string]any{
		"startReplayTask": map[string]any{"task": nil, "error": nil},
	})
	result := env.CallTool(t, "caido_send_ws_message", map[string]any{
		"sessionId": "58", "payload": "x",
	})
	if !result.IsError {
		t.Fatalf("expected error, got %v", result.Content)
	}
}

// wsStreamEdge builds one streams edge with the given protocol.
func wsStreamEdge(id, protocol, cursor string) map[string]any {
	return map[string]any{
		"cursor": cursor,
		"node": map[string]any{
			"id":        id,
			"host":      "ws.example.com",
			"port":      443,
			"path":      "/socket",
			"isTls":     true,
			"direction": "BOTH",
			"source":    "PROXY",
			"protocol":  protocol,
			"createdAt": int64(1714900000000),
		},
	}
}

// TestListWsStreamsPaginatesAcrossMixedPages covers the client-side protocol
// filter: a page holding mostly HTTP traffic must not end the listing before
// the requested number of WebSocket streams is reached.
func TestListWsStreamsPaginatesAcrossMixedPages(t *testing.T) {
	page := func(edges []map[string]any, hasNext bool, endCursor any) map[string]any {
		return map[string]any{
			"streams": map[string]any{
				"edges": edges,
				"pageInfo": map[string]any{
					"hasNextPage": hasNext,
					"endCursor":   endCursor,
				},
			},
		}
	}

	env := testutil.NewMCPTestEnv(t, func(s *mcp.Server, c *caido.Client) {
		tools.RegisterListWsStreamsTool(s, c)
	})
	// Page 1: one HTTP stream, one WS stream.
	env.Mock.On("ListStreams", page([]map[string]any{
		wsStreamEdge("s-http", "HTTP", "c1"),
		wsStreamEdge("s-ws1", "WS", "c2"),
	}, true, "c2"))
	// Page 2: two WS streams, no further pages.
	env.Mock.On("ListStreams", page([]map[string]any{
		wsStreamEdge("s-ws2", "WS", "c3"),
		wsStreamEdge("s-ws3", "WS", "c4"),
	}, false, nil))

	result := env.CallTool(t, "caido_list_ws_streams", map[string]any{"limit": 3})
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	out := testutil.UnmarshalToolResult[tools.ListWsStreamsOutput](t, result)
	if len(out.Streams) != 3 {
		t.Fatalf("streams = %d, want 3", len(out.Streams))
	}
	if out.Streams[0].ID != "s-ws1" || out.Streams[2].ID != "s-ws3" {
		t.Errorf("ids = %q..%q", out.Streams[0].ID, out.Streams[2].ID)
	}
	if out.HasMore {
		t.Errorf("hasMore = true, want false at the end of the listing")
	}
}

// TestListWsStreamsStopsAtLimit covers truncation mid-page: the cursor must
// point at the last consumed edge so the next call continues there.
func TestListWsStreamsStopsAtLimit(t *testing.T) {
	env := testutil.NewMCPTestEnv(t, func(s *mcp.Server, c *caido.Client) {
		tools.RegisterListWsStreamsTool(s, c)
	})
	env.Mock.On("ListStreams", map[string]any{
		"streams": map[string]any{
			"edges": []map[string]any{
				wsStreamEdge("s-ws1", "WS", "c1"),
				wsStreamEdge("s-ws2", "WS", "c2"),
			},
			"pageInfo": map[string]any{"hasNextPage": true, "endCursor": "c2"},
		},
	})

	result := env.CallTool(t, "caido_list_ws_streams", map[string]any{"limit": 1})
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	out := testutil.UnmarshalToolResult[tools.ListWsStreamsOutput](t, result)
	if len(out.Streams) != 1 || out.Streams[0].ID != "s-ws1" {
		t.Fatalf("streams = %+v", out.Streams)
	}
	if !out.HasMore || out.NextCursor != "c1" {
		t.Fatalf("hasMore/nextCursor = %v/%q, want true/c1", out.HasMore, out.NextCursor)
	}
}
