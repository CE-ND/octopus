package openai

import (
	"context"
	"encoding/json"
	"testing"
)

func TestConvertToInternalRequestPreservesRawInputItems(t *testing.T) {
	req := &ResponsesRequest{
		Model: "gpt-4o",
		Input: ResponsesInput{Items: []ResponsesItem{
			{Type: "input_text", Text: stringPtr("hello")},
		}},
	}

	internalReq, err := convertToInternalRequest(req)
	if err != nil {
		t.Fatalf("convertToInternalRequest failed: %v", err)
	}
	if len(internalReq.RawInputItems) == 0 {
		t.Fatalf("expected raw input items to be preserved")
	}

	var items []map[string]any
	if err := json.Unmarshal(internalReq.RawInputItems, &items); err != nil {
		t.Fatalf("unmarshal raw input items failed: %v", err)
	}
	if len(items) != 1 || items[0]["type"] != "input_text" {
		t.Fatalf("expected original raw input items to be kept, got %#v", items)
	}
	if internalReq.TransformOptions.ArrayInputs == nil || !*internalReq.TransformOptions.ArrayInputs {
		t.Fatalf("expected array input flag to stay true")
	}
}

func TestResponseInboundAcceptsSingleInputObject(t *testing.T) {
	inbound := &ResponseInbound{}

	internalReq, err := inbound.TransformRequest(context.Background(), []byte(`{"model":"gpt-4o","input":{"role":"user","content":"hello"}}`))
	if err != nil {
		t.Fatalf("TransformRequest failed: %v", err)
	}
	if len(internalReq.Messages) != 1 {
		t.Fatalf("expected one message, got %#v", internalReq.Messages)
	}
	if internalReq.Messages[0].Role != "user" {
		t.Fatalf("expected user role, got %q", internalReq.Messages[0].Role)
	}
	if internalReq.Messages[0].Content.Content == nil || *internalReq.Messages[0].Content.Content != "hello" {
		t.Fatalf("expected text content to be preserved, got %#v", internalReq.Messages[0].Content)
	}
}

func TestResponseInboundAcceptsImageURLObject(t *testing.T) {
	inbound := &ResponseInbound{}
	body := []byte(`{
		"model": "gpt-4o",
		"input": [{
			"type": "message",
			"role": "user",
			"content": [
				{"type": "input_text", "text": "what is in this image?"},
				{"type": "input_image", "image_url": {"url": "data:image/png;base64,AAA=", "detail": "high"}}
			]
		}]
	}`)

	internalReq, err := inbound.TransformRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("TransformRequest failed: %v", err)
	}
	if len(internalReq.Messages) != 1 {
		t.Fatalf("expected one message, got %#v", internalReq.Messages)
	}
	parts := internalReq.Messages[0].Content.MultipleContent
	if len(parts) != 2 {
		t.Fatalf("expected text and image parts, got %#v", parts)
	}
	if parts[1].ImageURL == nil || parts[1].ImageURL.URL != "data:image/png;base64,AAA=" {
		t.Fatalf("expected image URL object to be normalized, got %#v", parts[1])
	}
	if parts[1].ImageURL.Detail == nil || *parts[1].ImageURL.Detail != "high" {
		t.Fatalf("expected image detail to be preserved, got %#v", parts[1].ImageURL)
	}
}

func TestResponseInboundAcceptsNullItemContent(t *testing.T) {
	inbound := &ResponseInbound{}
	body := []byte(`{
		"model": "gpt-4o",
		"input": [
			{"type": "reasoning", "content": null, "summary": [{"type": "summary_text", "text": "checked"}]},
			{"type": "function_call", "content": null, "call_id": "call_1", "name": "noop", "arguments": "{}"},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "hello"}]}
		]
	}`)

	internalReq, err := inbound.TransformRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("TransformRequest failed: %v", err)
	}
	if len(internalReq.Messages) == 0 {
		t.Fatalf("expected messages to be produced")
	}
}

