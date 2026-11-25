package gemini

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bestruirui/octopus/internal/transformer/model"
)

// MessagesInbound implements the Gemini native generateContent protocol for
// /v1beta/models/{model}:generateContent and :streamGenerateContent.
//
// The Gemini request body does not carry the model name; it is supplied by the
// HTTP path and injected by the relay layer before validation.
type MessagesInbound struct {
	streamAggregator model.StreamAggregator
	storedResponse   *model.InternalLLMResponse
}

func (i *MessagesInbound) TransformRequest(ctx context.Context, body []byte) (*model.InternalLLMRequest, error) {
	var request model.GeminiGenerateContentRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("failed to unmarshal gemini request: %w", err)
	}

	internalRequest := &model.InternalLLMRequest{
		RawAPIFormat:        model.APIFormatGeminiContents,
		Metadata:            map[string]string{},
		TransformerMetadata: map[string]string{},
	}

	if systemMessage := convertGeminiSystemInstruction(request.SystemInstruction); systemMessage != nil {
		internalRequest.Messages = append(internalRequest.Messages, *systemMessage)
	}

	for _, content := range request.Contents {
		messages, err := convertGeminiContent(content)
		if err != nil {
			return nil, err
		}
		internalRequest.Messages = append(internalRequest.Messages, messages...)
	}

	applyGeminiGenerationConfig(internalRequest, request.GenerationConfig)
	internalRequest.Tools = convertGeminiTools(request.Tools)
	internalRequest.ToolChoice = convertGeminiToolConfig(request.ToolConfig)

	if len(request.SafetySettings) > 0 {
		if safety, err := json.Marshal(request.SafetySettings); err == nil {
			internalRequest.SetTransformerMetadataValue(model.TransformerMetadataGeminiSafetySettings, string(safety))
		}
	}

	if cachedContent := trimSpace(request.CachedContent); cachedContent != "" {
		internalRequest.GeminiCachedContentRef = &cachedContent
		internalRequest.SetGeminiExtensions(model.GeminiExtension{CachedContentRef: &cachedContent})
	}

	if len(request.Labels) > 0 {
		internalRequest.Metadata = request.Labels
	}

	return internalRequest, nil
}

func (i *MessagesInbound) TransformResponse(ctx context.Context, response *model.InternalLLMResponse) ([]byte, error) {
	i.storedResponse = response

	if response != nil && response.Error != nil {
		return marshalGeminiError(response.Error), nil
	}

	geminiResponse := convertInternalToGeminiResponse(response, false)
	body, err := json.Marshal(geminiResponse)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal gemini response: %w", err)
	}
	return body, nil
}

func (i *MessagesInbound) TransformStream(ctx context.Context, stream *model.InternalLLMResponse) ([]byte, error) {
	if stream == nil {
		return nil, nil
	}

	// Gemini SSE streams end when the connection closes. The OpenAI and
	// Anthropic adapters use an internal [DONE] sentinel; it must not be
	// forwarded as a Gemini data event.
	if stream.Object == "[DONE]" {
		return nil, nil
	}

	i.streamAggregator.Add(stream)

	if stream.Error != nil {
		return []byte("data: " + string(marshalGeminiError(stream.Error)) + "\n\n"), nil
	}

	geminiResponse := convertInternalToGeminiResponse(stream, true)
	if len(geminiResponse.Candidates) == 0 && geminiResponse.UsageMetadata == nil {
		return nil, nil
	}

	body, err := json.Marshal(geminiResponse)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal gemini stream: %w", err)
	}
	return []byte("data: " + string(body) + "\n\n"), nil
}

func (i *MessagesInbound) GetInternalResponse(ctx context.Context) (*model.InternalLLMResponse, error) {
	if i.storedResponse != nil {
		return i.storedResponse, nil
	}
	return i.streamAggregator.BuildAndReset(), nil
}
