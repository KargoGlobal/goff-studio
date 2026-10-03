package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-feature-flag/studio/internal/auth"
)

// The MCP endpoint is stateless Streamable HTTP: every POST gets one JSON reply and no SSE stream.
// It is read-only by construction: no tool here reaches Save, Create, Delete or any other write.

var mcpProtocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

const (
	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602

	mcpInstructions = "Read-only access to GO Feature Flag Studio. Use search_flags or get_flag_status to find a flag " +
		"and the environments it is in, get_flag for its full configuration, get_flag_history for who changed it and why, " +
		"and evaluate_flag to see what a given user would receive. Nothing here changes a flag."
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations"`
	run         func(ctx context.Context, s *Server, sess auth.Session, args mcpArgs) (any, error)
}

type mcpArgs map[string]any

func (a mcpArgs) str(name string) string {
	v, _ := a[name].(string)
	return strings.TrimSpace(v)
}

func (a mcpArgs) required(name string) (string, error) {
	if v := a.str(name); v != "" {
		return v, nil
	}
	return "", invalid("%s is required", name)
}

func (a mcpArgs) number(name string) int {
	if v, ok := a[name].(float64); ok {
		return int(v)
	}
	return 0
}

func schema(required []string, props map[string]any) map[string]any {
	out := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func prop(kind, description string) map[string]any {
	return map[string]any{"type": kind, "description": description}
}

func readOnly() map[string]any {
	return map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}
}

var mcpTools = []mcpTool{
	{
		Name:        "list_environments",
		Title:       "List environments",
		Description: "Lists the flag environments this token can see, and which of them are protected.",
		Annotations: readOnly(),
		InputSchema: schema(nil, map[string]any{}),
		run: func(ctx context.Context, s *Server, sess auth.Session, _ mcpArgs) (any, error) {
			envs, err := s.svc.Environments(ctx, sess)
			return map[string]any{"environments": envs}, err
		},
	},
	{
		Name:  "search_flags",
		Title: "Search flags",
		Description: "Finds flags whose key, team or description contains the query, case-insensitively. " +
			"Leave the query empty to list everything. Each hit says whether the flag is on in that environment.",
		Annotations: readOnly(),
		InputSchema: schema(nil, map[string]any{
			"query":       prop("string", "Text to look for in the flag key, team or description."),
			"environment": prop("string", "Only search this environment; omit to search all of them."),
			"limit":       prop("integer", fmt.Sprintf("Maximum hits, default %d, at most %d.", DefaultSearchResults, MaxSearchResults)),
		}),
		run: func(ctx context.Context, s *Server, sess auth.Session, a mcpArgs) (any, error) {
			return s.svc.FindFlags(ctx, sess, a.str("query"), a.str("environment"), a.number("limit"))
		},
	},
	{
		Name:        "get_flag_status",
		Title:       "Flag status by environment",
		Description: "For one flag key, reports every environment: whether the flag exists there, whether it is on, its team, and whether this token could toggle it.",
		Annotations: readOnly(),
		InputSchema: schema([]string{"key"}, map[string]any{"key": prop("string", "The flag key.")}),
		run: func(ctx context.Context, s *Server, sess auth.Session, a mcpArgs) (any, error) {
			key, err := a.required("key")
			if err != nil {
				return nil, err
			}
			status, err := s.svc.FlagStatus(ctx, sess, key)
			return map[string]any{"key": key, "environments": status}, err
		},
	},
	{
		Name:        "get_flag",
		Title:       "Get flag",
		Description: "Returns one flag's full configuration in an environment: variations, targeting rules, default rule, rollouts, metadata, and a plain-English summary.",
		Annotations: readOnly(),
		InputSchema: schema([]string{"environment", "key"}, map[string]any{
			"environment": prop("string", "The environment name."),
			"key":         prop("string", "The flag key."),
		}),
		run: func(ctx context.Context, s *Server, sess auth.Session, a mcpArgs) (any, error) {
			env, key, err := envAndKey(a)
			if err != nil {
				return nil, err
			}
			return s.svc.Get(ctx, sess, env, key)
		},
	},
	{
		Name:        "get_flag_history",
		Title:       "Flag history",
		Description: "Lists recent changes to a flag, newest first, with who made each change and the recorded message, reason and reference.",
		Annotations: readOnly(),
		InputSchema: schema([]string{"environment", "key"}, map[string]any{
			"environment": prop("string", "The environment name."),
			"key":         prop("string", "The flag key."),
			"limit":       prop("integer", fmt.Sprintf("Maximum entries, default %d, at most %d.", DefaultHistory, MaxHistory)),
		}),
		run: func(ctx context.Context, s *Server, sess auth.Session, a mcpArgs) (any, error) {
			env, key, err := envAndKey(a)
			if err != nil {
				return nil, err
			}
			if !s.svc.Capabilities().History {
				return map[string]any{"supported": false, "changes": []any{}}, nil
			}
			commits, err := s.svc.History(ctx, sess, env, key, a.number("limit"))
			return map[string]any{"supported": true, "changes": commits}, err
		},
	},
	{
		Name:  "evaluate_flag",
		Title: "Evaluate flag",
		Description: "Evaluates a flag for one evaluation context using the real GO Feature Flag engine, " +
			"and returns the variation, value and reason. Useful to confirm who is affected by a flag.",
		Annotations: readOnly(),
		InputSchema: schema([]string{"environment", "key", "targetingKey"}, map[string]any{
			"environment":  prop("string", "The environment name."),
			"key":          prop("string", "The flag key."),
			"targetingKey": prop("string", "The targeting key, usually a user or request ID."),
			"attributes":   map[string]any{"type": "object", "description": "Evaluation context attributes, such as country or plan."},
		}),
		run: func(ctx context.Context, s *Server, sess auth.Session, a mcpArgs) (any, error) {
			env, key, err := envAndKey(a)
			if err != nil {
				return nil, err
			}
			targetingKey, err := a.required("targetingKey")
			if err != nil {
				return nil, err
			}
			attrs, _ := a["attributes"].(map[string]any)
			return s.svc.Preview(ctx, sess, env, key, targetingKey, attrs, nil)
		},
	},
}

