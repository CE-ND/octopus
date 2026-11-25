package gemini

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/transformer/model"
)

func TestTransformRequestTextAndGenerationConfig(t *testing.T) {
	body := []byte(`{
		"systemInstruction": {"parts": [{"text": "你是助手"}]},
		"contents": [{"role": "user", "parts": [{"text": "你好"}]}],
		"tools": [{"functionDeclarations": [{
			"name": "lookup",
			"description": "查询",
			"parametersJsonSchema": {"type": "object", "properties": {"q": {"type": "string"}}}
		}]}],
		"generationConfig": {
			"temperature": 0.2,
			"topP": 0.9,
			"topK": 40,
			"maxOutputTokens": 128,
			"stopSequences": ["STOP"],
			"thinkingConfig": {"thinkingBudget": 256, "includeThoughts": true}
		}
	}`)

	request, err := (&MessagesInbound{}).TransformRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("TransformRequest() error = %v", err)
	}
	if len(request.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(request.Messages))
	}
	if request.Messages[0].Role != "system" || request.Messages[1].Role != "user" {
		t.Fatalf("unexpected roles: %#v", []string{request.Messages[0].Role, request.Messages[1].Role})
	}
	if request.Messages[1].Content.Content == nil || *request.Messages[1].Content.Content != "你好" {
		t.Fatalf("unexpected user content: %#v", request.Messages[1].Content)
	}
	if request.MaxTokens == nil || *request.MaxTokens != 128 {
		t.Fatalf("expected max tokens 128, got %#v", request.MaxTokens)
	}
	if request.TopK == nil || *request.TopK != 40 {
		t.Fatalf("expected top_k 40, got %#v", request.TopK)
	}
	if request.Stop == nil || len(request.Stop.MultipleStop) != 1 || request.Stop.MultipleStop[0] != "STOP" {
		t.Fatalf("unexpected stop: %#v", request.Stop)
	}
	if request.ReasoningBudget == nil || *request.ReasoningBudget != 256 {
		t.Fatalf("expected reasoning budget 256, got %#v", request.ReasoningBudget)
	}
	if len(request.Tools) != 1 || request.Tools[0].Type != "function" {
		t.Fatalf("unexpected tools: %#v", request.Tools)
	}
	if !strings.Contains(string(request.Tools[0].Function.Parameters), `"q"`) {
		t.Fatalf("parametersJsonSchema was not converted: %s", request.Tools[0].Function.Parameters)
	}
}

func TestTransformRequestFunctionCallAndResponse(t *testing.T) {
	body := []byte(`{
		"contents": [
			{"role": "user", "parts": [{"text": "查一下"}]},
			{"role": "model", "parts": [{
				"functionCall": {"id": "call_1", "name": "lookup", "args": {"q": "octopus"}},
				"thoughtSignature": "sig-1"
			}]},
			{"role": "user", "parts": [{
				"functionResponse": {"id": "call_1", "name": "lookup", "response": {"output": "ok"}}
			}]}
		]
	}`)

	request, err := (&MessagesInbound{}).TransformRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("TransformRequest() error = %v", err)
	}
	if len(request.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(request.Messages))
	}

	assistant := request.Messages[1]
	if assistant.Role != "assistant" || len(assistant.ToolCalls) != 1 {
		t.Fatalf("unexpected assistant message: %#v", assistant)
	}
	toolCall := assistant.ToolCalls[0]
	if toolCall.ID != "call_1" || toolCall.Function.Name != "lookup" {
		t.Fatalf("unexpected tool call: %#v", toolCall)
	}
	if toolCall.ThoughtSignature != "sig-1" {
		t.Fatalf("expected thought signature sig-1, got %q", toolCall.ThoughtSignature)
	}
	if len(assistant.ReasoningBlocks) != 1 || assistant.ReasoningBlocks[0].ToolCallID != "call_1" {
		t.Fatalf("expected signature block anchored to call_1, got %#v", assistant.ReasoningBlocks)
	}

	toolMessage := request.Messages[2]
	if toolMessage.Role != "tool" || toolMessage.ToolCallID == nil || *toolMessage.ToolCallID != "call_1" {
		t.Fatalf("unexpected tool message: %#v", toolMessage)
	}
	if toolMessage.Content.Content == nil || !strings.Contains(*toolMessage.Content.Content, "ok") {
		t.Fatalf("unexpected tool content: %#v", toolMessage.Content)
	}
}

