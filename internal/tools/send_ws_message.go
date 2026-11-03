package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/c0tton-fluff/caido-mcp-server/v4/internal/httputil"
	"github.com/c0tton-fluff/caido-mcp-server/v4/internal/replay"
	caido "github.com/caido-community/sdk-go"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SendWsMessageInput is the input for caido_send_ws_message.
type SendWsMessageInput struct {
	SessionID       string `json:"sessionId" jsonschema:"required,WebSocket replay session ID (kind ws)"`
	Payload         string `json:"payload" jsonschema:"required,Message payload (text, or base64 when payloadEncoding is base64)"`
	PayloadEncoding string `json:"payloadEncoding,omitempty" jsonschema:"Payload is text (default) or base64"`
	Direction       string `json:"direction,omitempty" jsonschema:"Frame direction: SERVER (default) or CLIENT"`
	Format          string `json:"format,omitempty" jsonschema:"Frame format: TEXT (default), BINARY, PING, PONG or CLOSE"`
	WaitMillis      int    `json:"waitMillis,omitempty" jsonschema:"How long to wait for replies after sending, in ms (default 1500, max 30000)"`
	MessageLimit    int    `json:"messageLimit,omitempty" jsonschema:"Frames to return (default 20, max 100)"`
	BodyLimit       int    `json:"bodyLimit,omitempty" jsonschema:"Max bytes of each frame body (default 2000)"`
}

// SendWsMessageOutput is the output of caido_send_ws_message.
type SendWsMessageOutput struct {
	SessionID string        `json:"sessionId"`
	TaskID    string        `json:"taskId"`
	EntryID   string        `json:"entryId"`
	Sent      *WsFrameView  `json:"sent,omitempty"`
	Messages  []WsFrameView `json:"messages"`
	Live      bool          `json:"live"`
}

// sendWsMessageHandler runs one WebSocket replay cycle against a session:
// open the connection, write the frame, collect what comes back, close.
//
// Caido creates a fresh entry for every startReplayTask, and records both
// directions in that entry, so the frames of this run are read back from the
// task's entry rather than the session.
func sendWsMessageHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, SendWsMessageInput) (*mcp.CallToolResult, SendWsMessageOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input SendWsMessageInput,
	) (*mcp.CallToolResult, SendWsMessageOutput, error) {
		if input.SessionID == "" {
			return nil, SendWsMessageOutput{}, fmt.Errorf("sessionId is required")
		}
		if err := checkRawSize("payload", input.Payload); err != nil {
			return nil, SendWsMessageOutput{}, err
		}
		direction, err := wsDirection(input.Direction)
		if err != nil {
			return nil, SendWsMessageOutput{}, err
		}
		format, err := wsFormat(input.Format)
		if err != nil {
			return nil, SendWsMessageOutput{}, err
		}
		encoding, err := wsPayloadEncoding(input.PayloadEncoding)
		if err != nil {
			return nil, SendWsMessageOutput{}, err
		}
		raw, err := encodeWsFrame(input.Payload, encoding)
		rendering := encodingForDraft(input.PayloadEncoding)
		if err != nil {
			return nil, SendWsMessageOutput{}, err
		}
		wait := input.WaitMillis
		if wait <= 0 {
			wait = 1500
		}
		if wait > 30000 {
			wait = 30000
		}
		bodyLimit := input.BodyLimit
		if bodyLimit <= 0 {
			bodyLimit = httputil.DefaultBodyLimit
		}
		msgLimit := clampLimit(input.MessageLimit, 20, 100)

		task, err := replay.StartWsTask(ctx, client, input.SessionID)
		if err != nil {
			return nil, SendWsMessageOutput{}, err
		}
		output := SendWsMessageOutput{
			SessionID: input.SessionID,
			TaskID:    task.ID,
			EntryID:   task.EntryID,
		}

		// Always close the connection: a task left running keeps a socket to
		// the target open until Caido exits.
		defer func() {
			// WithoutCancel: the close must happen even when the caller's
			// context was cancelled mid-run.
			_ = replay.StopWsTasks(
				context.WithoutCancel(ctx), client, []string{task.ID},
			)
		}()

		sent, err := replay.SendWsMessage(
			ctx, client, task.ID, raw, direction, format,
		)
		if err != nil {
			return nil, SendWsMessageOutput{}, err
		}
		sentBody, sentEncoding, sentSize, sentTruncated := renderWsFrame(
			raw, bodyLimit, rendering,
		)
		output.Sent = &WsFrameView{
			ID:           sent.ID,
			Direction:    sent.Direction,
			Format:       sent.Format,
			Length:       sent.Length,
			Body:         sentBody,
			BodyEncoding: sentEncoding,
			BodySize:     sentSize,
			Truncated:    sentTruncated,
			CreatedAt:    sent.CreatedAt,
		}

		// Poll the task's entry for the frames this run produced. A reply
		// usually lands within a few hundred milliseconds; a target that sends
		// nothing simply yields the sent frame and whatever arrived.
		deadline := time.Now().Add(time.Duration(wait) * time.Millisecond)
		var entry *replay.WsEntry
		for {
			entry, err = replay.GetWsEntry(ctx, client, task.EntryID)
			if err != nil {
				return nil, SendWsMessageOutput{}, err
			}
			answered := false
			for _, m := range entry.Messages {
				if m.ID != sent.ID {
					answered = true
					break
				}
			}
			if answered || time.Now().After(deadline) {
				break
			}
			select {
			case <-ctx.Done():
				return nil, SendWsMessageOutput{}, ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}

		output.Live = entry.Error == ""
		for i, m := range entry.Messages {
			if i >= msgLimit {
				break
			}
			body, bodyEncoding, size, truncated := renderWsFrame(
				m.Raw, bodyLimit, rendering,
			)
			output.Messages = append(output.Messages, WsFrameView{
				ID:           m.ID,
				Direction:    m.Direction,
				Format:       m.Format,
				Length:       m.Length,
				Body:         body,
				BodyEncoding: bodyEncoding,
				BodySize:     size,
				Truncated:    truncated,
				CreatedAt:    m.CreatedAt,
			})
		}
		if output.Messages == nil {
			output.Messages = []WsFrameView{}
		}
		return nil, output, nil
	}
}

// RegisterSendWsMessageTool registers the tool with the MCP server.
func RegisterSendWsMessageTool(server *mcp.Server, client *caido.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:  "caido_send_ws_message",
		Title: "Send WebSocket Message",
		Description: `Send one frame on a WebSocket replay session: Caido opens the connection, ` +
			`writes the payload, collects what the target sends back for waitMillis, then closes it. ` +
			`The run is recorded as a new replay entry (returned as entryId; read it later with ` +
			`caido_get_replay_entry). Payload is text by default, base64 for binary frames. ` +
			`Direction follows Caido's masking: SERVER (default) is sent as a masked client frame, ` +
			`which a target server accepts; CLIENT is sent unmasked as a server frame. ` +
			`This sends traffic to the target; stay in scope.`,
		InputSchema: schemaFor[SendWsMessageInput](func(s *jsonschema.Schema) {
			if p := prop(s, "payloadEncoding"); p != nil {
				p.Enum = []any{"text", "base64"}
			}
			if p := prop(s, "direction"); p != nil {
				p.Enum = []any{"CLIENT", "SERVER"}
			}
			if p := prop(s, "format"); p != nil {
				p.Enum = []any{"TEXT", "BINARY", "PING", "PONG", "CLOSE"}
			}
		}),
		Annotations: writeAnn(false, false, true),
	}, sendWsMessageHandler(client))
}
