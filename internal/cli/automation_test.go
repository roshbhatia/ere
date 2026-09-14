package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPStdioProtocol(t *testing.T) {
	input := strings.Join([]string{
		`{`,
		`{"id":9,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"missing"}`,
		`{"jsonrpc":"2.0","id":4,"method":"ping"}`,
	}, "\n")
	cmd := newMCPCmd(&options{})
	cmd.SetIn(strings.NewReader(input))
	var output bytes.Buffer
	cmd.SetOut(&output)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var responses []struct {
		Result struct {
			Protocol string    `json:"protocolVersion"`
			Tools    []rpcTool `json:"tools"`
		} `json:"result"`
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	dec := json.NewDecoder(&output)
	for dec.More() {
		var response struct {
			Result struct {
				Protocol string    `json:"protocolVersion"`
				Tools    []rpcTool `json:"tools"`
			} `json:"result"`
			Error struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := dec.Decode(&response); err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	if len(responses) != 6 {
		t.Fatalf("responses=%+v", responses)
	}
	if responses[0].Error.Code != -32700 || responses[1].Error.Code != -32600 || responses[4].Error.Code != -32601 {
		t.Fatalf("wrong protocol errors: %+v", responses)
	}
	if responses[2].Result.Protocol != "2025-06-18" || len(responses[3].Result.Tools) != 16 || responses[5].Error.Code != 0 {
		t.Fatalf("roundtrip failed: %+v", responses)
	}
}
