package tools

import (
	"context"
	"fmt"

	caido "github.com/caido-community/sdk-go"
	gen "github.com/caido-community/sdk-go/graphql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ListWsStreamsInput is the input for the list_ws_streams tool
type ListWsStreamsInput struct {
	Limit   int    `json:"limit,omitempty" jsonschema:"Maximum number of streams to return (default 20, max 100)"`
	After   string `json:"after,omitempty" jsonschema:"Cursor for pagination from previous response nextCursor"`
	ScopeID string `json:"scope_id,omitempty" jsonschema:"Optional scope ID to filter streams"`
}

// WsStreamSummary is a minimal representation of a WebSocket stream
type WsStreamSummary struct {
	ID        string `json:"id"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Path      string `json:"path"`
	IsTLS     bool   `json:"isTls"`
	Direction string `json:"direction"`
	Source    string `json:"source"`
	CreatedAt int64  `json:"createdAt"`
}

// ListWsStreamsOutput is the output of the list_ws_streams tool
type ListWsStreamsOutput struct {
	Streams    []WsStreamSummary `json:"streams"`
	HasMore    bool              `json:"hasMore"`
	NextCursor string            `json:"nextCursor,omitempty"`
}

// maxWsStreamPages bounds how many upstream pages one call reads while it
// looks for WebSocket streams, so a project holding almost nothing but HTTP
// traffic cannot turn one tool call into an unbounded scan.
const maxWsStreamPages = 20

func listWsStreamsHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, ListWsStreamsInput) (*mcp.CallToolResult, ListWsStreamsOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input ListWsStreamsInput,
	) (*mcp.CallToolResult, ListWsStreamsOutput, error) {
		limit := clampLimit(input.Limit, 20, 100)

		// Caido 0.57 removed the protocol query argument, so WebSocket
		// streams are filtered client-side. Filtering a single page is not
		// enough: a page of mixed streams can hold one WebSocket and stop
		// early, so pages are read until the requested count is met.
		output := ListWsStreamsOutput{Streams: []WsStreamSummary{}}
		after := input.After
		lastCursor := input.After

		for range maxWsStreamPages {
			opts := &caido.ListStreamsOptions{First: &limit}
			if after != "" {
				opts.After = &after
			}
			if input.ScopeID != "" {
				opts.ScopeID = &input.ScopeID
			}

			resp, err := client.Streams.List(ctx, opts)
			if err != nil {
				return nil, ListWsStreamsOutput{}, fmt.Errorf(
					"failed to list ws streams: %w", err,
				)
			}

			edges := resp.Streams.Edges
			if len(edges) == 0 {
				return nil, output, nil
			}
			for _, edge := range edges {
				n := edge.Node
				if n.Protocol == gen.StreamProtocolWs {
					if len(output.Streams) == limit {
						output.HasMore = true
						output.NextCursor = lastCursor
						return nil, output, nil
					}
					output.Streams = append(output.Streams, WsStreamSummary{
						ID:        n.Id,
						Host:      n.Host,
						Port:      n.Port,
						Path:      n.Path,
						IsTLS:     n.IsTls,
						Direction: string(n.Direction),
						Source:    string(n.Source),
						CreatedAt: n.CreatedAt,
					})
				}
				// Non-WebSocket edges are consumed too: resuming from this
				// cursor skips them, which is what keeps next page forward.
				lastCursor = edge.Cursor
			}

			if !resp.Streams.PageInfo.HasNextPage ||
				resp.Streams.PageInfo.EndCursor == nil {
				return nil, output, nil
			}
			after = *resp.Streams.PageInfo.EndCursor
			lastCursor = after
		}

		// Page budget spent and the response is still short: hand back a
		// cursor so the caller can continue instead of losing streams.
		output.HasMore = true
		output.NextCursor = lastCursor
		return nil, output, nil
	}
}

// RegisterListWsStreamsTool registers the tool with the MCP server
func RegisterListWsStreamsTool(server *mcp.Server, client *caido.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:  "caido_list_ws_streams",
		Title: "List WebSocket Streams",
		Description: `List WebSocket streams (connections) from the WebSocket tab. ` +
			`Returns id/host/port/path/direction/source. ` +
			`Use the stream id with caido_list_ws_messages to read frames, and ` +
			`caido_create_replay_session with a captured handshake request to replay one. ` +
			`Pages through mixed traffic so the requested number of WebSocket streams is met. ` +
			`Params: limit (default 20, max 100), after (cursor), scope_id (optional).`,
		Annotations: readOnlyAnn(),
	}, listWsStreamsHandler(client))
}