func TestTransformResponseTextAndUsage(t *testing.T) {
	response := &model.InternalLLMResponse{
		ID:      "resp-1",
		Model:   "gemini-4-neon-c-ep",
		Created: 1760000000,
		Choices: []model.Choice{{
			Index: 0,
			Message: &model.Message{
				Role:    "assistant",
				Content: model.MessageContent{Content: stringPointer("你好")},
			},
			FinishReason: stringPointer("stop"),
		}},
		Usage: &model.Usage{
			PromptTokens:     10,
			CompletionTokens: 2,
			TotalTokens:      12,
			PromptTokensDetails: &model.PromptTokensDetails{
				CachedTokens: 3,
			},
			CompletionTokensDetails: &model.CompletionTokensDetails{
				ReasoningTokens: 1,
			},
		},
	}

	body, err := (&MessagesInbound{}).TransformResponse(context.Background(), response)
	if err != nil {
		t.Fatalf("TransformResponse() error = %v", err)
	}

	var result model.GeminiGenerateContentResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].Content == nil {
		t.Fatalf("unexpected candidates: %#v", result.Candidates)
	}
	parts := result.Candidates[0].Content.Parts
	if len(parts) != 1 || parts[0].Text != "你好" {
		t.Fatalf("unexpected parts: %#v", parts)
	}
	if result.Candidates[0].FinishReason == nil || *result.Candidates[0].FinishReason != "STOP" {
		t.Fatalf("unexpected finish reason: %#v", result.Candidates[0].FinishReason)
	}
	if result.UsageMetadata == nil || result.UsageMetadata.CachedContentTokenCount != 3 || result.UsageMetadata.ThoughtsTokenCount != 1 {
		t.Fatalf("unexpected usage: %#v", result.UsageMetadata)
	}
}

func TestTransformStreamOmitsDone(t *testing.T) {
	inbound := &MessagesInbound{}
	ctx := context.Background()

	done, err := inbound.TransformStream(ctx, &model.InternalLLMResponse{Object: "[DONE]"})
	if err != nil {
		t.Fatalf("TransformStream([DONE]) error = %v", err)
	}
	if len(done) != 0 {
		t.Fatalf("expected no bytes for [DONE], got %q", done)
	}

	chunk := &model.InternalLLMResponse{
		Object: "chat.completion.chunk",
		Choices: []model.Choice{{
			Index: 0,
			Delta: &model.Message{
				Role:    "assistant",
				Content: model.MessageContent{Content: stringPointer("hi")},
			},
		}},
	}
	out, err := inbound.TransformStream(ctx, chunk)
	if err != nil {
		t.Fatalf("TransformStream() error = %v", err)
	}
	if !strings.HasPrefix(string(out), "data: ") || strings.Contains(string(out), "[DONE]") {
		t.Fatalf("unexpected stream framing: %q", out)
	}

	aggregated, err := inbound.GetInternalResponse(ctx)
	if err != nil {
		t.Fatalf("GetInternalResponse() error = %v", err)
	}
	if aggregated == nil || len(aggregated.Choices) != 1 || aggregated.Choices[0].Message.Content.Content == nil {
		t.Fatalf("unexpected aggregated response: %#v", aggregated)
	}
	if *aggregated.Choices[0].Message.Content.Content != "hi" {
		t.Fatalf("unexpected aggregated content: %q", *aggregated.Choices[0].Message.Content.Content)
	}
}

func TestTransformResponsePreservesToolCallSignature(t *testing.T) {
	response := &model.InternalLLMResponse{
		ID:    "resp-tool",
		Model: "gemini-4-neon-c-ep",
		Choices: []model.Choice{{
			Index: 0,
			Message: &model.Message{
				Role: "assistant",
				ToolCalls: []model.ToolCall{{
					Index:            0,
					ID:               "call_1",
					Type:             "function",
					Function:         model.FunctionCall{Name: "lookup", Arguments: `{"q":"octopus"}`},
					ThoughtSignature: "sig-1",
				}},
			},
			FinishReason: stringPointer("tool_calls"),
		}},
	}

	body, err := (&MessagesInbound{}).TransformResponse(context.Background(), response)
	if err != nil {
		t.Fatalf("TransformResponse() error = %v", err)
	}

	var result model.GeminiGenerateContentResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].Content == nil {
		t.Fatalf("unexpected candidates: %#v", result.Candidates)
	}
	parts := result.Candidates[0].Content.Parts
	if len(parts) != 1 || parts[0].FunctionCall == nil {
		t.Fatalf("unexpected parts: %#v", parts)
	}
	if parts[0].FunctionCall.ID != "call_1" || parts[0].FunctionCall.Name != "lookup" {
		t.Fatalf("unexpected function call: %#v", parts[0].FunctionCall)
	}
	if parts[0].ThoughtSignature != "sig-1" {
		t.Fatalf("expected thought signature sig-1, got %q", parts[0].ThoughtSignature)
	}
	if parts[0].FunctionCall.Args["q"] != "octopus" {
		t.Fatalf("unexpected function args: %#v", parts[0].FunctionCall.Args)
	}
}
