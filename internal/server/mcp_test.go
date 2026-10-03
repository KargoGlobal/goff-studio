package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-feature-flag/studio/internal/config"
)

func withMCP(cfg *config.Config) {
	withTokens(cfg)
	cfg.MCP.Enabled = true
}

func mcpPost(t *testing.T, srv *Server, token, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

type toolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Structured map[string]any `json:"structuredContent"`
	IsError    bool           `json:"isError"`
}

func callTool(t *testing.T, srv *Server, token, name string, args map[string]any) toolResult {
	t.Helper()
	params, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	rec := mcpPost(t, srv, token, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":`+string(params)+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d: %s", name, rec.Code, rec.Body)
	}
	var resp struct {
		ID     int        `json:"id"`
		Result toolResult `json:"result"`
		Error  *rpcError  `json:"error"`
	}
	decode(t, rec, &resp)
	if resp.Error != nil {
		t.Fatalf("%s: rpc error %+v", name, resp.Error)
	}
	if resp.ID != 7 {
		t.Errorf("id not echoed: %d", resp.ID)
	}
	return resp.Result
}

func TestMCPInitializeAndListTools(t *testing.T) {
	srv, _, _ := testServerWith(t, newRepo(), automationRules(), withMCP)

	rec := mcpPost(t, srv, readerToken, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var init struct {
		Result struct {
			ProtocolVersion string         `json:"protocolVersion"`
			Capabilities    map[string]any `json:"capabilities"`
			ServerInfo      map[string]any `json:"serverInfo"`
		} `json:"result"`
	}
	decode(t, rec, &init)
	if init.Result.ProtocolVersion != "2025-06-18" || init.Result.Capabilities["tools"] == nil || init.Result.ServerInfo["name"] != "goff-studio" {
		t.Errorf("unexpected initialize result: %+v", init.Result)
	}

	rec = mcpPost(t, srv, readerToken, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	decode(t, rec, &init)
	if init.Result.ProtocolVersion != mcpProtocolVersions[0] {
		t.Errorf("an unknown version should get the latest, got %q", init.Result.ProtocolVersion)
	}

	if rec := mcpPost(t, srv, readerToken, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
		t.Errorf("a notification gets 202 and no body, got %d %q", rec.Code, rec.Body)
	}

	rec = mcpPost(t, srv, readerToken, `{"jsonrpc":"2.0","id":"two","method":"tools/list"}`)
	var list struct {
		ID     string `json:"id"`
		Result struct {
			Tools []struct {
				Name        string         `json:"name"`
				InputSchema map[string]any `json:"inputSchema"`
				Annotations map[string]any `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	decode(t, rec, &list)
	if list.ID != "two" || len(list.Result.Tools) != len(mcpTools) {
		t.Fatalf("unexpected tools/list: %s", rec.Body)
	}
	for _, tool := range list.Result.Tools {
		if tool.Annotations["readOnlyHint"] != true || tool.Annotations["destructiveHint"] != false {
			t.Errorf("%s is not marked read-only: %+v", tool.Name, tool.Annotations)
		}
		if tool.InputSchema["type"] != "object" {
			t.Errorf("%s has no object schema", tool.Name)
		}
	}
}

func TestMCPToolsReadFlags(t *testing.T) {
	repo := newRepo()
	srv, _, _ := testServerWith(t, repo, automationRules(), withMCP)

	envs := callTool(t, srv, readerToken, "list_environments", nil)
	if envs.IsError || !strings.Contains(envs.Content[0].Text, `"production"`) {
		t.Errorf("list_environments: %+v", envs)
	}

	hits := callTool(t, srv, readerToken, "search_flags", map[string]any{"query": "checkout"})
	if hits.IsError || hits.Structured["hits"] == nil || !strings.Contains(hits.Content[0].Text, "new-checkout") {
		t.Errorf("search_flags: %+v", hits)
	}
	hidden := callTool(t, srv, readerToken, "search_flags", map[string]any{"query": "banner"})
	if strings.Contains(hidden.Content[0].Text, "banner-test") {
		t.Error("search leaked a flag the token cannot view")
	}

	flag := callTool(t, srv, readerToken, "get_flag", map[string]any{"environment": "production", "key": "new-checkout"})
	if flag.IsError || flag.Structured["key"] != "new-checkout" || !strings.Contains(flag.Content[0].Text, "gold-cohort") {
		t.Errorf("get_flag: %+v", flag)
	}

	status := callTool(t, srv, readerToken, "get_flag_status", map[string]any{"key": "new-checkout"})
	if status.IsError || !strings.Contains(status.Content[0].Text, `"present": true`) {
		t.Errorf("get_flag_status: %+v", status)
	}

	history := callTool(t, srv, readerToken, "get_flag_history", map[string]any{"environment": "production", "key": "new-checkout", "limit": 5})
	if history.IsError || history.Structured["supported"] != true || !strings.Contains(history.Content[0].Text, "disabled") {
		t.Errorf("get_flag_history: %+v", history)
	}

	eval := callTool(t, srv, readerToken, "evaluate_flag", map[string]any{
		"environment": "production", "key": "new-checkout", "targetingKey": "user-1", "attributes": map[string]any{"tier": "silver"},
	})
	if eval.IsError || !strings.Contains(eval.Content[0].Text, `"off"`) {
		t.Errorf("evaluate_flag: %+v", eval)
	}

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.puts) != 0 {
		t.Errorf("MCP tools wrote %d commits; they must be read-only", len(repo.puts))
	}
}

func TestMCPToolErrorsAreReportedAsToolResults(t *testing.T) {
	srv, _, _ := testServerWith(t, newRepo(), automationRules(), withMCP)

	hidden := callTool(t, srv, readerToken, "get_flag", map[string]any{"environment": "production", "key": "banner-test"})
	if !hidden.IsError || !strings.Contains(hidden.Content[0].Text, "cannot see it") {
		t.Errorf("hidden flag: %+v", hidden)
	}
	missing := callTool(t, srv, readerToken, "get_flag", map[string]any{"environment": "production"})
	if !missing.IsError || !strings.Contains(missing.Content[0].Text, "key is required") {
		t.Errorf("missing argument: %+v", missing)
	}
	traversal := callTool(t, srv, readerToken, "search_flags", map[string]any{"environment": "../etc"})
	if !traversal.IsError {
		t.Errorf("a path-like environment should be refused: %+v", traversal)
	}
}

func TestMCPProtocolErrors(t *testing.T) {
	srv, _, _ := testServerWith(t, newRepo(), automationRules(), withMCP)

	cases := []struct {
		body string
		code int
	}{
		{`{not json`, rpcParseError},
		{`{"jsonrpc":"1.0","id":1,"method":"ping"}`, rpcInvalidRequest},
		{`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`, rpcMethodNotFound},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"toggle_flag"}}`, rpcInvalidParams},
	}
	for _, tc := range cases {
		rec := mcpPost(t, srv, readerToken, tc.body)
		var resp struct {
			Error *rpcError `json:"error"`
		}
		decode(t, rec, &resp)
		if resp.Error == nil || resp.Error.Code != tc.code {
			t.Errorf("%s: want error %d, got %s", tc.body, tc.code, rec.Body)
		}
	}

	rec := mcpPost(t, srv, readerToken, `[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":2,"method":"ping"}]`)
	var batch []rpcResponse
	decode(t, rec, &batch)
	if len(batch) != 2 {
		t.Errorf("a batch answers each request but no notification, got %s", rec.Body)
	}
}

func TestMCPNeedsAToken(t *testing.T) {
	srv, sealer, _ := testServerWith(t, newRepo(), automationRules(), withMCP)

	if rec := mcpPost(t, srv, "", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("no token: %d", rec.Code)
	}
	if rec := mcpPost(t, srv, "wrong", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("bad token: %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	cookie := httptest.NewRecorder()
	if err := sealer.Write(cookie, *admin()); err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie.Result().Cookies()[0])
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("a browser cookie alone must not reach MCP, got %d", rec.Code)
	}

	if rec := mcpPost(t, srv, readerToken, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "Origin", "https://evil.example.net"); rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin: %d", rec.Code)
	}
	if rec := mcpPost(t, srv, readerToken, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "Origin", "https://studio.example.com"); rec.Code != http.StatusOK {
		t.Errorf("same origin: %d", rec.Code)
	}

	get := httptest.NewRecorder()
	srv.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if get.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /mcp: %d", get.Code)
	}
}

func TestMCPIsOffUnlessEnabled(t *testing.T) {
	srv, _, _ := testServerWith(t, newRepo(), automationRules(), withTokens)
	if rec := mcpPost(t, srv, readerToken, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); rec.Code == http.StatusOK {
		t.Errorf("MCP answered while disabled: %d %s", rec.Code, rec.Body)
	}
}