func TestResponseInboundAcceptsNativeToolCallArgumentsObject(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4o",
		"input": [{
			"type": "tool_search_call",
			"call_id": "call_123",
			"status": "completed",
			"execution": "client",
			"arguments": {
				"query": "node_repl js",
				"limit": 10
			}
		}]
	}`)

	var req ResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal responses request failed: %v", err)
	}
	if len(req.Input.Items) != 1 {
		t.Fatalf("expected one input item, got %#v", req.Input.Items)
	}
	if req.Input.Items[0].Arguments != `{"query":"node_repl js","limit":10}` {
		t.Fatalf("expected object arguments to be preserved as JSON text, got %q", req.Input.Items[0].Arguments)
	}

	inbound := &ResponseInbound{}
	internalReq, err := inbound.TransformRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("TransformRequest failed: %v", err)
	}
	if !internalReq.HasOpenAIResponsesPassthrough() {
		t.Fatalf("expected native tool call input item to require passthrough")
	}
	if reason := internalReq.OpenAIResponsesPassthroughReasonTextValue(); reason != "input:tool_search_call" {
		t.Fatalf("expected passthrough reason input:tool_search_call, got %q", reason)
	}
}

func TestConvertToInternalRequestMarksPassthroughForUnsupportedToolType(t *testing.T) {
	req := &ResponsesRequest{
		Model: "gpt-4o",
		Input: ResponsesInput{Text: stringPtr("hello")},
		Tools: []ResponsesTool{{
			Type: "apply_patch",
		}},
	}

	internalReq, err := convertToInternalRequest(req)
	if err != nil {
		t.Fatalf("convertToInternalRequest failed: %v", err)
	}
	if !internalReq.HasOpenAIResponsesPassthrough() {
		t.Fatalf("expected unsupported responses tool to require passthrough")
	}
	if ext := internalReq.GetOpenAIExtensions(); !ext.ResponsesPassthroughRequired || ext.ResponsesPassthroughReason != "tool:apply_patch" {
		t.Fatalf("expected OpenAI extension passthrough view, got %#v", ext)
	}
}

func TestConvertToInternalRequestMarksPassthroughForUnsupportedInputItem(t *testing.T) {
	req := &ResponsesRequest{
		Model: "gpt-4o",
		Input: ResponsesInput{Items: []ResponsesItem{{
			Type:   "apply_patch_call_output",
			CallID: "apc_123",
		}}},
	}

	internalReq, err := convertToInternalRequest(req)
	if err != nil {
		t.Fatalf("convertToInternalRequest failed: %v", err)
	}
	if !internalReq.HasOpenAIResponsesPassthrough() {
		t.Fatalf("expected unsupported responses input item to require passthrough")
	}
	if ext := internalReq.GetOpenAIExtensions(); !ext.ResponsesPassthroughRequired || ext.ResponsesPassthroughReason != "input:apply_patch_call_output" {
		t.Fatalf("expected OpenAI extension passthrough view, got %#v", ext)
	}
}

func TestConvertToInternalRequestDoesNotMarkPassthroughForSupportedFileAndAudioInputs(t *testing.T) {
	req := &ResponsesRequest{
		Model: "gpt-4o",
		Input: ResponsesInput{Items: []ResponsesItem{
			{
				Type: "message",
				Role: "user",
				Content: &ResponsesInput{Items: []ResponsesItem{
					{Type: "input_file", FileID: stringPtr("file_123")},
					{Type: "input_audio", InputAudio: &ResponsesInputAudio{Format: "wav", Data: "AAA="}},
				}},
			},
		}},
	}

	internalReq, err := convertToInternalRequest(req)
	if err != nil {
		t.Fatalf("convertToInternalRequest failed: %v", err)
	}
	if internalReq.HasOpenAIResponsesPassthrough() {
		t.Fatalf("expected supported file/audio inputs to stay normalized without passthrough")
	}
	if len(internalReq.Messages) != 1 || len(internalReq.Messages[0].Content.MultipleContent) != 2 {
		t.Fatalf("expected supported file/audio inputs to normalize into message content, got %#v", internalReq.Messages)
	}
	if internalReq.Messages[0].Content.MultipleContent[0].Type != "file" {
		t.Fatalf("expected file content part, got %#v", internalReq.Messages[0].Content.MultipleContent[0])
	}
	if internalReq.Messages[0].Content.MultipleContent[1].Type != "input_audio" {
		t.Fatalf("expected input_audio content part, got %#v", internalReq.Messages[0].Content.MultipleContent[1])
	}
}

func TestConvertToInternalRequestNormalizesTopLevelInputFile(t *testing.T) {
	req := &ResponsesRequest{
		Model: "gpt-4o",
		Input: ResponsesInput{Items: []ResponsesItem{{
			Type:     "input_file",
			FileID:   stringPtr("file_456"),
			Filename: stringPtr("notes.txt"),
		}}},
	}

	internalReq, err := convertToInternalRequest(req)
	if err != nil {
		t.Fatalf("convertToInternalRequest failed: %v", err)
	}
	if internalReq.HasOpenAIResponsesPassthrough() {
		t.Fatalf("expected top-level input_file to stay normalized without passthrough")
	}
	if len(internalReq.Messages) != 1 {
		t.Fatalf("expected one normalized message, got %#v", internalReq.Messages)
	}
	if internalReq.Messages[0].Role != "user" {
		t.Fatalf("expected top-level input_file to default to user role, got %#v", internalReq.Messages[0].Role)
	}
	if len(internalReq.Messages[0].Content.MultipleContent) != 1 || internalReq.Messages[0].Content.MultipleContent[0].Type != "file" {
		t.Fatalf("expected top-level input_file to become file content, got %#v", internalReq.Messages[0].Content)
	}
	if internalReq.Messages[0].Content.MultipleContent[0].File == nil || internalReq.Messages[0].Content.MultipleContent[0].File.FileID != "file_456" {
		t.Fatalf("expected normalized file reference to preserve file_id, got %#v", internalReq.Messages[0].Content.MultipleContent[0].File)
	}
}

func stringPtr(value string) *string {
	return &value
}