func envAndKey(a mcpArgs) (string, string, error) {
	env, err := a.required("environment")
	if err != nil {
		return "", "", err
	}
	key, err := a.required("key")
	return env, key, err
}

func (s *Server) handleMCPNoStream(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Allow", http.MethodPost)
	writeError(w, http.StatusMethodNotAllowed, "this MCP endpoint is stateless; send JSON-RPC requests with POST")
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-origin requests are not accepted")
		return
	}
	// Tokens only: a browser cookie must never be enough for a cross-site page to drive this endpoint.
	if !auth.HasBearer(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="goff-studio"`)
		writeError(w, http.StatusUnauthorized, "send an API token as Authorization: Bearer <token>")
		return
	}
	sess, err := s.tokens.Authenticate(r)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="goff-studio", error="invalid_token"`)
		writeError(w, http.StatusUnauthorized, "that API token is not recognised")
		return
	}

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "the request is too large")
		return
	}

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(trimmed, &batch); err != nil || len(batch) == 0 {
			writeJSON(w, http.StatusOK, rpcFailure(nil, rpcParseError, "could not parse the JSON-RPC batch"))
			return
		}
		var replies []rpcResponse
		for _, item := range batch {
			if reply := s.mcpDispatch(r.Context(), sess, item); reply != nil {
				replies = append(replies, *reply)
			}
		}
		if len(replies) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeJSON(w, http.StatusOK, replies)
		return
	}

	reply := s.mcpDispatch(r.Context(), sess, trimmed)
	if reply == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

// sameOrigin guards against DNS rebinding: a browser always sends Origin, MCP clients usually send none.
func (s *Server) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	base, err := url.Parse(s.cfg.Server.BaseURL)
	if err != nil {
		return false
	}
	got, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(got.Scheme, base.Scheme) && strings.EqualFold(got.Host, base.Host)
}

func rpcFailure(id json.RawMessage, code int, message string) *rpcResponse {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return &rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}}
}

func (s *Server) mcpDispatch(ctx context.Context, sess auth.Session, raw json.RawMessage) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return rpcFailure(nil, rpcParseError, "could not parse the JSON-RPC message")
	}
	notification := len(req.ID) == 0 || string(req.ID) == "null"
	if req.JSONRPC != "2.0" || req.Method == "" {
		if notification {
			return nil
		}
		return rpcFailure(req.ID, rpcInvalidRequest, "expected a JSON-RPC 2.0 request with a method")
	}
	if notification {
		return nil
	}

	ok := func(result any) *rpcResponse {
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
	}

	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		return ok(map[string]any{
			"protocolVersion": negotiateProtocol(params.ProtocolVersion),
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "goff-studio", "title": "GO Feature Flag Studio"},
			"instructions":    mcpInstructions,
		})
	case "ping":
		return ok(map[string]any{})
	case "tools/list":
		return ok(map[string]any{"tools": mcpTools})
	case "tools/call":
		var params struct {
			Name      string  `json:"name"`
			Arguments mcpArgs `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return rpcFailure(req.ID, rpcInvalidParams, "could not read the tool call parameters")
		}
		for _, tool := range mcpTools {
			if tool.Name == params.Name {
				return ok(s.callTool(ctx, sess, tool, params.Arguments))
			}
		}
		return rpcFailure(req.ID, rpcInvalidParams, fmt.Sprintf("there is no tool called %q", params.Name))
	default:
		return rpcFailure(req.ID, rpcMethodNotFound, fmt.Sprintf("method %q is not supported", req.Method))
	}
}

func negotiateProtocol(requested string) string {
	for _, v := range mcpProtocolVersions {
		if v == requested {
			return v
		}
	}
	return mcpProtocolVersions[0]
}

func (s *Server) callTool(ctx context.Context, sess auth.Session, tool mcpTool, args mcpArgs) map[string]any {
	if args == nil {
		args = mcpArgs{}
	}
	result, err := tool.run(ctx, s, sess, args)
	if err != nil {
		return toolError(err)
	}
	text, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return toolError(err)
	}
	out := map[string]any{"content": []map[string]any{{"type": "text", "text": string(text)}}, "isError": false}
	var structured map[string]any
	if json.Unmarshal(text, &structured) == nil {
		out["structuredContent"] = structured
	}
	return out
}

func toolError(err error) map[string]any {
	_, message := serviceErrorMessage(err)
	switch {
	case errors.Is(err, ErrForbidden):
		message = "this API token is not allowed to see that"
	case errors.Is(err, ErrNotFound):
		message = "there is no such flag or environment, or this API token cannot see it"
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": message}}, "isError": true}
}
